---
topic: data-plane
---

# D241 — The link graph is a stat-validated in-memory cache

**Decision.** The link graph is still derived from the concept files (D108),
but the per-file parse results are cached in memory and every graph read
validates them against the files instead of re-reading all of them. A file is
re-parsed when its size, modification time or mode changed, when its
modification time is within 2 s of when it was observed (git's "racily clean"
rule), when an extensionless link's asset lookup now answers differently, or
when it is a symlink. Readers get an immutable view, built only when something
changed. Links, the content hash and four frontmatter facets are cached, never
bodies, and nothing is persisted. The policy's per-concept type lookup reads
the same cache for one file at a time. `graph_neighbors` gains a `missing`
flag on link targets that are not concepts.

**Why.** Every graph access walked and regex-parsed the whole KB, and several
callers did it once per concept: `lint` with `scope_neighbors` (one walk per
concept in scope), the Atlas concept panel (two walks per click) and every
`Visible` check for a principal with a type selector (one read and parse per
concept). Validation keeps D108's guarantee without depending on every write
path to notify: the existing notification was incomplete by construction
(`OnSyncIn` only with SQLite, `supersede` touching no index, operators editing
files directly). Measured on the synthetic 1,000-concept, ~15 MB KB in
`internal/kb/graphcache_bench_test.go` (Apple M-series, `-benchtime 3x`):

| | before | after |
|---|---|---|
| `GraphNeighbors`, first call | 32 ms | 45 ms |
| `GraphNeighbors`, warm | 31 ms | 2.6 ms |
| `GraphSnapshot`, warm | 32 ms | 5.1 ms |
| `lint`, 111-concept scope, `scope_neighbors` | 3.75 s | 0.36 s |

The cost is a slower first read (hashes and asset probes are recorded) and a
cache whose correctness rests on the validation rules, so each rule has a test
that fails when it is removed.

**Alternatives rejected.**
- Invalidation hooks on the write paths: incomplete by construction, and blind
  to git pulls and editors.
- Persisting a link index: a second source of truth to reconcile on disk.
- An opt-out setting: a second code path; the equivalence test is the safety
  net instead.
- Size and mtime only: a `chmod` changes neither, yet decides whether the
  uncached walk can read the file. The equivalence test found this.

**Consequences.** Every existing graph API returns exactly what it returned
before: `graphcache_oracle_test.go` keeps verbatim copies of the uncached
readers and `graphcache_test.go` compares them after each step of a mutation
script. The policy change is proved the same way in
`policy_facets_test.go`. `ConceptFacets` resolves a file as `ReadConcept`
does (the direct form wins over an expanded `index.md`) and never runs a full
validation. A file rewritten with its size, mode and an old modification time
restored is not detected until one of them changes; git has the same limit.
The maps `Links` and `IncomingLinks` return are shared and must never be
mutated.
