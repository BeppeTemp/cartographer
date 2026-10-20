# The live MCP demo (D751, D800): Cloud Run behind a Cloudflare Worker. Read-only,
# except for guestbook messages the Worker has moderated.

locals {
  services = [
    "run.googleapis.com",
    "artifactregistry.googleapis.com",
    "iam.googleapis.com",
    "iamcredentials.googleapis.com",
    "sts.googleapis.com",
    "billingbudgets.googleapis.com",
    "cloudresourcemanager.googleapis.com",
    "cloudbuild.googleapis.com",
    "secretmanager.googleapis.com",
  ]
}

data "google_project" "this" {
  project_id = var.project_id
}

resource "google_project_service" "enabled" {
  for_each           = toset(local.services)
  service            = each.value
  disable_on_destroy = false
}

resource "google_artifact_registry_repository" "demo" {
  repository_id = "demo"
  location      = var.region
  format        = "DOCKER"
  depends_on    = [google_project_service.enabled]
}

# --- the service -------------------------------------------------------------

# One role, on one secret (below): the server calls nothing else in GCP.
resource "google_service_account" "runtime" {
  account_id   = "demo-runtime"
  display_name = "Cartographer demo (runtime)"
  depends_on   = [google_project_service.enabled]
}

# The guestbook repository's access token, mounted as the file guestbook.token
# under /etc/guestbook-git (GUESTBOOK_TOKEN_FILE in the image). The entrypoint
# copies it into the server's credentials directory as guestbook.token (D905),
# and git reads it from there when it asks for credentials, so it is never in an
# env var, argv or .git/config.
resource "google_secret_manager_secret" "guestbook_git_token" {
  secret_id = "guestbook-git-token"

  replication {
    auto {}
  }

  depends_on = [google_project_service.enabled]
}

resource "google_secret_manager_secret_version" "guestbook_git_token" {
  secret      = google_secret_manager_secret.guestbook_git_token.id
  secret_data = var.guestbook_git_token
}

resource "google_secret_manager_secret_iam_member" "runtime_guestbook_git_token" {
  secret_id = google_secret_manager_secret.guestbook_git_token.id
  role      = "roles/secretmanager.secretAccessor"
  member    = "serviceAccount:${google_service_account.runtime.email}"
}

resource "google_cloud_run_v2_service" "demo" {
  name                = "cartographer-demo"
  location            = var.region
  ingress             = "INGRESS_TRAFFIC_ALL"
  deletion_protection = false

  template {
    service_account                  = google_service_account.runtime.email
    max_instance_request_concurrency = 20

    scaling {
      min_instance_count = 0
      max_instance_count = 1
    }

    volumes {
      name = "guestbook-git"
      secret {
        secret = google_secret_manager_secret.guestbook_git_token.secret_id
        items {
          version = "latest"
          path    = "guestbook.token"
        }
      }
    }

    containers {
      image = var.image

      ports {
        container_port = 8080
      }

      resources {
        limits = {
          cpu    = "1"
          memory = "512Mi"
        }
      }

      # Two tokens (CARTOGRAPHER_TOKENS format: docs/deployment.md): the read
      # token for all traffic, the write token only for the one write the
      # Worker approves. Max 1 instance below means a single writer.
      env {
        name  = "CARTOGRAPHER_TOKENS"
        value = "${var.demo_token}|kb:astronomy:r;kb:guestbook:r,${var.guestbook_token}|kb:guestbook:rw"
      }

      # The remote's basename is the KB name: the repository must be named
      # "guestbook" (deploy/demo/entrypoint.sh).
      env {
        name  = "GUESTBOOK_REMOTE"
        value = var.guestbook_remote
      }

      volume_mounts {
        name       = "guestbook-git"
        mount_path = "/etc/guestbook-git"
      }

      # /health, not /ready: once auth is on, /ready needs the token
      # (deployment.md §Observability). /health answers while the KB is still
      # opening and every other path is a 503 with Retry-After until then.
      startup_probe {
        http_get {
          path = "/health"
        }
        period_seconds    = 2
        failure_threshold = 30
      }

      liveness_probe {
        http_get {
          path = "/health"
        }
      }
    }
  }

  lifecycle {
    # CI deploys new images; Terraform must not roll them back.
    ignore_changes = [
      template[0].containers[0].image,
      client,
      client_version,
    ]
  }

  depends_on = [
    google_project_service.enabled,
    google_secret_manager_secret_iam_member.runtime_guestbook_git_token,
    google_secret_manager_secret_version.guestbook_git_token,
  ]
}

# Anyone may call the service: the gate is the app's bearer token.
resource "google_cloud_run_v2_service_iam_member" "public" {
  name     = google_cloud_run_v2_service.demo.name
  location = var.region
  role     = "roles/run.invoker"
  member   = "allUsers"
}

# --- budget ------------------------------------------------------------------

resource "google_billing_budget" "demo" {
  billing_account = var.billing_account
  display_name    = "cartographer-demo"

  budget_filter {
    projects = ["projects/${data.google_project.this.number}"]
  }

  amount {
    specified_amount {
      currency_code = "EUR"
      units         = "5"
    }
  }

  dynamic "threshold_rules" {
    for_each = [0.5, 0.9, 1.0]
    content {
      threshold_percent = threshold_rules.value
    }
  }

  depends_on = [google_project_service.enabled]
}

