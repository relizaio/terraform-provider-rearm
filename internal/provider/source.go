package provider

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	rearm "github.com/relizaio/rearm-client-go"
	"github.com/relizaio/rearm-client-go/catalog"
)

// sourceModel is where a configuration came from: the repository, the file in it and the commit.
// The server records it with every apply as the declarative provenance, so the board header and
// the feed can say where a change came from rather than "from a file".
type sourceModel struct {
	Repo   types.String `tfsdk:"repo"`
	Path   types.String `tfsdk:"path"`
	Commit types.String `tfsdk:"commit"`
}

const sourceDescription = "Where this configuration came from, recorded by ReARM with every apply " +
	"(declarative provenance). Each field set here overrides the provider's provenance for this " +
	"resource; the rest fall through. Typically the provider carries repo and commit from CI and each " +
	"resource names its file in path. Changing it re-applies the resource so the new provenance is recorded."

// resourceSourceAttribute is the optional provenance block every applying resource carries. It is
// called provenance rather than source because Terraform reserves source in provider blocks, and
// the provider and the resources name it the same way.
func resourceSourceAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:    true,
		Description: sourceDescription,
		Attributes: map[string]schema.Attribute{
			"repo":   schema.StringAttribute{Optional: true, Description: "Repository URL."},
			"path":   schema.StringAttribute{Optional: true, Description: "File or module path within the repository."},
			"commit": schema.StringAttribute{Optional: true, Description: "Commit the configuration was applied from."},
		},
	}
}

// providerData is what the provider hands every resource: the client, and the provider-level
// source already resolved against the CI environment.
type providerData struct {
	client *rearm.Client
	source *catalog.Source
}

// configured unpacks the provider data in a resource's Configure; nil means it is not configured yet.
func configured(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *providerData {
	if req.ProviderData == nil {
		return nil
	}
	pd, ok := req.ProviderData.(*providerData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("expected *providerData, got %T", req.ProviderData))
		return nil
	}
	return pd
}

// applySpec is catalog.Apply, behind a variable so tests can see what every call site sends.
var applySpec = catalog.Apply

// resolveProviderSource resolves the provider's source per field: the provider block first, then
// the CI environment, else unset. The path has no fallback -- a run cannot know which file a
// resource came from. What the environment or the operator says is recorded as given: a run has no
// working tree, so the CLI's "no commit when the tree is dirty" rule has nothing to check.
func resolveProviderSource(cfg *sourceModel, getenv func(string) string) *catalog.Source {
	var repo, path, commit string
	if cfg != nil {
		repo, path, commit = value(cfg.Repo), value(cfg.Path), value(cfg.Commit)
	}
	repo = firstNonEmpty(repo, githubRepo(getenv), strings.TrimSpace(getenv("CI_PROJECT_URL")))
	commit = firstNonEmpty(commit, strings.TrimSpace(getenv("GITHUB_SHA")), strings.TrimSpace(getenv("CI_COMMIT_SHA")))
	return newSource(repo, path, commit)
}

// githubRepo is the repository URL a GitHub Actions run names, or "" outside Actions.
func githubRepo(getenv func(string) string) string {
	repository := strings.TrimSpace(getenv("GITHUB_REPOSITORY"))
	if repository == "" {
		return ""
	}
	server := strings.TrimRight(strings.TrimSpace(getenv("GITHUB_SERVER_URL")), "/")
	if server == "" {
		server = "https://github.com"
	}
	return server + "/" + repository
}

// sourceFor merges a resource's source over the provider's, per field. Nil when nothing is known,
// so the server records no source rather than an empty one.
func sourceFor(provider *catalog.Source, res *sourceModel) *catalog.Source {
	var repo, path, commit string
	if provider != nil {
		repo, path, commit = deref(provider.Repo), deref(provider.Path), deref(provider.Commit)
	}
	if res != nil {
		repo = firstNonEmpty(value(res.Repo), repo)
		path = firstNonEmpty(value(res.Path), path)
		commit = firstNonEmpty(value(res.Commit), commit)
	}
	return newSource(repo, path, commit)
}

func newSource(repo, path, commit string) *catalog.Source {
	if repo == "" && path == "" && commit == "" {
		return nil
	}
	return &catalog.Source{Repo: ptrOrNil(repo), Path: ptrOrNil(path), Commit: ptrOrNil(commit)}
}

// value is a set string attribute, trimmed; null, unknown and blank all read as unset.
func value(v types.String) string {
	if v.IsNull() || v.IsUnknown() {
		return ""
	}
	return strings.TrimSpace(v.ValueString())
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
