# Live MCP demo

A public, read-only MCP endpoint over two KBs (D751, D2053): `demo/astronomy` and
`cartographer-docs`, the documentation of the image's own binary, so an agent can ask Cartographer
about itself. `https://cartographer-demo.beppetemp.com/mcp`, with no token to configure (D789). The
Worker sets one read-only token (`kb:astronomy:r;kb:cartographer-docs:r`) on every request; it is a
secret held by Terraform and is never published. The demo has no write token and no write path: the
guestbook of D800 is gone.

## What runs where

| Piece | Where | Defined in |
|---|---|---|
| Server + baked-in KBs | Cloud Run `cartographer-demo`, project `cartographer-demo`, `europe-west1`; min 0, max 1 instance, concurrency 20 | `main.tf`, `Dockerfile` |
| Documentation KB | `cartographer-docs`, written at image build by the base image's `cartographer docs export` and sealed by one commit, so nothing external is cloned at start and the docs are those of the binary the image runs | `Dockerfile` |
| KB history | `demo/replay.sh`, run in the image build (relative to the build date, so rebuilt every release) | `Dockerfile` |
| Public hostname | Cloudflare Worker (custom domain): maps `/mcp` to `/mcp/routed`, sets the read token, caps the body of `/mcp` at 16 KB, caches `GET /api/ui/v1/*` for 5 s with CORS for the Pages Atlas, answers 404 under `/.well-known/`; plus one per-IP rate-limit rule (30 requests / 10 s) | `worker.js`, `main.tf` |
| Public Atlas | GitHub Pages (`docs.yml`, `demo/build.sh`); reads the live API through the Worker, falls back to its snapshot | `demo/build.sh` |
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

## Free-tier budget

Nothing here needs a paid plan; each limit and what happens at it:

| Resource | Free limit | How it is kept | When exceeded |
|---|---|---|---|
| Worker requests | 100k/day | Atlas assets on Pages; API responses cached at the edge; poll every 20 s on the Pages build | Cloudflare refuses until 00:00 UTC; the Pages Atlas falls back to the snapshot, MCP is unavailable |
| Per-IP edge rate limit | 1 WAF rule on the free plan | The rule (30 requests / 10 s per IP) runs before the Worker, so blocked requests cost no Worker quota | 10 s block for that IP |
| Cloud Run | monthly free tier, max 1 instance | Edge cache of 5 s on `GET /api/ui/v1/*`: all visitors share the read token, so Cloud Run sees about one request per URL per 5 s per Cloudflare location | Budget alert at 5 EUR (D751) |

A single determined attacker can still exhaust the daily Worker quota; the free plan accepts that, the
failure is graceful and the next UTC day starts clean.

## Releasing and redeploying

The image is built from the release's own server image (`BASE`), whose `cartographer docs export` writes the
documentation KB, so a release deploys documentation and binary together. `gh workflow run demo.yml -f tag=<tag>`
redeploys any tag, with one limit: a tag that predates `cartographer docs export` (before 0.29) fails the image
build, because its binary cannot write the documentation KB. Redeploy a release from 0.29 on.

The read token's scopes live in Terraform. A release that adds a KB to the image shows it only after a
maintainer `terraform apply` in this folder, so apply **before** the release that carries it (0.29: the scopes
become `kb:astronomy:r;kb:cartographer-docs:r` and the guestbook secret, its version and IAM binding, the KV
namespace and the Worker's moderation bindings are destroyed). Until then the `*.run.app` service keeps serving
the previous image, and the `demo` workflow's post-deploy check fails.

## Checks

- `make demo-smoke`: builds the image and runs it locally: the audit log is signed, a read tool answers on both
  KBs, write tools are refused with the read token, the routed `tools/list` shows `kb` as a required string,
  D1183, a `search` on `cartographer-docs` finds a page, the docs KB carries the image's version, and no token
  gets 401. `DEMO_BASE=cartographer` after `make docker` tests the working tree.
- `make demo-worker-test`: the Worker's routing and cache logic under `node --test` (no Cloudflare needed).
- `make demo-tf-check`: `terraform fmt -check` and `validate` (no credentials).

## Rotating the token

Change `demo_token` (the read token) in `terraform.tfvars` and `terraform apply`:
a new Cloud Run revision (no rebuild) and the Worker's secret binding change together. Nothing else
carries it.

## Tearing down

`terraform destroy`, then delete the state bucket and the `GCP_*` repository variables (the
`demo` job then skips itself).
