terraform {
  required_providers {
    rearm = {
      source = "relizaio/rearm"
    }
  }
}

# Credentials can also come from REARM_URI / REARM_APIKEYID / REARM_APIKEY.
provider "rearm" {
  uri = "https://app.rearmhq.com"

  # Recorded with every apply as the configuration's provenance. In GitHub Actions or GitLab CI,
  # repo and commit come from the environment when left out; set path per resource.
  provenance = {
    repo = "https://github.com/acme/platform-config"
  }
}
