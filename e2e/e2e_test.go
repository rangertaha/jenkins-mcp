// SPDX-License-Identifier: GPL-3.0-or-later

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"
)

// jobName is the freestyle job the suite creates and builds.
const jobName = "e2e-demo-job"

// consoleMarker is echoed by the job so the console-log test has something
// unambiguous to look for.
const consoleMarker = "E2E_MARKER_OUTPUT"

// stuckJobName is a job that can never start, used to put a deterministic
// item in the build queue.
const stuckJobName = "e2e-stuck-job"

// unbuildableConfig pins the job to a label no node has and forbids roaming,
// so Jenkins queues a build of it forever instead of running it. That makes
// the queue observable without racing a real build's execution.
const unbuildableConfig = `<?xml version='1.1' encoding='UTF-8'?>
<project>
  <description>jenkins-mcp e2e queue fixture: never runs</description>
  <keepDependencies>false</keepDependencies>
  <properties/>
  <scm class="hudson.scm.NullSCM"/>
  <assignedNode>label-no-node-has</assignedNode>
  <canRoam>false</canRoam>
  <disabled>false</disabled>
  <triggers/>
  <concurrentBuild>false</concurrentBuild>
  <builders/>
  <publishers/>
  <buildWrappers/>
</project>`

// freestyleConfig is a minimal freestyle project. Everything referenced here
// (FreeStyleProject, NullSCM, hudson.tasks.Shell) is Jenkins core, so this
// works on a plugin-less instance.
const freestyleConfig = `<?xml version='1.1' encoding='UTF-8'?>
<project>
  <description>jenkins-mcp e2e test job</description>
  <keepDependencies>false</keepDependencies>
  <properties/>
  <scm class="hudson.scm.NullSCM"/>
  <canRoam>true</canRoam>
  <disabled>false</disabled>
  <blockBuildWhenDownstreamBuilding>false</blockBuildWhenDownstreamBuilding>
  <blockBuildWhenUpstreamBuilding>false</blockBuildWhenUpstreamBuilding>
  <triggers/>
  <concurrentBuild>false</concurrentBuild>
  <builders>
    <hudson.tasks.Shell>
      <command>echo ` + consoleMarker + `</command>
    </hudson.tasks.Shell>
  </builders>
  <publishers/>
  <buildWrappers/>
</project>`

