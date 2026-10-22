---
topic: project-governance
---

# D996 — Forge rules are tested where they bite

**Decision.** A forge's server-side behaviour is exercised at three depths, none of
which contacts a network service on an ordinary run. The push rules of a forge
(a membership check on the author and the committer, a protected branch) are one
POSIX-sh `pre-receive` hook, `test/forge/pre-receive.sh`, configured by files in
the bare repository's `hooks/` and shared by the Go tests and the e2e scenarios.
Authentication runs against the real `git http-backend` behind basic auth, in
process (`internal/gitx/gitxtest`). The real forges, Forgejo and GitLab CE in
containers (`test/forge/run.sh`), run only on the release PR, on a PR that touches
their own files, and on `workflow_dispatch`: never on `main` and never on an
ordinary PR. `smoke-http` and `e2e` run with no git identity on the host: the
scripts isolate the git config themselves.

**Why.** 0.25.0 could not publish to a forge with a membership push rule, and every
git test in the repository pushed to a local bare repository over a path, so none
of them could have seen it. A global `user.name` in CI hid the other half: a host
with no git identity. One hook file rather than strings pasted in tests keeps the
rule, and what "member" means, in one place that also runs under the Windows
shards (git runs hooks with its bundled sh).

**Alternatives rejected.** Real forges on every PR: GitLab CE boots in minutes, so
every PR would pay it for a change that cannot touch a forge. An in-process fake of
a forge's API: it would encode our idea of the forge, which is the thing the test
exists to check. Docker service containers declared in the workflow: the same
pinned images are started by `run.sh` itself, so one script serves CI and a
developer's machine, and the versions live in one place.

**Consequences.** A new forge rule is a mode of `pre-receive.sh`, not a new inline
hook. The isolated git config of `test/e2e/run.sh` and `test/smoke/http.sh` means a
scenario that commits by hand passes its identity with `-c`. The `forge` filter of
`scripts/ci-filters.sh` is pinned by its table test. The real-forge job is not a
required check: it is evidence on the release PR, read by the maintainer.
