resource "rearm_agent_board" "platform" {
  # Recorded with each apply; repo and commit come from the provider or the CI environment.
  provenance = {
    path = "terraform/boards.tf"
  }

  name        = "platform"
  description = "the platform team's board"
  target      = rearm_component.api.name
  sources     = ["github:acme/platform", "github:acme/platform-docs"]

  documents_repo = "https://github.com/acme/platform-docs"

  # Perspectives the board hangs off; the target must be a member of each. product: marks a PRODUCT
  # component used as a perspective.
  perspectives = ["platform", "product:checkout"]

  coordinator_capabilities = ["PR_MERGE"] # the coordinator merges once the last required role has passed

  document_paths = {
    # {key} is the task's key (RD-42); {round}, {type} and {component} are the others.
    ARCHITECTURE = "docs/design/{key}/architecture-{round}.md"
  }

  settings = {
    budget_micros       = 50000000
    cycle_cap           = 6
    blocking_priority   = 2
    completion_priority = 1
  }

  # The role list: a role not listed here is deactivated, never deleted.
  # Leave roles unset to manage the board without its roles.
  roles = [
    {
      name   = "designer"
      prompt = file("${path.module}/prompts/designer.md")
      produces_outputs = [
        { specification = "ARCHITECTURE", scope = "TASK", required = true },
      ]
    },
    {
      name       = "reviewer"
      prompt     = file("${path.module}/prompts/reviewer.md")
      necessity  = "REQUIRED"
      human_gate = "ON_ANY_SIGNOFF"
      required_inputs = [
        { kind = "DOCUMENT", specification = "ARCHITECTURE", scope = "TASK", min_lifecycle = "ASSEMBLED" },
      ]
      hop_budget_micros = 2000000 # allowance per hop: flagged when exceeded, not enforced
      blind_review      = true    # reads the task without earlier hops' notes, sessions and agents
      strength = {
        required_strength = 0.75
        strength_category = "REVIEWER"
      }
    },
  ]
}
