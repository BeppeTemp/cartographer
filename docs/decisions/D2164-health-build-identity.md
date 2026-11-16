---
topic: deployment-release
---

# D2164 — /health names the build, and a service is current only when version and build match

**Decision.** `/health` carries `build`, the identity of the answering executable: `vcs:<revision>`
when the binary embeds the VCS revision of a clean checkout (`debug.ReadBuildInfo`, `vcs.modified`
not `true`), `sha256:<hex>` of the executable file otherwise, and no key when neither is available.
It is part of the public body, next to `version` and `started_at`, and the key and the two prefixes
are stable. `upgrade-repair` and `service.Replace` take the service as current only when the version
satisfies the usual rule **and** the builds are equal; an empty identity on either side (a server that
predates the field) leaves the version alone to decide. `status` says `build skew` when the local
installed service reports this client's version and another build.

**Why.** A release candidate rebuilt the same day keeps its tag, so two different binaries report one
version, and `upgrade-repair` left the service on the old one while `status` showed nothing (#2140).
The version string names a release, not a build. The identity is computed once at `serve` start: a
hash taken after the file was replaced would describe the new file for the old process. Release builds
(goreleaser, from a clean checkout) carry the revision; a `make build` or a `go build` from a modified
tree carries none that is trustworthy, so it falls back to the file hash, which is also the only
identity a test binary has.

The build is compared only for a release version and a service whose definition launches this very
executable (`os.SameFile`, which follows symlinks); a dev client or another path falls back to the version.
Hashing costs one full read of the executable at `serve` start on a build without a revision.

**Alternatives rejected.** A build timestamp injected with `-ldflags`: every build path, goreleaser and
the Makefile and CI, would need it, and a forgotten one reads as equal. The revision alone: a modified
tree rebuilt twice reports one revision for two contents. Always hashing: costs a read of the whole
file at start for the common release case, and a revision is something a human can look up. Judging a
remote server by build in `status`: a server of the same release built for another platform, or the
container image, is a different file by nature, so only the local service is compared.

**Consequences.** `build` is a contract: it can be added to but not renamed or reshaped, and a value
is only comparable with one of the same prefix (a revision never equals a hash, so a switch between the
two kinds counts as a different build and costs one replacement). A `Replace` whose caller is not the
binary the service launches times out naming the observed and the expected build, where it used to
succeed on the version alone. `service restart --wait` still proves the version and a new process only.
