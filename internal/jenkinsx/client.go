// SPDX-License-Identifier: GPL-3.0-or-later

// Package jenkinsx owns the Jenkins REST client for jenkins-mcp. It is named
// jenkinsx (not jenkins) to avoid colliding with the binary/module name
// throughout this codebase, the same rationale the AWS predecessor used for
// its "awsx" package.
//
// Jenkins has no SDK: every operation is a plain JSON-over-HTTP call against
// the controller's REST API, authenticated with HTTP Basic auth using a
// username and API token.
package jenkinsx

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// maxErrorBodyBytes caps how much of an error response body is read into a
// StatusError, so a misbehaving endpoint returning an enormous body can't
// blow up memory.
const maxErrorBodyBytes = 4096

// Client is a Jenkins REST API client. All methods are safe for concurrent
// use.
type Client struct {
	http  *http.Client
	base  *url.URL
	user  string
	token string

	mu    sync.Mutex
	crumb *crumb // cached CSRF crumb; nil until first POST
}

// NewClient creates a Client for the Jenkins controller at baseURL,
// authenticating as user with the given API token. A nil httpClient uses
// http.DefaultClient.
func NewClient(baseURL, user, token string, httpClient *http.Client) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("jenkinsx: base URL is required")
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil {
		return nil, fmt.Errorf("jenkinsx: parsing base URL %q: %w", baseURL, err)
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{http: httpClient, base: u, user: user, token: token}, nil
}

// Get performs a GET request against path (relative to the base URL),
// attaching query as the query string and decoding the JSON response body
// into out. out may be nil to discard the body.
func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	_, err := c.GetWithHeaders(ctx, path, query, out)
	return err
}

// GetWithHeaders behaves like Get but also returns the response headers, for
// endpoints where information travels in a header rather than the JSON body
// (e.g. Jenkins reports its own version via the X-Jenkins header, not in any
// endpoint's JSON response).
func (c *Client) GetWithHeaders(ctx context.Context, path string, query url.Values, out any) (http.Header, error) {
	resp, err := c.do(ctx, http.MethodGet, path, query, nil, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if out == nil {
		return resp.Header, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.Header, fmt.Errorf("jenkinsx: decoding response from %s: %w", path, err)
	}
	return resp.Header, nil
}

// Text performs a GET request and returns the raw response body as text
// along with the response headers, for endpoints (like the build console
// log) that don't return JSON.
func (c *Client) Text(ctx context.Context, path string, query url.Values) (string, http.Header, error) {
	resp, err := c.do(ctx, http.MethodGet, path, query, nil, nil)
	if err != nil {
		return "", nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", nil, fmt.Errorf("jenkinsx: reading response from %s: %w", path, err)
	}
	return string(b), resp.Header, nil
}

// Head performs a HEAD request and returns only the response headers.
//
// This exists for one specific job: learning how large a build's console log
// is without downloading it. Jenkins reports the log's size in X-Text-Size,
// but reading progressiveText with a start offset past the end of the log
// does NOT return an empty body — it returns the whole log from the
// beginning (verified against Jenkins 2.568.3), so a GET cannot be used as a
// cheap size probe. HEAD returns the same X-Text-Size with no body at all.
func (c *Client) Head(ctx context.Context, path string, query url.Values) (http.Header, error) {
	resp, err := c.do(ctx, http.MethodHead, path, query, nil, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.Header, nil
}

// PostForm performs a POST request with a form-encoded body, attaching a
// CSRF crumb header when Jenkins requires one. It decodes a JSON response
// body into out when out is non-nil (some endpoints, like triggering a
// build, return no body and communicate their result via a header instead;
// callers use the returned http.Header for those).
func (c *Client) PostForm(ctx context.Context, path string, query, form url.Values, out any) (http.Header, error) {
	resp, err := c.postForm(ctx, path, query, form, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return resp.Header, fmt.Errorf("jenkinsx: decoding response from %s: %w", path, err)
		}
	}
	return resp.Header, nil
}

// do issues a single HTTP request with Basic auth and the given extra
// headers, returning the response on success (2xx/3xx) or a *StatusError on
// a 4xx/5xx status. The caller is responsible for closing the returned
// response's body on success.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, headers map[string]string) (*http.Response, error) {
	// Every request in the server funnels through here, which makes this the
	// one place that can guarantee a caller-supplied path segment cannot
	// walk out of the endpoint it was meant for. url.URL.JoinPath resolves
	// ".." itself (it runs path.Join), so a job name, artifact path or stage
	// ID containing ".." silently rewrites the target: an artifact read of
	// "../../../../job/other/config.xml" becomes a read of another job's
	// configuration, and with a base URL that has a path prefix, enough of
	// them leave Jenkins altogether — still carrying this request's Basic
	// auth header, which would hand the API token to whatever else is hosted
	// there. Rejecting the segment here kills that whole class rather than
	// relying on each tool to sanitize its own inputs.
	if err := rejectTraversal(path); err != nil {
		return nil, err
	}

	full := c.base.JoinPath(path)
	if len(query) > 0 {
		full.RawQuery = query.Encode()
	}

	// Defense in depth: even with ".." rejected, refuse anything that ended
	// up outside the configured base path.
	if got := absPath(full.EscapedPath()); !strings.HasPrefix(got, basePathPrefix(c.base)) {
		return nil, fmt.Errorf("jenkinsx: refusing request to %q, which is outside the configured Jenkins base URL", got)
	}

	req, err := http.NewRequestWithContext(ctx, method, full.String(), body)
	if err != nil {
		return nil, fmt.Errorf("jenkinsx: building request: %w", err)
	}
	req.SetBasicAuth(c.user, c.token)
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("jenkinsx: %s %s: %w", method, path, err)
	}

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
		_ = resp.Body.Close()
		return nil, &StatusError{StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(b))}
	}
	return resp, nil
}

// rejectTraversal returns an error if any segment of p is "." or "..".
//
// Both are path-normalization elements that url.URL.JoinPath resolves via
// path.Join, so both silently retarget a request. ".." climbs a level; "."
// is subtler but just as wrong — it makes a segment VANISH, so a job path
// of "." turns /job/./lastBuild/api/json into /job/lastBuild/api/json, the
// detail of a job named "lastBuild", returned as a success rather than an
// input error.
//
// Segments are compared whole rather than as substrings: a job or artifact
// legitimately named "my..build" or "v1.2" is fine, and only a complete "."
// or ".." segment normalizes. Both the raw and percent-decoded forms are
// checked, since "%2e%2e" decodes to ".." inside url.URL.
func rejectTraversal(p string) error {
	candidates := []string{p}
	if decoded, err := url.PathUnescape(p); err == nil && decoded != p {
		candidates = append(candidates, decoded)
	}
	for _, c := range candidates {
		for _, seg := range strings.Split(c, "/") {
			if seg == "." || seg == ".." {
				return fmt.Errorf("jenkinsx: refusing request path %q: a %q path segment would change which endpoint is requested", p, seg)
			}
		}
	}
	return nil
}

// basePathPrefix returns the base URL's path with leading and trailing
// slashes, the prefix every request path must keep once joined.
func basePathPrefix(base *url.URL) string {
	p := absPath(base.EscapedPath())
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// absPath normalizes a URL path to start with "/". url.URL.JoinPath leaves
// the result relative when the base URL has an empty path (the common case
// for a bare https://ci.example.com), which URL.String later fixes up but
// which would break a naive prefix comparison.
func absPath(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}
