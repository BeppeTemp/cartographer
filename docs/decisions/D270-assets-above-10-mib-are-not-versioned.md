---
topic: data-plane
---

# D270 — Assets above 10 MiB are not versioned, and the asset listing survives stray files

**Decision.** `kb.AssetMaxFileSize` is a fixed 10 MiB and means "the largest file still worth versioning in the KB's git history": `asset_write` refuses a larger file, `asset_read` will not return one. `ListAssets` never fails on a file it can describe: hidden files and directories are skipped (they are not assets), and a file above the cap is listed with `oversized: true`, hashed by streaming, and stays deletable. Lint reports it as `oversized_asset` (warning); an owner whose assets cannot be listed at all is one `unlistable_assets` finding, not an aborted run. `concept_collapse` refuses when the directory holds anything besides `index.md`.

**Why.** The old 1 MiB cap was a transport budget, and it was enforced on list as well as write. Assets reach a KB through git too — a push, an import — so one 2 MiB PDF or a `.DS_Store` in any expanded concept made `lint` fail on the whole KB and left the file impossible to list or delete through the API. The stdio transport decodes with `json.Decoder` and has no per-line cap, so the size limit is free to express a storage policy instead. The cost: an agent can now move 10 MiB (about 13 MiB of base64) in a single call.

**Alternatives rejected.**
- A configurable cap (per server or per KB): one more knob for a value nobody has needed to tune; a constant can become a setting later without changing the contract.
- Git LFS for large assets: adds a server and client dependency for a KB that is meant to hold text and small evidence.
- Keeping the listing strict and fixing only lint: `concept_delete` and `concept_collapse` depend on the same listing and would still be blocked by one file.

**Consequences.** Path-safety rules on API input are unchanged: a hidden, escaping, `.md` or symlinked path is still refused by read, write and delete. A symlink or special file inside an expanded concept still makes `ListAssets` fail — deliberately, since it is not safe to describe — and lint turns that into a finding. Files larger than the cap are expected to live outside the KB and be cited by link.
