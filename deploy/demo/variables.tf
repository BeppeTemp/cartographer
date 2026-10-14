variable "project_id" {
  type    = string
  default = "cartographer-demo"
}

variable "region" {
  type    = string
  default = "europe-west1"
}

variable "hostname" {
  description = "Public hostname of the endpoint; its zone is on Cloudflare."
  type        = string
  default     = "cartographer-demo.beppetemp.com"
}

variable "billing_account" {
  description = "Billing account id of the project, for the budget alert."
  type        = string
}

variable "cloudflare_account_id" {
  type = string
}

variable "cloudflare_zone_id" {
  description = "Zone that holds var.hostname."
  type        = string
}

variable "github_repository" {
  description = "owner/name allowed to deploy through Workload Identity Federation."
  type        = string
  default     = "BeppeTemp/cartographer"
}

variable "demo_token" {
  description = "The read-only bearer token (kb:astronomy:r). The Worker sets it on every request, so it is never published: it keeps the *.run.app URL closed to anyone who goes around Cloudflare."
  type        = string
  sensitive   = true
}

variable "image" {
  description = "Image of the first apply. Later deploys come from CI, which Terraform ignores (lifecycle)."
  type        = string
}
