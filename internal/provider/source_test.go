package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/catalog"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func src(repo, path, commit string) *sourceModel {
	s := &sourceModel{Repo: types.StringNull(), Path: types.StringNull(), Commit: types.StringNull()}
	if repo != "" {
		s.Repo = types.StringValue(repo)
	}
	if path != "" {
		s.Path = types.StringValue(path)
	}
	if commit != "" {
		s.Commit = types.StringValue(commit)
	}
	return s
}

func fields(s *catalog.Source) [3]string {
	if s == nil {
		return [3]string{"<nil>"}
	}
	return [3]string{deref(s.Repo), deref(s.Path), deref(s.Commit)}
}

func TestProviderConfigWinsOverTheEnvironmentPerField(t *testing.T) {
	ci := env(map[string]string{"GITHUB_REPOSITORY": "acme/ci", "GITHUB_SHA": "cafe"})
	got := resolveProviderSource(src("https://example.com/acme/stated", "", ""), ci)
	if want := [3]string{"https://example.com/acme/stated", "", "cafe"}; fields(got) != want {
		t.Errorf("got %v, want %v: the stated repo wins, the commit falls back", fields(got), want)
	}
}

func TestEnvironmentFallbacks(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		want [3]string
	}{
		{"GitHub with its server URL", map[string]string{"GITHUB_SERVER_URL": "https://ghe.acme.io/",
			"GITHUB_REPOSITORY": "acme/platform", "GITHUB_SHA": "abc123"},
			[3]string{"https://ghe.acme.io/acme/platform", "", "abc123"}},
		{"GitHub without one", map[string]string{"GITHUB_REPOSITORY": "acme/platform", "GITHUB_SHA": "abc123"},
			[3]string{"https://github.com/acme/platform", "", "abc123"}},
		{"GitLab", map[string]string{"CI_PROJECT_URL": "https://gitlab.com/acme/platform", "CI_COMMIT_SHA": "def456"},
			[3]string{"https://gitlab.com/acme/platform", "", "def456"}},
		{"GitHub is read before GitLab", map[string]string{"GITHUB_REPOSITORY": "acme/gh", "GITHUB_SHA": "1",
			"CI_PROJECT_URL": "https://gitlab.com/acme/gl", "CI_COMMIT_SHA": "2"},
			[3]string{"https://github.com/acme/gh", "", "1"}},
	}
	for _, c := range cases {
		if got := fields(resolveProviderSource(nil, env(c.vars))); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
	if got := resolveProviderSource(nil, env(nil)); got != nil {
		t.Errorf("nothing set must mean no source, got %v", fields(got))
	}
}

func TestAResourceOverridesTheProviderPerField(t *testing.T) {
	provider := resolveProviderSource(nil, env(map[string]string{"GITHUB_REPOSITORY": "acme/platform", "GITHUB_SHA": "abc123"}))
	got := sourceFor(provider, src("", "terraform/boards.tf", ""))
	if want := [3]string{"https://github.com/acme/platform", "terraform/boards.tf", "abc123"}; fields(got) != want {
		t.Errorf("got %v, want %v", fields(got), want)
	}
	if got := sourceFor(provider, src("https://example.com/other", "", "")); fields(got)[0] != "https://example.com/other" {
		t.Errorf("a resource repo must win, got %v", fields(got))
	}
	if got := sourceFor(provider, nil); fields(got) != fields(provider) {
		t.Errorf("no resource source means the provider's, got %v", fields(got))
	}
	if got := sourceFor(nil, nil); got != nil {
		t.Errorf("nothing anywhere means no source, got %v", fields(got))
	}
}

func TestEmptyStringsAreUnsetNotSent(t *testing.T) {
	blank := &sourceModel{Repo: types.StringValue(""), Path: types.StringValue("  "), Commit: types.StringUnknown()}
	if got := resolveProviderSource(blank, env(nil)); got != nil {
		t.Errorf("blank and unknown values must not be recorded, got %v", fields(got))
	}
	got := sourceFor(nil, &sourceModel{Repo: types.StringValue(""), Path: types.StringValue("main.tf"), Commit: types.StringNull()})
	if got == nil || got.Repo != nil || got.Commit != nil || deref(got.Path) != "main.tf" {
		t.Errorf("only the set field is sent, got %v", fields(got))
	}
	if got := resolveProviderSource(nil, env(map[string]string{"GITHUB_SHA": " ", "GITHUB_REPOSITORY": ""})); got != nil {
		t.Errorf("blank environment values are unset too, got %v", fields(got))
	}
}

// Every apply call site sends the merged source: the four resources' create/update path and the
// preset's delete.
func TestEveryApplySendsTheMergedSource(t *testing.T) {
	var sent []*catalog.Source
	applySpec = func(_ context.Context, _ *rearm.Client, _ any, _ bool, source *catalog.Source) (*catalog.Result, error) {
		sent = append(sent, source)
		return &catalog.Result{}, nil
	}
	defer func() { applySpec = catalog.Apply }()

	provider := resolveProviderSource(nil, env(map[string]string{"GITHUB_REPOSITORY": "acme/platform", "GITHUB_SHA": "abc123"}))
	want := [3]string{"https://github.com/acme/platform", "terraform/main.tf", "abc123"}
	here := src("", "terraform/main.tf", "")
	ctx := context.Background()

	board := boardModel()
	board.Source = here
	component := componentModel{Name: types.StringValue("api"), Source: here}
	branch := branchModel{Component: types.StringValue("api"), Name: types.StringValue("main"), Source: here}
	preset := presetModel{Name: types.StringValue("coder"), Source: here}

	calls := map[string]func(d *diagAdder) bool{
		"board":     func(d *diagAdder) bool { return (&agentBoardResource{source: provider}).apply(ctx, &board, d) },
		"component": func(d *diagAdder) bool { return (&componentResource{source: provider}).apply(ctx, &component, d) },
		"branch":    func(d *diagAdder) bool { return (&branchResource{source: provider}).apply(ctx, &branch, d) },
		"preset":    func(d *diagAdder) bool { return (&agentRolePresetResource{source: provider}).apply(ctx, &preset, d) },
		"preset delete": func(d *diagAdder) bool {
			return (&agentRolePresetResource{source: provider}).deactivate(ctx, &preset, d)
		},
	}
	for name, call := range calls {
		sent = nil
		var diags diag.Diagnostics
		if !call(&diagAdder{&diags}) || diags.HasError() {
			t.Fatalf("%s: apply failed: %v", name, diags)
		}
		if len(sent) != 1 {
			t.Fatalf("%s: %d applies, want 1", name, len(sent))
		}
		if got := fields(sent[0]); got != want {
			t.Errorf("%s sent %v, want %v", name, got, want)
		}
	}
}
