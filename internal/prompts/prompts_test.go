// SPDX-License-Identifier: GPL-3.0-or-later

package prompts

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx"
	"github.com/rangertaha/jenkins-mcp/internal/jenkinsx/tools"
	"github.com/rangertaha/jenkins-mcp/internal/server"
)

// allPrompts is every prompt Register adds, with arguments that exercise
// its rendering. Adding a prompt means adding a line here, which is what
// keeps the render and tool-reference tests below covering all of them.
var allPrompts = []struct {
	name string
	args map[string]string
}{
	{"diagnose_failed_build", map[string]string{"job": "team-a/service-b", "build": "42"}},
	{"survey_job", map[string]string{"job": "team-a/service-b"}},
	{"triage_queue", nil},
	{"compare_builds", map[string]string{"job": "team-a/service-b", "good": "41", "bad": "42"}},
	{"find_flaky_test", map[string]string{"job": "team-a/service-b", "builds": "5"}},
	{"triage_pipeline_failure", map[string]string{"job": "team-a/service-b", "build": "42"}},
}

func TestRegisterAddsAllPrompts(t *testing.T) {
	s := server.New("test", "0.0.0", false)
	Register(s)

	if got := s.PromptCount(); got != len(allPrompts) {
		t.Fatalf("PromptCount() = %d, want %d", got, len(allPrompts))
	}
}

// TestPromptsRenderWithoutFormatErrors catches the classic fmt.Sprintf
// mistake in a prompt body — too few or too many arguments — which produces
// a "%!s(MISSING)" or "%!(EXTRA ...)" in text handed straight to the model.
func TestPromptsRenderWithoutFormatErrors(t *testing.T) {
	for _, p := range allPrompts {
		t.Run(p.name, func(t *testing.T) {
			s := server.New("test", "0.0.0", false)
			Register(s)

			text := getPromptText(t, s, p.name, p.args)
			if text == "" {
				t.Fatal("rendered prompt is empty")
			}
			for _, bad := range []string{"%!s", "%!(", "(MISSING)", "(EXTRA"} {
				if strings.Contains(text, bad) {
					t.Errorf("rendered prompt contains %q, a fmt error: %s", bad, text)
				}
			}
		})
	}
}

// TestPromptsOnlyReferenceRealTools is a class test: a prompt is just text,
// so naming a tool that doesn't exist (or mistyping one, or keeping a name
// after a rename) fails silently — the model is told to call something and
// gets an unknown-tool error at runtime. This pins every jenkins_* name
// appearing in any prompt against the tools actually registered.
func TestPromptsOnlyReferenceRealTools(t *testing.T) {
	registered := registeredToolNames(t)

	for _, p := range allPrompts {
		t.Run(p.name, func(t *testing.T) {
			s := server.New("test", "0.0.0", false)
			Register(s)

			text := getPromptText(t, s, p.name, p.args)
			for _, name := range toolMentions(text) {
				if !registered[name] {
					t.Errorf("prompt %q references tool %q, which is not registered; "+
						"registered tools: %v", p.name, name, sortedKeys(registered))
				}
			}
		})
	}
}

// toolNamePattern matches a jenkins_* tool name mentioned in prompt text.
var toolNamePattern = regexp.MustCompile(`jenkins_[a-z_]+`)

