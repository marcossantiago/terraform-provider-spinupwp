terraform {
  required_providers {
    spinupwp = {
      source  = "marcossantiago/spinupwp"
      version = "~> 0.1"
    }
  }
}

# The token is read from SPINUPWP_API_TOKEN when api_token is not set.
provider "spinupwp" {}
