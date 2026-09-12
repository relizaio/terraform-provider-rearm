# A feature set of a product, pinned to the api component's main branch.
resource "rearm_branch" "platform_stable" {
  component      = rearm_component.platform.name
  name           = "stable"
  type           = "FEATURE"
  auto_integrate = "ENABLED"

  dependency {
    component = rearm_component.api.name
    branch    = "main"
  }

  dependency_pattern {
    pattern            = "^acme-.*"
    target_branch_name = "main"
    fallback_to_base   = "ENABLED"
  }
}