# --- deploys from GitHub Actions, no key anywhere ----------------------------

resource "google_iam_workload_identity_pool" "github" {
  workload_identity_pool_id = "github"
  depends_on                = [google_project_service.enabled]
}

resource "google_iam_workload_identity_pool_provider" "github" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.github.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-actions"

  attribute_mapping = {
    "google.subject"       = "assertion.sub"
    "attribute.repository" = "assertion.repository"
  }

  # This repository only, from a release tag or from a manual run on main.
  attribute_condition = <<-EOT
    assertion.repository == "${var.github_repository}" && (
      assertion.ref.startsWith("refs/tags/v") ||
      (assertion.event_name == "workflow_dispatch" && assertion.ref == "refs/heads/main")
    )
  EOT

  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
}

resource "google_service_account" "deployer" {
  account_id   = "demo-deployer"
  display_name = "Cartographer demo (GitHub Actions deploy)"
  depends_on   = [google_project_service.enabled]
}

resource "google_service_account_iam_member" "deployer_wif" {
  service_account_id = google_service_account.deployer.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principalSet://iam.googleapis.com/${google_iam_workload_identity_pool.github.name}/attribute.repository/${var.github_repository}"
}

resource "google_cloud_run_v2_service_iam_member" "deployer" {
  name     = google_cloud_run_v2_service.demo.name
  location = var.region
  role     = "roles/run.developer"
  member   = "serviceAccount:${google_service_account.deployer.email}"
}

resource "google_artifact_registry_repository_iam_member" "deployer" {
  repository = google_artifact_registry_repository.demo.name
  location   = var.region
  role       = "roles/artifactregistry.writer"
  member     = "serviceAccount:${google_service_account.deployer.email}"
}

# Deploying a revision that runs as the runtime account needs actAs on it, and
# on nothing else.
resource "google_service_account_iam_member" "deployer_acts_as_runtime" {
  service_account_id = google_service_account.runtime.name
  role               = "roles/iam.serviceAccountUser"
  member             = "serviceAccount:${google_service_account.deployer.email}"
}

# --- Cloudflare: Worker in front, one rate-limit rule -------------------------

# Rate-limit counters of the guestbook: expiring keys, two writes per message
# attempt (the free plan allows 1,000 writes a day).
resource "cloudflare_workers_kv_namespace" "guestbook_rl" {
  account_id = var.cloudflare_account_id
  title      = "cartographer-demo-guestbook-rl"
}

resource "cloudflare_workers_script" "proxy" {
  account_id  = var.cloudflare_account_id
  script_name = "cartographer-demo"
  content     = file("${path.module}/worker.js")
  main_module = "worker.js"

  # DEMO_TOKEN is the read token; GUESTBOOK_TOKEN is attached only to a
  # moderated concept_new (worker.js).
  bindings = concat([{
    name = "UPSTREAM"
    type = "plain_text"
    text = google_cloud_run_v2_service.demo.uri
    }, {
    name = "DEMO_TOKEN"
    type = "secret_text"
    text = var.demo_token
    }, {
    name = "GUESTBOOK_TOKEN"
    type = "secret_text"
    text = var.guestbook_token
    }, {
    name = "GUESTBOOK_OPEN"
    type = "plain_text"
    text = tostring(var.guestbook_open)
    }, {
    name = "ATLAS_ORIGIN"
    type = "plain_text"
    text = var.atlas_origin
    }, {
    name         = "GUESTBOOK_RL"
    type         = "kv_namespace"
    namespace_id = cloudflare_workers_kv_namespace.guestbook_rl.id
    }, {
    name = "AI"
    type = "ai"
    }],
    # An empty secret is not a binding: with no URL the Worker sends nothing.
    var.notify_url == "" ? [] : [{
      name = "NOTIFY_URL"
      type = "secret_text"
      text = var.notify_url
      }, {
      name = "NOTIFY_SECRET"
      type = "secret_text"
      text = var.notify_secret
  }])
}

resource "cloudflare_workers_custom_domain" "proxy" {
  account_id = var.cloudflare_account_id
  zone_id    = var.cloudflare_zone_id
  hostname   = var.hostname
  service    = cloudflare_workers_script.proxy.script_name
}

# The free plan allows one rule, with a 10 s period. It runs before the Worker.
# ip.src alone is refused on the free plan: cf.colo.id must be a characteristic.
resource "cloudflare_ruleset" "rate_limit" {
  zone_id     = var.cloudflare_zone_id
  name        = "cartographer-demo rate limit"
  description = "Per client IP on the demo hostname"
  kind        = "zone"
  phase       = "http_ratelimit"

  rules = [{
    action      = "block"
    description = "30 requests per 10 s per IP"
    enabled     = true
    expression  = "(http.host eq \"${var.hostname}\")"
    ratelimit = {
      characteristics     = ["cf.colo.id", "ip.src"]
      period              = 10
      requests_per_period = 30
      mitigation_timeout  = 10
    }
  }]
}
