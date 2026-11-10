---
topic: deployment-release
---

# D1883 — The Helm chart deploys any subset of Meridian and Surveyors

**Decision.** The chart has a switch per component: `meridian.enabled` (default `true`; `false` renders Surveyors only, against an external Meridian at `meridian.url`) and `atlas.enabled` (default `true`; `false` sets `web.enabled: false` in Meridian's config, a key the chart then owns). A release must hold at least one of Meridian and a Surveyor. The Atlas is still never deployed alone (D1439): `atlas.enabled` only turns it off.

**Why.** A Surveyor runs wherever its perimeter is, and a cluster that only hosts Surveyors had to carry a Meridian it did not use, or hand-write the Deployments. A Surveyor-only release needs the address of a Meridian it does not render, so the address is a value (`meridian.url`) and the token stays a reference to the entry's own Secret (`existingSecret`, `token_env`): the chart still renders no Secret object. An `http://` URL injects `allow_insecure_http: true` and sends the token in clear; the notes say so. The cost is a values schema with a conditional shape: a Meridian setting the chart can tell is set is refused when `meridian.enabled` is false (ingress, tokens, claim, `config.kbs`, `config.auth`, `extraEnvFrom`, credentials), while one it cannot tell from its default (image, probes, resources) is ignored, and no test can see that.

**Alternatives rejected.** A second chart for Surveyors: two charts to version, publish and keep at the same tag, for objects the existing templates already render. Keeping Meridian always rendered and scaling it to zero: a `replicas` value is refused by construction (D1441) and a Service and claim would stay. A standalone `atlas` component: a frontend with no server is not a supported topology (D1439). Reading the Meridian address from the in-cluster Service only: an external Meridian would be unreachable.

**Consequences.** This amends D1441, whose chart "always renders the server"; one replica by construction still holds whenever Meridian is rendered. It builds on D1867 (component names). The chart's CI values files must end in `-values.yaml` or the loop of `test/helm/run.sh` skips them, and `check_release` is split between the shapes so that a Surveyors-only release is not asserted to hold a Meridian. Both switches ship with the chart's first publication, with 0.29; there is nothing to migrate.