// toolMentions returns the distinct jenkins_* tool names named in text.
func toolMentions(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range toolNamePattern.FindAllString(text, -1) {
		name := strings.TrimRight(m, "_")
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// registeredToolNames returns every tool name the server actually exposes.
// The client is pointed at an unroutable URL: registration only builds
// handlers, it never calls Jenkins, so no server is needed.
func registeredToolNames(t *testing.T) map[string]bool {
	t.Helper()

	client, err := jenkinsx.NewClient("http://127.0.0.1:1", "u", "tok", nil)
	if err != nil {
		t.Fatalf("jenkinsx.NewClient: %v", err)
	}

	s := server.New("test", "0.0.0", false)
	tools.RegisterJobs(s, client)
	tools.RegisterBuilds(s, client)
	tools.RegisterQueue(s, client)
	tools.RegisterNodes(s, client)
	tools.RegisterViews(s, client)
	tools.RegisterSystem(s, client)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()
	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- s.Run(ctx, serverTransport) }()

	mc := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := mc.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}

	cancel()
	<-serverErrCh
	return names
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func getPromptText(t *testing.T, s *server.Server, name string, args map[string]string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverErrCh := make(chan error, 1)
	go func() { serverErrCh <- s.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.GetPrompt(ctx, &mcp.GetPromptParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("GetPrompt(%s): %v", name, err)
	}
	if len(res.Messages) != 1 {
		t.Fatalf("GetPrompt(%s) returned %d messages, want 1", name, len(res.Messages))
	}
	text, ok := res.Messages[0].Content.(*mcp.TextContent)
	if !ok {
		t.Fatalf("prompt message content type = %T, want *mcp.TextContent", res.Messages[0].Content)
	}

	cancel()
	<-serverErrCh
	return text.Text
}

func TestDiagnoseFailedBuildRenderSubstitutesArgs(t *testing.T) {
	s := server.New("test", "0.0.0", false)
	Register(s)

	text := getPromptText(t, s, "diagnose_failed_build", map[string]string{"job": "team-a/service-b", "build": "42"})

	if !strings.Contains(text, "team-a/service-b") {
		t.Errorf("rendered prompt should mention the job name: %s", text)
	}
	if !strings.Contains(text, "42") {
		t.Errorf("rendered prompt should mention the build number: %s", text)
	}
	if strings.Contains(text, "%s") || strings.Contains(text, "%!s") {
		t.Errorf("rendered prompt still contains an unsubstituted format verb: %s", text)
	}
}

func TestDiagnoseFailedBuildDefaultsBuildToLastFailed(t *testing.T) {
	s := server.New("test", "0.0.0", false)
	Register(s)

	text := getPromptText(t, s, "diagnose_failed_build", map[string]string{"job": "team-a/service-b"})

	if !strings.Contains(text, "lastFailedBuild") {
		t.Errorf("rendered prompt should default build to lastFailedBuild: %s", text)
	}
}

func TestSurveyJobRenderSubstitutesJobName(t *testing.T) {
	s := server.New("test", "0.0.0", false)
	Register(s)

	text := getPromptText(t, s, "survey_job", map[string]string{"job": "team-a/service-b"})

	if !strings.Contains(text, "team-a/service-b") {
		t.Errorf("rendered prompt should mention the job name: %s", text)
	}
	if strings.Contains(text, "%s") || strings.Contains(text, "%!s") {
		t.Errorf("rendered prompt still contains an unsubstituted format verb: %s", text)
	}
}

func TestCompareBuildsDefaultsToLastSuccessfulAndFailed(t *testing.T) {
	s := server.New("test", "0.0.0", false)
	Register(s)

	text := getPromptText(t, s, "compare_builds", map[string]string{"job": "team-a/service-b"})

	for _, want := range []string{"lastSuccessfulBuild", "lastFailedBuild"} {
		if !strings.Contains(text, want) {
			t.Errorf("rendered prompt should default to %s: %s", want, text)
		}
	}
}

func TestFindFlakyTestDefaultsBuildCount(t *testing.T) {
	s := server.New("test", "0.0.0", false)
	Register(s)

	text := getPromptText(t, s, "find_flaky_test", map[string]string{"job": "team-a/service-b"})

	if !strings.Contains(text, "10") {
		t.Errorf("rendered prompt should default to 10 builds: %s", text)
	}
}

func TestTriageQueueTakesNoArguments(t *testing.T) {
	s := server.New("test", "0.0.0", false)
	Register(s)

	// Renders from a nil argument map: a prompt with no arguments must not
	// depend on any key being present.
	text := getPromptText(t, s, "triage_queue", nil)

	if !strings.Contains(text, "jenkins_list_queue") || !strings.Contains(text, "jenkins_list_nodes") {
		t.Errorf("triage_queue should correlate the queue with node availability: %s", text)
	}
}
