resource "rearm_agent_board" "platform" {
  name        = "platform"
  description = "the platform team's board"
  target      = rearm_component.api.name
  sources     = ["github:acme/platform", "github:acme/platform-docs"]

  documents_repo = "https://github.com/acme/platform-docs"
  document_paths = {
    ARCHITECTURE = "docs/design/{task}.md"
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
      strength = {
        required_strength = 0.75
        strength_category = "REVIEWER"
      }
    },
  ]
}
