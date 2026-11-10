package forge

import (
	"strings"
	"testing"
)

func TestCompileRouteBindsTheTarget(t *testing.T) {
	for _, tc := range []struct{ pattern, path, target string }{
		{"/pulls/{n}", "/pulls/42", "42"},
		{"/branches/{b}/protection", "/branches/release%2Fv1/protection", "release%2Fv1"},
		{"/pipelines/{uuid}/stopPipeline", "/pipelines/%7B1f4a2c3e-0000-4000-8000-0123456789ab%7D/stopPipeline", "%7B1f4a2c3e-0000-4000-8000-0123456789ab%7D"},
		{"/pipelines/{uuid}/steps/{step}/log", "/pipelines/%7B1f4a2c3e-0000-4000-8000-0123456789ab%7D/steps/%7Bffffffff-1111-4222-8333-444444444444%7D/log", "%7B1f4a2c3e-0000-4000-8000-0123456789ab%7D"},
	} {
		m := compileRoute(tc.pattern).FindStringSubmatch(tc.path)
		if m == nil {
			t.Fatalf("%s does not match %s", tc.pattern, tc.path)
		}
		if got := m[compileRoute(tc.pattern).SubexpIndex("target")]; got != tc.target {
			t.Fatalf("%s on %s: target = %q, want %q", tc.pattern, tc.path, got, tc.target)
		}
	}
	for _, tc := range []struct{ pattern, path string }{
		{"/pulls/{n}", "/pulls/42/merge"},
		{"/pipelines/{uuid}", "/pipelines/1f4a2c3e-0000-4000-8000-0123456789ab"},
		{"/pipelines/{uuid}", "/pipelines/%7Bnot-a-uuid%7D"},
		{"/branches/{b}/protection", "/branches/release/v1/protection"},
	} {
		if compileRoute(tc.pattern).MatchString(tc.path) {
			t.Fatalf("%s matches %s", tc.pattern, tc.path)
		}
	}
}

func TestCheckFieldsFollowsDottedPaths(t *testing.T) {
	allowed := []string{"title", "source.branch.name", "destination.branch.name", "draft"}
	for _, body := range []string{
		`{"title":"t","source":{"branch":{"name":"feat"}}}`,
		`{"title":"t","source":{"branch":{"name":"feat"}},"destination":{"branch":{"name":"main"}},"draft":true}`,
		``,
	} {
		if msg := checkFields([]byte(body), allowed); msg != "" {
			t.Fatalf("%s refused: %s", body, msg)
		}
	}
	for body, want := range map[string]string{
		`{"title":"t","source":{"branch":{"name":"feat"},"repository":{"full_name":"other/fork"}}}`: `"source.repository" is not accepted`,
		`{"title":"t","source":{"branch":{"name":"feat","target":"x"}}}`:                            `"source.branch.target" is not accepted`,
		`{"title":"t","source":"feat"}`:                                                             `"source" must be an object with: source.branch.name`,
		`{"reviewers":[]}`:                                                                          `"reviewers" is not accepted`,
	} {
		msg := checkFields([]byte(body), allowed)
		if !strings.Contains(msg, want) {
			t.Fatalf("%s: message %q, want %q", body, msg, want)
		}
	}
	if msg := checkFields([]byte(`{"x":1}`), nil); !strings.Contains(msg, "does not take a request body") {
		t.Fatalf("body on a bodiless route: %q", msg)
	}
}
