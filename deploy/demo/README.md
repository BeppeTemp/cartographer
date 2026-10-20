# Live MCP demo

A public MCP endpoint over two KBs (D751, D800): `demo/astronomy`, read-only, and a moderated
guestbook. `https://cartographer-demo.beppetemp.com/mcp`, with no token to configure (D789). The
Worker sets a read-only token (`kb:astronomy:r;kb:guestbook:r`) on every request, and a write token
(`kb:guestbook:rw`) only on a guestbook message it has moderated. Both are secrets held by Terraform
and are never published.

## What runs where

| Piece | Where | Defined in |
|---|---|---|
| Server + baked-in KB | Cloud Run `cartographer-demo`, project `cartographer-demo`, `europe-west1`; min 0, max 1 instance (a single writer), concurrency 20 | `main.tf`, `Dockerfile` |
| Guestbook KB | Seed in `demo/guestbook/`; lives in a private git remote cloned at start by the entrypoint (the server starts without it if the clone fails), pushed after every write; the git token is a Secret Manager secret mounted as a file, which the entrypoint copies into the server's credentials directory as `guestbook.token`, so that directory stays writable for the audit key (D905) | `entrypoint.sh`, `main.tf` |
| KB history | `demo/replay.sh`, run in the image build (relative to the build date, so rebuilt every release) | `Dockerfile` |
| Public hostname | Cloudflare Worker (custom domain): maps `/mcp` to `/mcp/routed`, sets the read token, moderates guestbook messages (rules, KV rate limits, Workers AI classifier, kill switch), caches `GET /api/ui/v1/*` for 5 s with CORS for the Pages Atlas, answers 404 under `/.well-known/`; plus one per-IP rate-limit rule (30 requests / 10 s) | `worker.js`, `main.tf` |
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
   (gitignored). The guestbook variables come from the next section; leave `guestbook_open = false`
   until it is bootstrapped.
3. The service needs an image before its first revision starts: create the registry, push
   an image built from a release, then apply everything.

   ```bash
   terraform init
   terraform apply -target=google_project_service.enabled -target=google_artifact_registry_repository.demo
   ctx=$(mktemp -d); mkdir -p "$ctx/deploy/demo" "$ctx/demo"
   cp ../../deploy/demo/Dockerfile ../../deploy/demo/entrypoint.sh "$ctx/deploy/demo/"; cp -R ../../demo/astronomy ../../demo/replay.sh "$ctx/demo/"
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

## Bootstrap the guestbook (once, by the maintainer)

Until these steps are done the image serves astronomy alone (its tools then carry no `kb` argument),
and the Pages Atlas shows the live astronomy KB only.

1. Create a **private** repository named `guestbook` (the KB takes the remote's basename as its
   name, so the name is not free).
2. Push `demo/guestbook/` as its root: `git init` inside a copy of the folder, commit, add the
   remote, push `main`.
3. Create a fine-grained personal access token limited to that repository, with *Contents:
   read and write*, and an expiry you will remember.
4. In `terraform.tfvars` set `guestbook_remote` (the https URL), `guestbook_git_token` (the token),
   `guestbook_token` (`openssl rand -hex 24`, different from `demo_token`) and, optionally,
   `notify_url` / `notify_secret` (a `whsec_` + base64 secret) for a Standard Webhooks
   notification of each accepted message.
5. `terraform apply`: this creates the Secret Manager secret, the KV namespace and the AI binding.
   Roll out an image with the entrypoint (the next release, or `gh workflow run demo.yml -f tag=<tag>`).
6. Set `guestbook_open = true` and `terraform apply` again: the Worker now accepts messages.

## Guestbook moderation

- **Remove a message**: `git revert` its commit in the private repository, and push. The demo picks it
  up at its next pull (any request that reaches the guestbook) or cold start. A removal request comes as
  an issue on this repository naming the message id (`messages/<date>-<suffix>`).
- **Close the guestbook**: `guestbook_open = false` and `terraform apply`. It stays readable; the Worker
  refuses writes without a server deploy.
- **Rotate the git token before it expires**: create the new token, update `guestbook_git_token`,
  `terraform apply` (a new secret version; the next cold start reads it). An expired token leaves the
  guestbook unmounted at the next cold start and astronomy up; `demo.yml` warns when `tools/list` stops
  listing the guestbook.
- **Free-tier budget**. Nothing here needs a paid plan; each limit and what happens at it:

  | Resource | Free limit | How it is kept | When exceeded |
  |---|---|---|---|
  | Worker requests | 100k/day | Atlas assets on Pages; API responses cached at the edge; poll every 20 s on the Pages build | Cloudflare refuses until 00:00 UTC; the Pages Atlas falls back to the snapshot, MCP is unavailable |
  | Per-IP edge rate limit | 1 WAF rule on the free plan | The rule (30 requests / 10 s per IP) runs before the Worker, so blocked requests cost no Worker quota | 10 s block for that IP |
  | Cloud Run | monthly free tier, max 1 instance | Edge cache of 5 s on `GET /api/ui/v1/*`: all visitors share the read token, so Cloud Run sees about one request per URL per 5 s per Cloudflare location | Budget alert at 5 EUR (D751) |
  | KV writes | 1,000/day | Only message attempts write to KV: 2 keys per attempt, capped at 100 attempts a day | KV errors, so messages are refused |
  | Workers AI | daily free allocation | Only message attempts call it, same cap | The classifier errors, so messages are refused |

  A single determined attacker can still exhaust the daily Worker quota; the free plan accepts that, the
  failure is graceful and the next UTC day starts clean.

## Checks

- `make demo-smoke`: builds the image and runs it locally against a bare git repository seeded from
  `demo/guestbook/` (a Docker volume, `file://`, a dummy token): the audit log is signed, read tool answers, write tools refused with
  the read token on both KBs, a message written with the write token reaches the remote, the routed
  `tools/list` shows `kb` as a required enum (and no `kb` with astronomy alone), no token gets 401, and an
  unreachable remote leaves astronomy up. `DEMO_BASE=cartographer` after `make docker` tests the working tree.
- `make demo-worker-test`: the Worker's moderation logic under `node --test` (no Cloudflare needed).
- `make demo-tf-check`: `terraform fmt -check` and `validate` (no credentials).

## Rotating the tokens

Change `demo_token` (read) or `guestbook_token` (write) in `terraform.tfvars` and `terraform apply`:
a new Cloud Run revision (no rebuild) and the Worker's secret binding change together. Nothing else
carries them.

## Tearing down

`terraform destroy`, then delete the state bucket and the `GCP_*` repository variables (the
`demo` job then skips itself).
