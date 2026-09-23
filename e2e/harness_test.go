// SPDX-License-Identifier: GPL-3.0-or-later

//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	// jenkinsImage is pinned to the LTS line. The e2e suite pulls this if it
	// isn't cached locally, which dominates first-run time (~500MB).
	jenkinsImage = "jenkins/jenkins:lts"

	// adminUser/adminPassword are provisioned by initGroovy below. The
	// password is only ever used inside the throwaway container; the tests
	// authenticate with the generated API token, not this password, so the
	// real token path is what gets exercised.
	adminUser     = "e2e-admin"
	adminPassword = "e2e-password"

	// tokenFile is where initGroovy writes the generated API token, read
	// back out with `docker exec cat`.
	tokenFile = "/var/jenkins_home/e2e-api-token.txt"

	// startupTimeout bounds how long we wait for Jenkins to serve its login
	// page. A cold LTS boot is typically 45-90s; this leaves generous slack
	// for a loaded CI machine.
	startupTimeout = 5 * time.Minute
)

// initGroovy runs during Jenkins startup (the official image copies
// /usr/share/jenkins/ref/init.groovy.d into JENKINS_HOME). It creates a real
// security realm with one admin user, generates an API token for that user,
// and writes the token where the test can read it. Provisioning a genuine
// token (rather than using HTTP Basic with the password) means the e2e run
// exercises the same credential shape the server documents.
//
// It also pins the built-in node to 2 executors so triggered builds actually
// have somewhere to run instead of queueing forever.
const initGroovy = `
import jenkins.model.Jenkins
import hudson.security.HudsonPrivateSecurityRealm
import hudson.security.FullControlOnceLoggedInAuthorizationStrategy
import jenkins.security.ApiTokenProperty
import hudson.model.User

def instance = Jenkins.get()

def realm = new HudsonPrivateSecurityRealm(false)
realm.createAccount("` + adminUser + `", "` + adminPassword + `")
instance.setSecurityRealm(realm)

def strategy = new FullControlOnceLoggedInAuthorizationStrategy()
strategy.setAllowAnonymousRead(false)
instance.setAuthorizationStrategy(strategy)

instance.setNumExecutors(2)
instance.save()

def user = User.getById("` + adminUser + `", false)
def tokenProperty = user.getProperty(ApiTokenProperty.class)
def generated = tokenProperty.tokenStore.generateNewToken("e2e")
user.save()

new File("` + tokenFile + `").text = generated.plainValue
`

// jenkinsInstance describes a running Jenkins container the tests can talk to.
type jenkinsInstance struct {
	URL       string
	User      string
	Token     string
	container string
}

// env returns the environment for the jenkins MCP subprocess: every inherited
// JENKINS_* variable is stripped so a developer's real Jenkins configuration
// can never leak into (or be mutated by) an e2e run.
func (j *jenkinsInstance) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "JENKINS_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"JENKINS_URL="+j.URL,
		"JENKINS_USER="+j.User,
		"JENKINS_TOKEN="+j.Token,
		// Deliberately NOT read-only: the whole point of the e2e run is to
		// exercise the mutating tools (trigger build) against real Jenkins,
		// including the CSRF crumb handshake they need.
		"JENKINS_READONLY=false",
	)
}

