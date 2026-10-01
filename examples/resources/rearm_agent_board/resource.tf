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

  # Element id families over the defaults. defined_in lists the document types whose documents define the
  # prefix's ids, in order of precedence; a heading with the id anywhere else is a reference. Unset takes the
  # family's default; [] means references only.
  element_families = [
    { prefix = "T", family = "test", defined_in = ["TEST_PLAN"] },
    { prefix = "SAF", family = "safety" },
  ]

  settings = {
    budget_micros       = 50000000
    cycle_cap           = 6
    blocking_priority   = 2
    completion_priority = 1

    # Alert when an investigation is not done this many minutes after its deadline.
    staleness = {
      investigation_overdue_minutes = 60
    }

    # The level ladder, opt-in: with it every task has a work level (0 unless set), the poll offers lower
    # levels first, and every served prompt explains the rungs. Without it work levels are refused, so a
    # group's default_work_level below needs it. Removing it is refused while a task or group carries one.
    ladder = {
      levels = [
        { name = "requirements", description = "what the client needs" },
        { name = "solution", description = "the decisions that meet it" },
        { name = "components" },
        { name = "modules" },
      ]
    }
  }

  # Task groups, in display order: a group waits for the ones it depends on. A group dropped from
  # this list is closed, never deleted (the plan warns which); [] deletes them all while none holds
  # tasks. Leave groups unset to manage the board without its groups.
  groups = [
    { key = "core-work", name = "Core services" },
    { key = "ui-work", name = "Front end", depends_on = ["core-work"], default_work_level = 2 }, # a rung of the ladder
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
      # The designer may ask the reviewer for an investigation report; it comes back pinned on the
      # designer's task. Every role named must produce BOARD_INVESTIGATION_REPORT at TASK scope.
      commissions = {
        roles                 = ["reviewer"]
        intake                = "AUTO"
        default_budget_micros = 3000000
      }
    },
    {
      name       = "reviewer"
      prompt     = file("${path.module}/prompts/reviewer.md")
      necessity  = "REQUIRED"
      human_gate = "ON_ANY_SIGNOFF"
      required_inputs = [
        { kind = "DOCUMENT", specification = "ARCHITECTURE", scope = "TASK", min_lifecycle = "ASSEMBLED" },
      ]
      produces_outputs = [
        { specification = "BOARD_REVIEW_ITEMS", scope = "TASK", required = true },
        { specification = "BOARD_INVESTIGATION_REPORT", scope = "TASK", required = true }, # when commissioned
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
