---
topic: deployment-release
---

# D68 — Deploy via Flux GitOps, `kubectl apply` removed from CI

**Context.** The `build-deploy.yaml` pipeline ended with a "Deploy to k3s" step
(`kubectl apply` + `rollout status`) using a long-lived kubeconfig (`secrets.KUBECONFIG_B64`,
service account `gitea-deployer`). But the cluster is GitOps: the Flux Kustomization
`flux-system/ai-tools` already reconciles `homelab-manifests` and applies the manifest bump made
by the previous step. The v1.5.0 release (run #41) made it evident: the imperative step
failed (`kubectl apply` → *"the server has asked for the client to provide credentials"*, the SA's
token no longer valid for the openapi download), marking the run **red**, while the real deploy
had already succeeded **via Flux**.

**Decision.** Removed the "Deploy to k3s" step: the pipeline stops at the manifest bump
(build → push image → release → commit to `homelab-manifests`); the deploy is delegated
entirely to Flux. This eliminates the redundancy and the conflict with Flux's drift control
(two actors applying the same Deployment), and removes the long-lived `KUBECONFIG_B64`
kubeconfig from the repo secrets (one less surface). **Discarded alternative:** regenerating the
`gitea-deployer` token — it would only have restored a deploy path that has no reason
to exist alongside Flux.
