# A CI key (FREEFORM): its identity and settings are configuration, its secret is not.
resource "rearm_api_key" "ci" {
  name = "ci-release"
  permissions = {
    type      = "READ_WRITE"
    functions = ["BOARD_WRITE", "CONFIGURATION_READ"]
  }
  notes               = "release pipeline"
  secret_expires_days = 90
  session_max_minutes = 30
}

# The secret, minted once into slot 1 and handed to a consumer in the same run; it is never written to
# state or plan files. Set rotate = true for a run meant to replace it.
ephemeral "rearm_api_key_secret" "ci" {
  key  = rearm_api_key.ci.id
  slot = 1
}
