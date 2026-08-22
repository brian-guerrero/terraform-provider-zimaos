terraform {
  required_providers {
    zimaos = {
      source = "brian-guerrero/zimaos"
    }
  }
}

# Preferred: username + password. Tokens are short-lived, so the provider
# exchanges credentials for a fresh one via POST /login on each run instead
# of relying on a token you'd otherwise have to keep pasting in.
provider "zimaos" {
  host     = "http://192.168.1.50:8080"
  username = var.zimaos_username
  password = var.zimaos_password
}

# Alternative: a pre-obtained API token instead of username/password.
# provider "zimaos" {
#   host  = "http://192.168.1.50:8080"
#   token = var.zimaos_token
# }
