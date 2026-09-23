// SPDX-License-Identifier: GPL-3.0-or-later

package tools

import (
	"regexp"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// TestBuildIDPatternAcceptsRealBuildIdentifiers guards the constraint that
// is riskiest to get wrong: an MCP client validates against this pattern
// BEFORE the call reaches the server, so anything Jenkins would actually
// accept must match, or legitimate calls are rejected at the client.
//
// Every permalink below was verified to resolve as a path segment against
// Jenkins 2.568.3.
func TestBuildIDPatternAcceptsRealBuildIdentifiers(t *testing.T) {
	re := regexp.MustCompile(buildIDPattern)

	valid := []string{
		"1", "42", "1000",
		"lastBuild", "lastSuccessfulBuild", "lastFailedBuild", "lastStableBuild",
		"lastCompletedBuild", "lastUnsuccessfulBuild", "lastUnstableBuild", "firstBuild",
	}
	for _, v := range valid {
		if !re.MatchString(v) {
			t.Errorf("buildIDPattern rejects %q, which real Jenkins accepts", v)
		}
	}

	invalid := []string{"", "  ", "../etc", "lastBuild/..", "notABuild", "-1", "1.5"}
	for _, v := range invalid {
		if re.MatchString(v) {
			t.Errorf("buildIDPattern accepts %q, which is not a valid build identifier", v)
		}
	}
}

func TestRefinePagingBounds(t *testing.T) {
	s := &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{
		"limit":  {},
		"offset": {},
	}}
	refinePaging(s)

	limit := s.Properties["limit"]
	if limit.Minimum == nil || *limit.Minimum != 1 {
		t.Errorf("limit.Minimum = %v, want 1", limit.Minimum)
	}
	if limit.Maximum == nil || *limit.Maximum != float64(maxPageLimit) {
		t.Errorf("limit.Maximum = %v, want %d", limit.Maximum, maxPageLimit)
	}
	offset := s.Properties["offset"]
	if offset.Minimum == nil || *offset.Minimum != 0 {
		t.Errorf("offset.Minimum = %v, want 0", offset.Minimum)
	}
}

// TestInputSchemasCarryConstraints walks each refining input type and
// checks its RefineSchema actually sets what it promises. A new input type
// that declares constraints but forgets to wire them up fails here.
func TestInputSchemasCarryConstraints(t *testing.T) {
	cases := []struct {
		name   string
		refine func(*jsonschema.Schema)
		props  []string
		check  func(*testing.T, *jsonschema.Schema)
	}{
		{
			name:   "GetBuildInput",
			refine: GetBuildInput{}.RefineSchema,
			props:  []string{"job", "build"},
			check: func(t *testing.T, s *jsonschema.Schema) {
				if s.Properties["build"].Pattern != buildIDPattern {
					t.Errorf("build.Pattern = %q, want the build ID pattern", s.Properties["build"].Pattern)
				}
				if s.Properties["job"].MinLength == nil {
					t.Error("job.MinLength is nil, want 1")
				}
			},
		},
		{
			name:   "CancelQueueItemInput",
			refine: CancelQueueItemInput{}.RefineSchema,
			props:  []string{"id"},
			check: func(t *testing.T, s *jsonschema.Schema) {
				if m := s.Properties["id"].Minimum; m == nil || *m != 1 {
					t.Errorf("id.Minimum = %v, want 1 (the handler rejects id<=0)", m)
				}
			},
		},
		{
			name:   "GetBuildConsoleInput",
			refine: GetBuildConsoleInput{}.RefineSchema,
			props:  []string{"job", "build", "start", "maxBytes"},
			check: func(t *testing.T, s *jsonschema.Schema) {
				if m := s.Properties["start"].Minimum; m == nil || *m != 0 {
					t.Errorf("start.Minimum = %v, want 0", m)
				}
				if m := s.Properties["maxBytes"].Maximum; m == nil || *m != float64(maxTextBytes) {
					t.Errorf("maxBytes.Maximum = %v, want %d", m, maxTextBytes)
				}
			},
		},
		{
			name:   "ListJobsInput",
			refine: ListJobsInput{}.RefineSchema,
			props:  []string{"limit", "offset"},
			check: func(t *testing.T, s *jsonschema.Schema) {
				if s.Properties["limit"].Maximum == nil {
					t.Error("limit.Maximum is nil, want it bounded")
				}
			},
		},
		{
			name:   "GetNodeInput",
			refine: GetNodeInput{}.RefineSchema,
			props:  []string{"name"},
			check: func(t *testing.T, s *jsonschema.Schema) {
				if s.Properties["name"].MinLength == nil {
					t.Error("name.MinLength is nil, want 1")
				}
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{}}
			for _, p := range c.props {
				s.Properties[p] = &jsonschema.Schema{}
			}
			c.refine(s)
			c.check(t, s)
		})
	}
}

// TestBuildDetailResultEnum checks the output enum lists exactly the values
// Jenkins reports, including "" for a build still in progress.
func TestBuildDetailResultEnum(t *testing.T) {
	s := &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{"result": {}}}
	BuildDetail{}.RefineSchema(s)

	got := s.Properties["result"].Enum
	if len(got) != len(buildResultEnum) {
		t.Fatalf("result.Enum = %v, want %v", got, buildResultEnum)
	}
	seen := map[any]bool{}
	for _, v := range got {
		seen[v] = true
	}
	for _, want := range []any{"SUCCESS", "FAILURE", "UNSTABLE", "ABORTED", "NOT_BUILT", ""} {
		if !seen[want] {
			t.Errorf("result.Enum is missing %q", want)
		}
	}
}

// TestRefineSchemaToleratesMissingProperties ensures a refiner never panics
// when a property it names isn't present — inference decides the property
// set, and a renamed JSON tag must not take the server down.
func TestRefineSchemaToleratesMissingProperties(t *testing.T) {
	empty := func() *jsonschema.Schema {
		return &jsonschema.Schema{Properties: map[string]*jsonschema.Schema{}}
	}
	refiners := []func(*jsonschema.Schema){
		GetBuildInput{}.RefineSchema,
		GetBuildConsoleInput{}.RefineSchema,
		ListJobsInput{}.RefineSchema,
		ListQueueInput{}.RefineSchema,
		ListNodesInput{}.RefineSchema,
		ListViewsInput{}.RefineSchema,
		ListPluginsInput{}.RefineSchema,
		ListArtifactsInput{}.RefineSchema,
		GetArtifactInput{}.RefineSchema,
		GetTestResultsInput{}.RefineSchema,
		StopBuildInput{}.RefineSchema,
		GetJobInput{}.RefineSchema,
		GetJobConfigInput{}.RefineSchema,
		GetNodeInput{}.RefineSchema,
		GetViewInput{}.RefineSchema,
		TriggerBuildInput{}.RefineSchema,
		CancelQueueItemInput{}.RefineSchema,
		BuildDetail{}.RefineSchema,
	}
	for i, r := range refiners {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Errorf("refiner %d panicked on an empty schema: %v", i, p)
				}
			}()
			r(empty())
		}()
	}
}
