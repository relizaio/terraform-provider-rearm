# terraform-provider-rearm

Terraform / OpenTofu provider for [ReARM](https://rearmhq.com), built on
[rearm-client-go](https://github.com/relizaio/rearm-client-go).

```hcl
terraform {
  required_providers {
    rearm = { source = "relizaio/rearm" }
  }
}

provider "rearm" {
  uri = "https://app.rearmhq.com"   # or REARM_URI
  # api_key_id / api_key, or REARM_APIKEYID / REARM_APIKEY
}

resource "rearm_component" "api" {
  name                      = "payments-api"
  type                      = "COMPONENT"
  version_schema            = "semver"
  feature_branch_versioning = "Branch.Micro"
  vcs_uri                   = "https://github.com/acme/payments-api"
}

resource "rearm_branch" "platform_stable" {
  component      = "acme-platform"
  name           = "stable"
  type           = "FEATURE"
  auto_integrate = "ENABLED"
  dependency {
    component = rearm_component.api.name
    branch    = "main"
  }
}
```

## Resources

| Resource | Identity | Backed by |
|---|---|---|
| `rearm_component` | component name | declarative Catalog apply / export |
| `rearm_branch` | `component/name` | declarative Branches apply / export |
| `rearm_agent_board` | board name | board file apply / export; delete archives |
| `rearm_agent_role_preset` | preset name | presets file (one preset, not authoritative); delete deactivates |

Semantics follow the declarative model: an attribute left unset is not managed by Terraform
and keeps its value in ReARM; the `dependency` and `dependency_pattern` blocks are owned as a
whole, so omitting them means none. Removing a resource from configuration removes it from state only, with
a warning: archiving is governed by the organization's declarative prune setting, not by a
single resource.

A board's `roles`, when set, is its role list: a role not in it is deactivated (never deleted,
since tasks point at it), and `roles = []` deactivates them all; left unset, the roles are not
managed. The board's nested attributes (`settings`, a role's `strength`, `required_inputs`,
`produces_outputs`) follow the same rule: unset is not managed, `[]` is none. Destroying a board
archives it, keeping its tasks and history; destroying a preset deactivates it. A change that
leaves tasks waiting on a deactivated role comes back as a Terraform warning.

### Provenance

Every apply tells ReARM where the configuration came from, and ReARM records it as the entity's
declarative provenance: the board header links to the repository and commit, and the board's feed
names them. Set `provenance` on the provider and, per field, on any resource; a resource's field wins,
the rest fall through. Unset provider fields fall back to the CI environment: `repo` to
`GITHUB_SERVER_URL`/`GITHUB_REPOSITORY` in GitHub Actions or `CI_PROJECT_URL` in GitLab, `commit`
to `GITHUB_SHA` or `CI_COMMIT_SHA`. `path` has no fallback, since a run cannot know which file a
resource came from, so name it on the resource. Values are recorded as given: a run has no
working tree, so unlike the CLI there is no "no commit when the file is dirty" check, and the
environment is the honest default. `provenance` is not read back from ReARM.

ReARM records provenance with an apply that changes the resource. Changing only a resource's
`provenance` block plans an in-place update that stores the new value in Terraform state, but ReARM
keeps the provenance of the last apply that changed something, and records the new one with the
resource's next change. The plan says so with a warning ("ReARM will not record this provenance
change"). Provider-level provenance, such as a new commit on every CI run, is not resource state and
plans nothing by itself. The block is called `provenance` rather than `source` because Terraform
reserves `source` in provider blocks.

Import: `terraform import rearm_component.api payments-api`,
`terraform import rearm_branch.platform_stable acme-platform/stable`,
`terraform import rearm_agent_board.platform platform`,
`terraform import rearm_agent_role_preset.coder coder`.

## Development

```
go build ./...
```

Use a `dev_overrides` block in `~/.terraformrc` to point Terraform at the local binary.
Releases follow the Terraform Registry layout in `.goreleaser.yml` (signed checksums,
registry manifest).
