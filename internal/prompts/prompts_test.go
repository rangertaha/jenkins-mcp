// SPDX-License-Identifier: GPL-3.0-or-later

package prompts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rangertaha/jenkins-mcp/internal/server"
)

func TestRegisterAddsBothPrompts(t *testing.T) {
	s := server.New("test", "0.0.0", false)
	Register(s)

	if s.PromptCount() != 2 {
		t.Fatalf("PromptCount() = %d, want 2", s.PromptCount())
	}
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
