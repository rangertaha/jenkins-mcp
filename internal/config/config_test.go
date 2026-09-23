// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestSplitList(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"whitespace only", "   ", nil},
		{"single", "jobs", []string{"jobs"}},
		{"multiple", "jobs,builds,nodes", []string{"jobs", "builds", "nodes"}},
		{"whitespace around entries", " jobs , builds ,  nodes ", []string{"jobs", "builds", "nodes"}},
		{"mixed case lower-cased", "Jobs,Builds,NODES", []string{"jobs", "builds", "nodes"}},
		{"empty entries dropped", "jobs,,builds,", []string{"jobs", "builds"}},
		{"garbage only commas", ",,,", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitList(c.in)
			if c.want == nil {
				if got != nil {
					t.Errorf("splitList(%q) = %#v, want nil", c.in, got)
				}
				return
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("splitList(%q) = %#v, want %#v", c.in, got, c.want)
			}
		})
	}
}

// FuzzSplitList checks splitList never panics on arbitrary input and that
// its documented invariants always hold: every returned entry is non-empty,
// trimmed, and lower-cased.
func FuzzSplitList(f *testing.F) {
	for _, seed := range []string{
		"", ",", ",,,", "jobs,builds", " jobs , builds ", "JOBS,BUILDS", "jobs,,builds,",
		"\t\n", "jobs\x00builds", "😀,jobs", strings.Repeat("a,", 1000),
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := splitList(in)
		for _, entry := range got {
			if entry == "" {
				t.Fatalf("splitList(%q) contains an empty entry: %#v", in, got)
			}
			if entry != strings.TrimSpace(entry) {
				t.Fatalf("splitList(%q) contains an untrimmed entry %q: %#v", in, entry, got)
			}
			if entry != strings.ToLower(entry) {
				t.Fatalf("splitList(%q) contains a non-lower-cased entry %q: %#v", in, entry, got)
			}
		}
	})
}

// FuzzIsTruthy checks isTruthy never panics and stays consistent regardless
// of surrounding whitespace or case, since its doc comment promises both are
// ignored.
func FuzzIsTruthy(f *testing.F) {
	for _, seed := range []string{
		"", "true", "TRUE", " true ", "1", "0", "false", "yes", "no", "😀", "\x00",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := isTruthy(in)
		if wrapped := isTruthy("  " + in + "  "); wrapped != got {
			t.Errorf("isTruthy(%q)=%v but isTruthy(%q)=%v: whitespace should be ignored", in, got, "  "+in+"  ", wrapped)
		}
		if upper := isTruthy(strings.ToUpper(in)); upper != got {
			t.Errorf("isTruthy(%q)=%v but isTruthy(%q)=%v: case should be ignored", in, got, strings.ToUpper(in), upper)
		}
	})
}

func TestIsTruthy(t *testing.T) {
	truthy := []string{"1", "true", "True", "TRUE", "yes", "Yes", "on", "On", " true ", "\ttrue\n"}
	for _, v := range truthy {
		if !isTruthy(v) {
			t.Errorf("isTruthy(%q) = false, want true", v)
		}
	}
	falsy := []string{"", "0", "false", "False", "no", "off", "2", "yesplease", " ", "null"}
	for _, v := range falsy {
		if isTruthy(v) {
			t.Errorf("isTruthy(%q) = true, want false", v)
		}
	}
}

func TestConfigAllToolsets(t *testing.T) {
	cases := []struct {
		name     string
		toolsets []string
		want     bool
	}{
		{"nil", nil, true},
		{"empty slice", []string{}, true},
		{"specific toolsets", []string{"jobs", "builds"}, false},
		{"explicit all", []string{"all"}, true},
		{"all mixed with others", []string{"jobs", "all"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := &Config{Toolsets: c.toolsets}
			if got := cfg.AllToolsets(); got != c.want {
				t.Errorf("AllToolsets() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestConfigToolsetEnabled(t *testing.T) {
	all := &Config{}
	if !all.ToolsetEnabled("jobs") {
		t.Error("empty Toolsets should enable every toolset")
	}

	specific := &Config{Toolsets: []string{"jobs", "builds"}}
	if !specific.ToolsetEnabled("jobs") {
		t.Error("jobs should be enabled")
	}
	if !specific.ToolsetEnabled("Jobs") {
		t.Error("ToolsetEnabled should be case-insensitive")
	}
	if specific.ToolsetEnabled("nodes") {
		t.Error("nodes should not be enabled")
	}
}

func TestLoad(t *testing.T) {
	t.Setenv(EnvURL, "  https://ci.example.com/  ")
	t.Setenv(EnvUser, "  alice  ")
	t.Setenv(EnvToken, "  tok_123  ")
	t.Setenv(EnvToolsets, "Jobs, Builds")
	t.Setenv(EnvReadOnly, "true")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.URL != "https://ci.example.com" {
		t.Errorf("URL = %q, want trimmed %q (no trailing slash)", cfg.URL, "https://ci.example.com")
	}
	if cfg.User != "alice" {
		t.Errorf("User = %q, want %q", cfg.User, "alice")
	}
	if cfg.Token != "tok_123" {
		t.Errorf("Token = %q, want %q", cfg.Token, "tok_123")
	}
	if !reflect.DeepEqual(cfg.Toolsets, []string{"jobs", "builds"}) {
		t.Errorf("Toolsets = %#v, want [jobs builds]", cfg.Toolsets)
	}
	if !cfg.ReadOnly {
		t.Error("ReadOnly = false, want true")
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Setenv(EnvURL, "https://ci.example.com")
	t.Setenv(EnvUser, "alice")
	t.Setenv(EnvToken, "tok_123")
	t.Setenv(EnvToolsets, "")
	t.Setenv(EnvReadOnly, "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !cfg.AllToolsets() {
		t.Error("unset JENKINS_TOOLSETS should mean all toolsets")
	}
	if cfg.ReadOnly {
		t.Error("unset JENKINS_READONLY should mean ReadOnly=false")
	}
}

func TestLoadMissingRequiredFields(t *testing.T) {
	t.Setenv(EnvURL, "")
	t.Setenv(EnvUser, "")
	t.Setenv(EnvToken, "")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want an error naming the missing required variables")
	}
	for _, want := range []string{EnvURL, EnvUser, EnvToken} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Load() error = %q, want it to mention %q", err.Error(), want)
		}
	}
}

func TestLoadPartiallyMissing(t *testing.T) {
	t.Setenv(EnvURL, "https://ci.example.com")
	t.Setenv(EnvUser, "")
	t.Setenv(EnvToken, "tok_123")

	_, err := Load()
	if err == nil {
		t.Fatal("Load() error = nil, want an error naming JENKINS_USER")
	}
	if !strings.Contains(err.Error(), EnvUser) {
		t.Errorf("Load() error = %q, want it to mention %q", err.Error(), EnvUser)
	}
	if strings.Contains(err.Error(), EnvURL) || strings.Contains(err.Error(), EnvToken) {
		t.Errorf("Load() error = %q, should not mention variables that were set", err.Error())
	}
}
