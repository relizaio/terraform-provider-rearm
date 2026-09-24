# Seeds the coordinator prompt of new boards without wired sources.
resource "rearm_agent_role_preset" "coordinator" {
  name   = "coordinator-board-truth"
  prompt = file("${path.module}/presets/coordinator-board-truth.md")
}

resource "rearm_agent_role_preset" "coder" {
  name   = "coder"
  prompt = file("${path.module}/presets/coder.md")
  strength = {
    required_strength = 0.5
    strength_category = "CODER"
  }
}