// TestEndToEnd boots a real Jenkins in Docker and drives the compiled binary
// against it over real JSON-RPC stdio. One container is shared by all
// subtests, which run in order (list-before-create matters).
func TestEndToEnd(t *testing.T) {
	jenkins := startJenkins(t)
	bin := buildBinary(t)
	client := startMCPServer(t, bin, jenkins.env(), 10*time.Minute)

	// Asserts the tools a client actually receives over the wire, by name.
	// The exact count is pinned in-process by register_test.go; repeating a
	// bare number here only produces a stale assertion every time a tool is
	// added, which is exactly what happened when the toolset grew from 14.
	t.Run("tools_list", func(t *testing.T) {
		resp := client.request("tools/list", map[string]any{})
		result, _ := resp["result"].(map[string]any)
		raw, _ := result["tools"].([]any)

		got := make(map[string]bool, len(raw))
		for _, entry := range raw {
			tool, _ := entry.(map[string]any)
			if name, _ := tool["name"].(string); name != "" {
				got[name] = true
			}
		}

		// One representative tool per toolset: if a toolset failed to
		// register, its entry here goes missing.
		for _, want := range []string{
			"jenkins_list_jobs", "jenkins_get_build", "jenkins_list_queue",
			"jenkins_list_nodes", "jenkins_list_views", "jenkins_whoami",
		} {
			if !got[want] {
				t.Errorf("tools/list is missing %s; got %d tools: %v", want, len(raw), keysOf(got))
			}
		}
	})

	t.Run("whoami", func(t *testing.T) {
		got := client.mustCallTool("jenkins_whoami", nil)
		if name, _ := got["name"].(string); name != adminUser {
			t.Errorf("name = %v, want %s", got["name"], adminUser)
		}
		if auth, _ := got["authenticated"].(bool); !auth {
			t.Errorf("authenticated = %v, want true (API token auth failed against real Jenkins)", got["authenticated"])
		}
		if anon, _ := got["anonymous"].(bool); anon {
			t.Errorf("anonymous = true, want false — the token was not accepted")
		}
	})

	t.Run("system_info", func(t *testing.T) {
		got := client.mustCallTool("jenkins_system_info", nil)
		version, _ := got["version"].(string)
		if version == "" {
			t.Error("version is empty; the X-Jenkins response header was not read")
		}
		if useSecurity, _ := got["useSecurity"].(bool); !useSecurity {
			t.Errorf("useSecurity = %v, want true (the init script enables a security realm)", got["useSecurity"])
		}
		t.Logf("real Jenkins version: %s", version)
	})

	t.Run("list_jobs_empty", func(t *testing.T) {
		got := client.mustCallTool("jenkins_list_jobs", nil)
		if count, _ := got["count"].(float64); count != 0 {
			t.Errorf("count = %v, want 0 on a fresh instance", got["count"])
		}
	})

	// Create the job out-of-band (the server exposes no job-creation tool),
	// so the read paths have something real to find.
	createJob(t, jenkins, jobName, freestyleConfig)

	t.Run("list_jobs_after_create", func(t *testing.T) {
		got := client.mustCallTool("jenkins_list_jobs", nil)
		list := items(t, "jenkins_list_jobs", got)
		if len(list) != 1 {
			t.Fatalf("got %d jobs, want 1: %v", len(list), got)
		}
		if name, _ := list[0]["name"].(string); name != jobName {
			t.Errorf("name = %v, want %s", list[0]["name"], jobName)
		}
		if fullName, _ := list[0]["fullName"].(string); fullName != jobName {
			t.Errorf("fullName = %v, want %s", list[0]["fullName"], jobName)
		}
		if class, _ := list[0]["class"].(string); !strings.Contains(class, "FreeStyleProject") {
			t.Errorf("class = %v, want it to mention FreeStyleProject", list[0]["class"])
		}
	})

	t.Run("get_job", func(t *testing.T) {
		got := client.mustCallTool("jenkins_get_job", map[string]any{"job": jobName})
		if name, _ := got["name"].(string); name != jobName {
			t.Errorf("name = %v, want %s", got["name"], jobName)
		}
		if buildable, _ := got["buildable"].(bool); !buildable {
			t.Errorf("buildable = %v, want true", got["buildable"])
		}
		if next, _ := got["nextBuildNumber"].(float64); next != 1 {
			t.Errorf("nextBuildNumber = %v, want 1 before any build", got["nextBuildNumber"])
		}
	})

	// The highest-value path: a real mutating call (which needs a real CSRF
	// crumb), the queue-item Location header parse, then polling the build
	// and reading its progressive console log.
	t.Run("trigger_build_and_read_console", func(t *testing.T) {
		trigger := client.mustCallTool("jenkins_trigger_build", map[string]any{"job": jobName})
		queueURL, _ := trigger["queueUrl"].(string)
		if queueURL == "" {
			t.Error("queueUrl is empty; Jenkins' Location header was not captured")
		}
		queueID, _ := trigger["queueId"].(float64)
		if queueID <= 0 {
			t.Errorf("queueId = %v, want a positive ID parsed from %q", trigger["queueId"], queueURL)
		}

		build := waitForBuild(t, client, jobName)
		if result, _ := build["result"].(string); result != "SUCCESS" {
			t.Errorf("build result = %v, want SUCCESS", build["result"])
		}
		if number, _ := build["number"].(float64); number != 1 {
			t.Errorf("build number = %v, want 1", build["number"])
		}

		text, nextStart := readFullConsole(t, client, jobName)
		if !strings.Contains(text, consoleMarker) {
			t.Errorf("console log does not contain %q; got:\n%s", consoleMarker, text)
		}
		if nextStart <= 0 {
			t.Errorf("final nextStart = %v, want a positive byte offset from X-Text-Size", nextStart)
		}
	})

	t.Run("get_build_by_number", func(t *testing.T) {
		got := client.mustCallTool("jenkins_get_build", map[string]any{"job": jobName, "build": "1"})
		if number, _ := got["number"].(float64); number != 1 {
			t.Errorf("number = %v, want 1", got["number"])
		}
	})

	t.Run("list_nodes", func(t *testing.T) {
		got := client.mustCallTool("jenkins_list_nodes", nil)
		list := items(t, "jenkins_list_nodes", got)
		if len(list) == 0 {
			t.Fatal("no nodes returned; the built-in node should always be present")
		}
		node := list[0]
		t.Logf("built-in node displayName = %q", node["displayName"])
		if offline, _ := node["offline"].(bool); offline {
			t.Errorf("built-in node is offline: %v", node)
		}
		if n, _ := node["numExecutors"].(float64); n <= 0 {
			t.Errorf("numExecutors = %v, want > 0", node["numExecutors"])
		}
	})

	// jenkins_get_node works when handed the controller's real URL segment,
	// which is "(built-in)" — confirmed against live Jenkins, where
	// /computer/(built-in)/api/json returns 200 (as does the legacy
	// "(master)" alias).
	t.Run("get_node_by_url_segment", func(t *testing.T) {
		got := client.mustCallTool("jenkins_get_node", map[string]any{"name": "(built-in)"})
		if name, _ := got["displayName"].(string); name == "" {
			t.Errorf("displayName is empty: %v", got)
		}
		if n, _ := got["numExecutors"].(float64); n <= 0 {
			t.Errorf("numExecutors = %v, want > 0", got["numExecutors"])
		}
	})

	// The chaining contract: whatever jenkins_list_nodes reports as `name`
	// must be directly usable as jenkins_get_node's `name`. For the
	// controller this is exactly where it used to break — real Jenkins
	// reports displayName "Built-In Node", which 404s as a URL segment,
	// while the usable segment is "(built-in)". Only `name` carries it.
	t.Run("get_node_roundtrip_from_list", func(t *testing.T) {
		listed := client.mustCallTool("jenkins_list_nodes", nil)
		list := items(t, "jenkins_list_nodes", listed)
		if len(list) == 0 {
			t.Fatal("no nodes to round-trip")
		}
		name, _ := list[0]["name"].(string)
		if name == "" {
			t.Fatalf("jenkins_list_nodes returned no name field: %v", list[0])
		}

		res := client.callTool("jenkins_get_node", map[string]any{"name": name})
		if res.isError {
			t.Errorf("jenkins_get_node(%q) failed, but %q is what jenkins_list_nodes reported "+
				"as name and what jenkins_get_node's description tells the model to pass", name, name)
			return
		}
		if got, _ := res.structured["name"].(string); got != name {
			t.Errorf("round-tripped name = %q, want %q", got, name)
		}
	})

	// A plugin-less instance is a legitimate state for the stock image, so
	// a non-empty list can't be required here. What CAN be required is that
	// the page is self-consistent: count must equal the number of items
	// returned. Asserting merely that a "count" key exists proves nothing —
	// PageResult.Count has no omitempty, so it is present in every result
	// even when the tool is hitting the wrong endpoint entirely.
	t.Run("list_plugins", func(t *testing.T) {
		got := client.mustCallTool("jenkins_list_plugins", nil)
		assertCoherentPage(t, "jenkins_list_plugins", got)

		for _, p := range items(t, "jenkins_list_plugins", got) {
			if name, _ := p["shortName"].(string); name == "" {
				t.Errorf("plugin entry has no shortName: %v", p)
			}
		}
	})

	// Real coverage for the queue toolset. A job pinned to a label no node
	// has can never start, so it sits in the queue deterministically —
	// unlike polling for an ordinary build, which races its own execution.
	// This exercises list_queue against a genuinely non-empty queue and
	// gives jenkins_cancel_queue_item its only end-to-end test.
	t.Run("queue_lists_and_cancels_a_stuck_build", func(t *testing.T) {
		createJob(t, jenkins, stuckJobName, unbuildableConfig)
		client.mustCallTool("jenkins_trigger_build", map[string]any{"job": stuckJobName})

		var id float64
		var why string
		deadline := time.Now().Add(60 * time.Second)
		for time.Now().Before(deadline) {
			got := client.mustCallTool("jenkins_list_queue", nil)
			assertCoherentPage(t, "jenkins_list_queue", got)
			for _, item := range items(t, "jenkins_list_queue", got) {
				if name, _ := item["taskName"].(string); name == stuckJobName {
					id, _ = item["id"].(float64)
					why, _ = item["why"].(string)
				}
			}
			if id > 0 {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if id <= 0 {
			t.Fatalf("%s never appeared in the queue; jenkins_list_queue is not reporting queued items", stuckJobName)
		}
		if why == "" {
			t.Errorf("queue item %v has no why; Jenkins always explains what an item waits for", id)
		}
		t.Logf("stuck queue item %v: %s", id, why)

		cancelled := client.mustCallTool("jenkins_cancel_queue_item", map[string]any{"id": id})
		if ok, _ := cancelled["canceled"].(bool); !ok {
			t.Errorf("cancel reported canceled=false: %v", cancelled)
		}

		// The cancellation must actually take effect, not just report success.
		deadline = time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			still := false
			for _, item := range items(t, "jenkins_list_queue", client.mustCallTool("jenkins_list_queue", nil)) {
				if name, _ := item["taskName"].(string); name == stuckJobName {
					still = true
				}
			}
			if !still {
				return
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Errorf("%s is still queued after jenkins_cancel_queue_item reported success", stuckJobName)
	})

	t.Run("list_views", func(t *testing.T) {
		got := client.mustCallTool("jenkins_list_views", nil)
		list := items(t, "jenkins_list_views", got)
		if len(list) == 0 {
			t.Fatal("no views returned; Jenkins always has at least the 'all' view")
		}
		var names []string
		for _, v := range list {
			name, _ := v["name"].(string)
			names = append(names, name)
		}
		t.Logf("views: %v", names)
	})

	t.Run("get_view", func(t *testing.T) {
		listed := client.mustCallTool("jenkins_list_views", nil)
		list := items(t, "jenkins_list_views", listed)
		if len(list) == 0 {
			// Without this guard an empty list panics on list[0], and a Go
			// test panic kills the process: every later subtest AND the
			// whole pipeline suite would silently never run.
			t.Fatal("no views returned; cannot round-trip into jenkins_get_view")
		}
		name, _ := list[0]["name"].(string)

		// jenkins_get_view returns a ViewDetail, not a ListResult: its jobs
		// live under "jobs", not "items".
		got := client.mustCallTool("jenkins_get_view", map[string]any{"name": name})
		jobs := objects(t, "jenkins_get_view", got, "jobs")
		if len(jobs) == 0 {
			t.Fatalf("view %q lists no jobs, but %s exists and should appear in the default view", name, jobName)
		}
		// Regression guard for the tree= string that once omitted
		// fullName/_class for embedded jobs: both must survive against real
		// Jenkins, not just against a mock whose fixture happens to include
		// them.
		if fullName, _ := jobs[0]["fullName"].(string); fullName != jobName {
			t.Errorf("view job fullName = %v, want %s (the tree= query may be dropping fullName)", jobs[0]["fullName"], jobName)
		}
		if class, _ := jobs[0]["class"].(string); class == "" {
			t.Error("view job class is empty (the tree= query may be dropping _class)")
		}
	})

	t.Run("get_job_rejects_missing_job", func(t *testing.T) {
		res := client.callTool("jenkins_get_job", map[string]any{"job": "no-such-job-here"})
		if !res.isError {
			t.Errorf("expected a tool error for a nonexistent job, got: %v", res.structured)
		}
	})

	// Everything below covers the resource and prompt surfaces over real
	// stdio. Both are otherwise only exercised in-process over an in-memory
	// transport, which would not notice if they failed to register in the
	// actual server binary.

	t.Run("prompts_list", func(t *testing.T) {
		resp := client.request("prompts/list", map[string]any{})
		result, _ := resp["result"].(map[string]any)
		raw, _ := result["prompts"].([]any)

		got := map[string]bool{}
		for _, entry := range raw {
			p, _ := entry.(map[string]any)
			if name, _ := p["name"].(string); name != "" {
				got[name] = true
			}
		}
		for _, want := range []string{
			"diagnose_failed_build", "survey_job", "triage_queue",
			"compare_builds", "find_flaky_test", "triage_pipeline_failure",
		} {
			if !got[want] {
				t.Errorf("prompts/list is missing %s; got %v", want, keysOf(got))
			}
		}
	})

	t.Run("resources_list", func(t *testing.T) {
		resp := client.request("resources/list", map[string]any{})
		result, _ := resp["result"].(map[string]any)
		raw, _ := result["resources"].([]any)

		var uris []string
		for _, entry := range raw {
			r, _ := entry.(map[string]any)
			if uri, _ := r["uri"].(string); uri != "" {
				uris = append(uris, uri)
			}
		}
		if len(uris) == 0 || !contains(uris, "jenkins://info") {
			t.Errorf("resources/list should advertise jenkins://info, got %v", uris)
		}

		// Templated resources are listed separately from concrete ones.
		tmplResp := client.request("resources/templates/list", map[string]any{})
		tmplResult, _ := tmplResp["result"].(map[string]any)
		tmplRaw, _ := tmplResult["resourceTemplates"].([]any)

		var templates []string
		for _, entry := range tmplRaw {
			r, _ := entry.(map[string]any)
			if uri, _ := r["uriTemplate"].(string); uri != "" {
				templates = append(templates, uri)
			}
		}
		for _, want := range []string{
			"jenkins://job/{+path}/config.xml",
			"jenkins://build/{+job}/{number}/console",
		} {
			if !contains(templates, want) {
				t.Errorf("resources/templates/list is missing %q, got %v", want, templates)
			}
		}
	})

	t.Run("resource_read_info", func(t *testing.T) {
		text := readResource(t, client, "jenkins://info")
		if !strings.Contains(text, "version") {
			t.Errorf("jenkins://info should report a version: %s", text)
		}
	})

	// Reads the config of the job created earlier, exercising the {+path}
	// reserved expansion against the real server.
	t.Run("resource_read_job_config", func(t *testing.T) {
		text := readResource(t, client, "jenkins://job/"+jobName+"/config.xml")
		if !strings.Contains(text, "<project>") {
			t.Errorf("job config.xml should be XML, got: %s", text)
		}
		if !strings.Contains(text, consoleMarker) {
			t.Errorf("job config.xml should contain the job's shell step: %s", text)
		}
	})

	t.Run("resource_read_build_console", func(t *testing.T) {
		text := readResource(t, client, "jenkins://build/"+jobName+"/1/console")
		if !strings.Contains(text, consoleMarker) {
			t.Errorf("build console resource should contain %q, got: %s", consoleMarker, text)
		}
	})

	t.Run("resource_read_unknown_is_an_error", func(t *testing.T) {
		c := client
		c.nextID++
		id := c.nextID
		c.send(map[string]any{
			"jsonrpc": "2.0", "id": id, "method": "resources/read",
			"params": map[string]any{"uri": "jenkins://job/no-such-job-here/config.xml"},
		})
		resp := c.recvID(id)
		if resp["error"] == nil {
			t.Errorf("reading a nonexistent job's config should be a protocol error, got: %v", resp["result"])
		}
	})
}

// readResource reads a resource and returns its single text content.
func readResource(t *testing.T, c *mcpClient, uri string) string {
	t.Helper()

	resp := c.request("resources/read", map[string]any{"uri": uri})
	result, _ := resp["result"].(map[string]any)
	contents, _ := result["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("resources/read(%s) returned %d contents, want 1: %v", uri, len(contents), result)
	}
	entry, _ := contents[0].(map[string]any)
	text, _ := entry["text"].(string)
	if text == "" {
		t.Fatalf("resources/read(%s) returned empty text: %v", uri, entry)
	}
	return text
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

// waitForBuild polls jenkins_get_build until the build finishes. Right after
// a trigger the build is still queued and lastBuild does not resolve, so
// tool errors are tolerated until the deadline.
func waitForBuild(t *testing.T, c *mcpClient, job string) map[string]any {
	t.Helper()

	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		res := c.callTool("jenkins_get_build", map[string]any{"job": job, "build": "lastBuild"})
		if !res.isError && res.structured != nil {
			building, _ := res.structured["building"].(bool)
			result, _ := res.structured["result"].(string)
			if !building && result != "" {
				return res.structured
			}
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("build of %s did not complete within the deadline", job)
	return nil
}

// readFullConsole consumes a build's console log the way the tool's own
// description (and the diagnose_failed_build prompt) tells a client to:
// start at 0 and follow nextStart while hasMore is true.
//
// This loop is not just for completeness — it removes a real race. A build
// reporting building=false with a result set does NOT mean its log stream
// has closed: Jenkins can still answer X-More-Data: true for a moment
// afterwards, so a single-shot read asserting hasMore==false is flaky
// (observed failing roughly 1 run in 5).
func readFullConsole(t *testing.T, c *mcpClient, job string) (string, float64) {
	t.Helper()

	var sb strings.Builder
	var start float64
	deadline := time.Now().Add(2 * time.Minute)

	for time.Now().Before(deadline) {
		got := c.mustCallTool("jenkins_get_build_console", map[string]any{
			"job": job, "build": "lastBuild", "start": start,
		})
		chunk, _ := got["text"].(string)
		sb.WriteString(chunk)

		next, _ := got["nextStart"].(float64)
		if next < start {
			t.Fatalf("nextStart went backwards: %v then %v", start, next)
		}
		start = next

		if hasMore, _ := got["hasMore"].(bool); !hasMore {
			return sb.String(), start
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("console log for %s never finished streaming", job)
	return "", 0
}

// createJob creates a job straight through the Jenkins REST API, including
// the CSRF crumb handshake, so the server's own tools aren't used to set up
// their own fixtures.
func createJob(t *testing.T, j *jenkinsInstance, name, configXML string) {
	t.Helper()

	client := &http.Client{Timeout: 30 * time.Second}

	crumbField, crumbValue := fetchCrumb(t, client, j)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		j.URL+"/createItem?name="+name, strings.NewReader(configXML))
	if err != nil {
		t.Fatalf("building createItem request: %v", err)
	}
	req.SetBasicAuth(j.User, j.Token)
	req.Header.Set("Content-Type", "application/xml")
	if crumbField != "" {
		req.Header.Set(crumbField, crumbValue)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("createItem: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("createItem returned %s: %s", resp.Status, body)
	}
}

// fetchCrumb gets a CSRF crumb for out-of-band setup requests.
func fetchCrumb(t *testing.T, client *http.Client, j *jenkinsInstance) (field, value string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.URL+"/crumbIssuer/api/json", nil)
	if err != nil {
		t.Fatalf("building crumb request: %v", err)
	}
	req.SetBasicAuth(j.User, j.Token)

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("fetching crumb: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", "" // CSRF protection disabled
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		t.Fatalf("crumbIssuer returned %s: %s", resp.Status, body)
	}

	var crumb struct {
		Crumb             string `json:"crumb"`
		CrumbRequestField string `json:"crumbRequestField"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&crumb); err != nil {
		t.Fatalf("decoding crumb: %v", err)
	}
	if crumb.Crumb == "" || crumb.CrumbRequestField == "" {
		t.Fatalf("crumbIssuer returned an incomplete crumb: %+v", crumb)
	}
	return crumb.CrumbRequestField, crumb.Crumb
}

// keysOf returns m's keys sorted, for a deterministic failure message.
func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// assertCoherentPage checks a paged list result is internally consistent.
//
// It exists because asserting that a "count" key is merely PRESENT proves
// nothing: PageResult.Count has no omitempty, so it appears in every result
// even from a tool hitting the wrong endpoint. count == len(items) is the
// weakest assertion that can actually fail.
func assertCoherentPage(t *testing.T, tool string, structured map[string]any) {
	t.Helper()

	count, ok := structured["count"].(float64)
	if !ok {
		t.Errorf("%s: result has no numeric count: %v", tool, structured)
		return
	}
	list := items(t, tool, structured)
	if int(count) != len(list) {
		t.Errorf("%s: count = %v but returned %d items", tool, count, len(list))
	}
	if count == 0 {
		if hasMore, _ := structured["hasMore"].(bool); hasMore {
			t.Errorf("%s: hasMore is true on an empty page: %v", tool, structured)
		}
	}
}
