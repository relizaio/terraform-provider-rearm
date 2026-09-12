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
}
