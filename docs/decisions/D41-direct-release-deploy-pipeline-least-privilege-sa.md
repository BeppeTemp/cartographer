---
topic: deployment-release
---

# D41 — Direct release+deploy pipeline (least-privilege SA) + `install.sh` without Homebrew

**Decision.** `.gitea/workflows/build-deploy.yaml` runs on `v*` tags: vet+test → build+push
Docker image → cross-compile client (`darwin/linux` × `amd64/arm64`) + checksums → Gitea
release → tag bump in `homelab-manifests` → `kubectl apply`+`rollout status` with a kubeconfig of
a least-privilege service account (get/patch on the cartographer `Deployment` only). Client
installation via `install.sh` (POSIX `sh`, detects OS/arch, downloads from the latest release, verifies
checksums).
**Rationale.** A direct deploy (without ArgoCD/Flux) is proportionate to the homelab's scale and
limits the blast radius of a compromised CI token to a single Deployment. `install.sh` without
Homebrew avoids maintaining a formula/tap for a project with limited distribution.
Details: `docs/deployment.md` §CI/CD: release + deploy pipeline, §Client installation.
