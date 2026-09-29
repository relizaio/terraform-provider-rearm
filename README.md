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
| `rearm_api_key` | declared key name | API_KEYS file (one FREEFORM key, not authoritative); delete deactivates; never a secret |

| Ephemeral resource | Mints |
|---|---|
| `rearm_api_key_secret` | a secret in slot 1 or 2 of a key, for a consumer in the same run; nothing in state |

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

`settings.ladder` declares the board's level ladder, opt-in: its rungs in order (a name and an
optional description), each numbered by its place from 0. With it every task has a level (0 unless
set) and the served prompts explain the rungs; without it a level -- a group's `default_level`
included -- is refused. It is read back whole, and removing it is refused while a task or group
carries a level.

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

### API keys and their secrets

`rearm_api_key` declares a FREEFORM key's identity and settings -- its permissions, notes, status,
`secret_expires_days`, `session_max_minutes` -- and never holds a secret: a key it creates has none,
and state carries only `secret_slots` metadata (slot, active, dates). FREEFORM is the only type
declared: ORGANIZATION and ORGANIZATION_RW keys are deprecated. The provider's key needs
CONFIGURATION_WRITE and declares only keys no stronger than itself.

Secrets are minted by the `rearm_api_key_secret` ephemeral resource (Terraform and OpenTofu 1.10 and
up) and handed to a consumer in the same run, such as a write-only attribute of a secret store; they
never reach state or plan files. Name the key by `rearm_api_key.<name>.id`, so the mint waits until
the key exists. An empty slot is minted once; later runs read `minted = false` with no value, since
ReARM keeps only a hash. `rotate = true` mints a new value on every open (plan included) and the old
one stops working, so set it for a rotation run only. A person does the same with
`rearm apikey mint <key> --slot 1`. See `examples/resources/rearm_api_key/resource.tf`.

Import: `terraform import rearm_component.api payments-api`,
`terraform import rearm_branch.platform_stable acme-platform/stable`,
`terraform import rearm_agent_board.platform platform`,
`terraform import rearm_agent_role_preset.coder coder`,
`terraform import rearm_api_key.ci ci-release`.

## Development

```
go build ./...
```

Use a `dev_overrides` block in `~/.terraformrc` to point Terraform at the local binary.
Releases follow the Terraform Registry layout in `.goreleaser.yml` (signed checksums,
registry manifest).
