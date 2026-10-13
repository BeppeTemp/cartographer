# The first four are the GitHub repository variables the deploy workflow reads
# (variables, not secrets: none of them is a credential).
output "GCP_WIF_PROVIDER" {
  value = google_iam_workload_identity_pool_provider.github.name
}

output "GCP_DEPLOY_SA" {
  value = google_service_account.deployer.email
}

output "GCP_REGION" {
  value = var.region
}

output "GCP_PROJECT" {
  value = var.project_id
}

output "service_url" {
  value = google_cloud_run_v2_service.demo.uri
}

output "artifact_registry_repo" {
  value = "${var.region}-docker.pkg.dev/${var.project_id}/${google_artifact_registry_repository.demo.repository_id}"
}