// startJenkins boots a throwaway Jenkins container, waits for it to serve,
// and returns its URL and credentials. The container is removed via
// t.Cleanup, which runs even when a test fails or panics.
func startJenkins(t *testing.T) *jenkinsInstance {
	t.Helper()

	if _, err := exec.LookPath("docker"); err != nil {
		t.Skipf("docker not found in PATH: %v", err)
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		t.Skipf("docker daemon not reachable: %v\n%s", err, out)
	}

	name := fmt.Sprintf("jenkins-mcp-e2e-%d", time.Now().UnixNano())

	// The container runs as uid 1000 and reads this mount; the host user may
	// be a different uid, so the directory and file must be world-readable
	// rather than relying on ownership. t.TempDir is 0700, hence MkdirTemp
	// plus an explicit chmod.
	base, err := os.MkdirTemp("", "jenkins-mcp-e2e-init-")
	if err != nil {
		t.Fatalf("creating init script dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })

	initDir := filepath.Join(base, "init.groovy.d")
	if err := os.MkdirAll(initDir, 0o755); err != nil {
		t.Fatalf("creating init.groovy.d: %v", err)
	}
	for _, dir := range []string{base, initDir} {
		if err := os.Chmod(dir, 0o755); err != nil {
			t.Fatalf("chmod %s: %v", dir, err)
		}
	}
	script := filepath.Join(initDir, "e2e-security.groovy")
	if err := os.WriteFile(script, []byte(initGroovy), 0o644); err != nil {
		t.Fatalf("writing init groovy: %v", err)
	}

	runArgs := []string{
		"run", "-d",
		"--name", name,
		// Port 0 lets Docker pick a free host port; hardcoding 8080 would
		// collide with anything already listening there.
		"-p", "127.0.0.1:0:8080",
		"-e", "JAVA_OPTS=-Djenkins.install.runSetupWizard=false",
		"-v", initDir + ":/usr/share/jenkins/ref/init.groovy.d:ro",
		jenkinsImage,
	}
	if out, err := exec.Command("docker", runArgs...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}

	inst := &jenkinsInstance{User: adminUser, container: name}

	t.Cleanup(func() {
		if t.Failed() {
			// Container logs are the only way to diagnose a provisioning
			// failure after the fact, so surface them on failure.
			if out, err := exec.Command("docker", "logs", "--tail", "60", name).CombinedOutput(); err == nil {
				t.Logf("jenkins container logs (last 60 lines):\n%s", out)
			}
		}
		if out, err := exec.Command("docker", "rm", "-f", name).CombinedOutput(); err != nil {
			t.Logf("removing container %s: %v\n%s", name, err, out)
		}
	})

	port, err := hostPort(name)
	if err != nil {
		t.Fatalf("resolving mapped port: %v", err)
	}
	inst.URL = "http://127.0.0.1:" + port

	waitForJenkins(t, inst.URL)
	inst.Token = readToken(t, name)

	return inst
}

// hostPort asks Docker which host port it mapped the container's 8080 to.
func hostPort(container string) (string, error) {
	out, err := exec.Command("docker", "port", container, "8080/tcp").Output()
	if err != nil {
		return "", fmt.Errorf("docker port: %w", err)
	}
	// Output looks like "127.0.0.1:49154" (possibly several lines).
	line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	idx := strings.LastIndex(line, ":")
	if idx < 0 {
		return "", fmt.Errorf("unexpected docker port output %q", line)
	}
	return line[idx+1:], nil
}

// waitForJenkins polls the login page until Jenkins finishes booting. During
// startup Jenkins answers with 503 and a "please wait" page, so a connection
// succeeding is not enough — we wait for a real 200.
func waitForJenkins(t *testing.T, baseURL string) {
	t.Helper()

	deadline := time.Now().Add(startupTimeout)
	client := &http.Client{Timeout: 10 * time.Second}
	var lastErr error
	var lastStatus int

	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/login", nil)
		if err != nil {
			cancel()
			t.Fatalf("building readiness request: %v", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				cancel()
				return
			}
		} else {
			lastErr = err
		}
		cancel()
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("jenkins did not become ready within %s (last status %d, last error %v)",
		startupTimeout, lastStatus, lastErr)
}

// readToken polls for the API token the init script writes. The script runs
// during startup, so by the time the login page serves it has normally
// finished — but it's a separate side effect, so it gets its own wait rather
// than being assumed.
func readToken(t *testing.T, container string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Minute)
	var lastOut []byte
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "exec", container, "cat", tokenFile).CombinedOutput()
		if err == nil {
			if token := strings.TrimSpace(string(out)); token != "" {
				return token
			}
		}
		lastOut = out
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("API token file %s never appeared in %s (last output: %s)", tokenFile, container, lastOut)
	return ""
}

// buildBinary compiles the real jenkins binary the tests drive over stdio.
func buildBinary(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "jenkins-e2e-bin")
	build := exec.Command("go", "build", "-o", bin, "../cmd/jenkins")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build ../cmd/jenkins: %v\n%s", err, out)
	}
	return bin
}
