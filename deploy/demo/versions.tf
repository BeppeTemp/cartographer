terraform {
  required_version = ">= 1.6"

  required_providers {
    google = {
      source  = "hashicorp/google"
      version = "~> 6.0"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 5.0"
    }
  }

  # The state bucket is the one bootstrap step outside Terraform (README).
  backend "gcs" {
    bucket = "cartographer-demo-tfstate"
    prefix = "demo"
  }
}

provider "google" {
  project = var.project_id
  region  = var.region

  # The budget API is billed to a project: without this it asks for the one
  # ADC was issued in.
  billing_project       = var.project_id
  user_project_override = true
}

# Authenticates with CLOUDFLARE_API_TOKEN from the environment.
provider "cloudflare" {}
