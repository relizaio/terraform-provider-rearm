resource "rearm_component" "api" {
  name                      = "payments-api"
  type                      = "COMPONENT"
  version_schema            = "semver"
  feature_branch_versioning = "Branch.Micro"
  vcs_uri                   = "https://github.com/acme/payments-api"
  vcs_type                  = "git"
  nature                    = "SOFTWARE"
}

resource "rearm_component" "platform" {
  name           = "acme-platform"
  type           = "PRODUCT"
  version_schema = "YY.0M.Micro"
}
