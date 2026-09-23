// SPDX-License-Identifier: GPL-3.0-or-later

package jenkinsx

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
)

// crumb is a cached Jenkins CSRF protection token. A disabled crumb
// (Jenkins' crumbIssuer endpoint returns 404 when CSRF protection is off)
// is cached too, so every subsequent POST doesn't re-probe for one.
type crumb struct {
	field    string
	value    string
	disabled bool
}

// ensureCrumb returns the cached crumb, fetching and caching one on first
// use. A nil result (with a nil error) means CSRF protection is disabled and
// no crumb header should be sent.
func (c *Client) ensureCrumb(ctx context.Context) (*crumb, error) {
	c.mu.Lock()
	cached := c.crumb
	c.mu.Unlock()
	if cached != nil {
		if cached.disabled {
			return nil, nil
		}
		return cached, nil
	}

	var body struct {
		Crumb             string `json:"crumb"`
		CrumbRequestField string `json:"crumbRequestField"`
	}
	if err := c.Get(ctx, "/crumbIssuer/api/json", nil, &body); err != nil {
		if IsNotFound(err) {
			cr := &crumb{disabled: true}
			c.mu.Lock()
			c.crumb = cr
			c.mu.Unlock()
			return nil, nil
		}
		return nil, err
	}

	cr := &crumb{field: body.CrumbRequestField, value: body.Crumb}
	c.mu.Lock()
	c.crumb = cr
	c.mu.Unlock()
	return cr, nil
}

// postForm attaches a crumb header (if CSRF protection is enabled) and
// issues the POST. If Jenkins rejects the request with a 403 (its standard
// response to a missing/stale/invalid crumb — not matched on wording, since
// Jenkins' error text varies by version, plugin, and locale), the cached
// crumb is cleared and the request is retried exactly once with a freshly
// fetched one. A 403 caused by something other than the crumb (e.g. a
// genuine permissions error) simply fails the same way on the retry, at the
// cost of one extra request.
func (c *Client) postForm(ctx context.Context, path string, query, form url.Values, retried bool) (*http.Response, error) {
	cr, err := c.ensureCrumb(ctx)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{"Content-Type": "application/x-www-form-urlencoded"}
	if cr != nil {
		headers[cr.field] = cr.value
	}

	resp, err := c.do(ctx, http.MethodPost, path, query, strings.NewReader(form.Encode()), headers)
	if err != nil {
		var statusErr *StatusError
		if !retried && errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusForbidden {
			c.mu.Lock()
			c.crumb = nil
			c.mu.Unlock()
			return c.postForm(ctx, path, query, form, true)
		}
		return nil, err
	}
	return resp, nil
}
