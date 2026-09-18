---
topic: deployment-release
---

# D221 — the Kubernetes upgrade step ends on a verified image, not on a rollout

**Decision.** In the `cartographer-ops` bundled skill, the Kubernetes upgrade
step's success condition is an observation, not an action: after pushing the new
image tag, read the Deployment's container image back and check it is the new tag
*before* waiting for rollout, then confirm with the version the server reports
(`cartographer status` prints `server vX`). The step stays controller-agnostic —
"the GitOps controller that owns the manifests repo, if there is one" — because
the skill ships to every user, while only `docs/deployment.md` describes the
maintainer's Flux-reconciled cluster (D68).

**Why.** Where the manifest is applied by a GitOps controller rather than by the
operator, editing and pushing it does not change the Deployment; the controller
does, on its own reconcile interval. `kubectl rollout status` run in between
returns success in under a second, truthfully, about the old ReplicaSet, which has
nothing left to roll out. An agent following the skill literally — and it has no
other source for this procedure — reports the upgrade done while the previous
version is still serving. That is the project's own deployment model, so the
uncovered case is the default one and its failure mode is a silent false positive.
The cost is a three-line sub-list where the sibling bullets are one line each.

**Alternatives rejected.**

- *Leave the bullet and document the caveat in `docs/deployment.md` only* — the
  agent loads the skill, not the repository docs; the gap would stay exactly where
  it bites.
- *Name Flux (or Argo CD) in the skill* — the skill is bundled in the binary and
  ships to every user, most of whom do not run the maintainer's controller. A
  product name there is wrong for them and stale for us. A `bundle_test.go`
  assertion now fails if one reappears.
- *Force a reconcile as part of the step* — the command differs per controller and
  needs credentials the operator may not have. Verifying is universal; forcing is
  not.
- *Replace `rollout status` with the image check* — the rollout wait is still the
  step that ends when the new pods are actually serving. The image check is a
  precondition for it being meaningful, not a substitute.

**Consequences.** A Kubernetes upgrade is not done until the Deployment's image
has been read back and the server reports the new version. `internal/skillbundle/bundle_test.go`
pins both halves — the image verification must stay in the step, and no controller
product name may enter the skill — so a future shortening of the bullet fails the
build instead of silently restoring the false positive. Any future upgrade step
added to that list has to end on something the agent can observe, not on a command
it can run.
