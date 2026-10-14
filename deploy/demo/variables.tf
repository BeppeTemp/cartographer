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
  description = "The read-only bearer token (kb:astronomy:r;kb:guestbook:r). The Worker sets it on every request, so it is never published: it keeps the *.run.app URL closed to anyone who goes around Cloudflare."
  type        = string
  sensitive   = true
}

variable "guestbook_token" {
  description = "The guestbook write token (kb:guestbook:rw). Only the Worker holds it, and attaches it to a moderated concept_new. Distinct from demo_token: openssl rand -hex 24."
  type        = string
  sensitive   = true
}

variable "guestbook_git_token" {
  description = "Fine-grained personal access token with contents read/write on the guestbook repository only. Expires: rotate it (deploy/demo/README.md)."
  type        = string
  sensitive   = true
}

variable "guestbook_remote" {
  description = "https URL of the private guestbook repository. Its name must be \"guestbook\": the KB is named after the remote's basename. Never committed."
  type        = string
}

variable "guestbook_open" {
  description = "Whether the Worker accepts guestbook messages. false is the kill switch: the guestbook stays readable."
  type        = bool
  default     = false
}

variable "atlas_origin" {
  description = "Origin of the public Atlas (GitHub Pages), allowed to read the live API cross-origin."
  type        = string
  default     = "https://beppetemp.github.io"
}

variable "notify_url" {
  description = "Optional webhook that receives each accepted message (Standard Webhooks signature). Empty sends nothing."
  type        = string
  sensitive   = true
  default     = ""
}

variable "notify_secret" {
  description = "Signing secret of notify_url, in the Standard Webhooks form: whsec_ followed by base64. Empty with notify_url empty."
  type        = string
  sensitive   = true
  default     = ""
}

variable "image" {
  description = "Image of the first apply. Later deploys come from CI, which Terraform ignores (lifecycle)."
  type        = string
}
