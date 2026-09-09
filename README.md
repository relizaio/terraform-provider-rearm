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

Semantics follow the declarative model: an attribute left unset is not managed by Terraform
and keeps its value in ReARM; the `dependency` and `dependency_pattern` blocks are owned as a
whole, so omitting them means none. Removing a resource from configuration removes it from state only, with
a warning: archiving is governed by the organization's declarative prune setting, not by a
single resource.

Import: `terraform import rearm_component.api payments-api`,
`terraform import rearm_branch.platform_stable acme-platform/stable`.

## Development

```
go build ./...
```

Use a `dev_overrides` block in `~/.terraformrc` to point Terraform at the local binary.
Releases follow the Terraform Registry layout in `.goreleaser.yml` (signed checksums,
registry manifest).
