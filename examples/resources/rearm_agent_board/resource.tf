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

  # Task keys: PL-1, PL-2... Unset, ReARM derives a prefix from the name. A change is a rename in
  # place: existing keys stay, and a prefix once used is never reused in the organization.
  task_prefix = "PL"

  # The docs repository serves several boards, so this one's documents go under boards/platform/.
  # root = "" would put them at the repository root instead.
  documents = {
    prefix = "platform"
    shared = true
  }

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

  # Task groups, in display order: a group waits for the ones it depends on. A group dropped from
  # this list is closed, never deleted (the plan warns which); [] deletes them all while none holds
  # tasks. Leave groups unset to manage the board without its groups.
  groups = [
    { key = "core-work", name = "Core services" },
    { key = "ui-work", name = "Front end", depends_on = ["core-work"], default_level = 2 },
  ]

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
