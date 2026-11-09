---
topic: architecture
---

# D1856 — The Surveyor's packages, routes and wire fields are named surveyor

**Decision.** The Surveyor's Go packages (`internal/surveyor/...`, `internal/surveyorconfig`, `internal/surveyor/surveyortest`), files (`cmd/cartographer/surveyor*.go`, `uiapi_surveyor*.go`, `Surveyors.tsx`), identifiers, `settingschema.ScopeSurveyor` (`"surveyor"`), the routes `POST /api/surveyors`, `POST /api/surveyor-settings` and `GET /api/ui/v1/kbs/{kb}/surveyors`, and the wire fields `surveyor`, `surveyors` and `surveyor_template` (and the `field` of the two 400 answers) are renamed from `worker`. The old routes answer 404: no alias.

**Why.** The "What stays" paragraph of D1442 priced this rename as large churn for a name no user reads. In the 0.29 series the Surveyor becomes a documented component with a public contract, and a contract named `worker` beside a product named Surveyor would need a deprecation path as soon as it is documented. Nothing carrying the old names was released, so the cost is one change now, and it grows with every page that cites the old names.

**Alternatives rejected.** Dual routes or aliases: nothing released uses the old ones, so they would only be debt. A deprecation window: there is nothing to deprecate. Leaving the wire fields: a documented contract with a name foreign to the product.

**Consequences.** A dev-build Surveyor or Atlas that beats or reads the old routes gets 404, so the Surveyor and the server of this series must be the same build. `TestNoWorkerNameLeft` fails when the old word comes back into the Surveyor's packages, files or the Atlas API client. What stays: the config key `web.enabled` (released); the unrelated workers (the KB push goroutine, the demo's Cloudflare Worker, test-runner workers, the doctor skill's writer subagents); `cartographer worker` is still an unknown command; the files under `docs/decisions/` keep their text. This supersedes the "What stays" part of D1442, whose command, no-alias and gray-area rules stand.
