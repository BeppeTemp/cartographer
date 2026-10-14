# Live MCP demo

A public, read-only MCP endpoint over the `demo/astronomy` KB (D751):
`https://cartographer-demo.beppetemp.com/mcp`, with no token to configure (D789). The Worker sets
the read-only token (scope `kb:astronomy:r`) on every request, so the token is a secret held by
Terraform and is never published.

## What runs where

| Piece | Where | Defined in |
|---|---|---|
| Server + baked-in KB | Cloud Run `cartographer-demo`, project `cartographer-demo`, `europe-west1`; min 0, max 1 instance, concurrency 20 | `main.tf`, `Dockerfile` |
| KB history | `demo/replay.sh`, run in the image build (relative to the build date, so rebuilt every release) | `Dockerfile` |
| Public hostname | Cloudflare Worker (custom domain) forwarding to the `*.run.app` URL with the token set and `/.well-known/` answering 404, plus one per-IP rate-limit rule (30 requests / 10 s) | `worker.js`, `main.tf` |
| Deploys | `.github/workflows/demo.yml`, on every release or by hand, through Workload Identity Federation (no key) | `main.tf` |
| Cost guard | 5 EUR budget alert on the project | `main.tf` |

The `*.run.app` URL stays reachable around Cloudflare, but only with the token, which is not
published; `max-instances = 1` is the backstop.
Probes use `/health`: with auth on, `/ready` needs the token.

## Bootstrap (once, by the maintainer)

Needs `gcloud` (ADC: `gcloud auth application-default login`), `terraform`,
`CLOUDFLARE_API_TOKEN` in the environment (Workers Scripts and Zone Rulesets edit, DNS read).

1. State bucket (the one step outside Terraform):

   ```bash
   gcloud storage buckets create gs://cartographer-demo-tfstate \
     --project cartographer-demo --location europe-west1 --uniform-bucket-level-access
   gcloud storage buckets update gs://cartographer-demo-tfstate --versioning
   ```

2. `cp terraform.tfvars.example terraform.tfvars` and fill in the ids and a random `demo_token`
   (gitignored).
3. The service needs an image before its first revision starts: create the registry, push
   an image built from a release, then apply everything.

   ```bash
   terraform init
   terraform apply -target=google_project_service.enabled -target=google_artifact_registry_repository.demo
   ctx=$(mktemp -d); mkdir -p "$ctx/deploy/demo" "$ctx/demo"
   cp ../../deploy/demo/Dockerfile "$ctx/deploy/demo/"; cp -R ../../demo/astronomy ../../demo/replay.sh "$ctx/demo/"
   gcloud builds submit --project cartographer-demo --config cloudbuild.bootstrap.yaml \
     --substitutions _TAG=<tag> "$ctx"
   terraform apply
   ```

   The image is built by Cloud Build, not locally: Cloud Run needs `linux/amd64`, and a
   local `docker build --platform linux/amd64` on Apple silicon fails with "does not provide
   the specified platform" when the classic builder has the arm64 base cached. The context is
   only the Dockerfile and `demo/`: the whole repository is too large to upload.
4. Set the repository **variables** (not secrets) from `terraform output`:
   `GCP_WIF_PROVIDER`, `GCP_DEPLOY_SA`, `GCP_REGION`, `GCP_PROJECT`. From then on every
   release deploys; `gh workflow run demo.yml -f tag=<tag>` redeploys without one.

## Checks

- `make demo-smoke`: builds the image and runs it locally: read tool answers, write tool
  refused, no token gets 401. `DEMO_BASE=cartographer` after `make docker` tests the working tree.
- `make demo-tf-check`: `terraform fmt -check` and `validate` (no credentials).

## Rotating the token

Change `demo_token` in `terraform.tfvars` and `terraform apply`: a new Cloud Run revision (no
rebuild) and the Worker's secret binding change together. Nothing else carries the token.

## Tearing down

`terraform destroy`, then delete the state bucket and the `GCP_*` repository variables (the
`demo` job then skips itself).
