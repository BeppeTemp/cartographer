---
topic: deployment-release
---

# D1022 — Releases carry keyless build provenance and SBOMs, and the installer never skips the manifest

**Decision.** `release.yml` attests what it publishes with `actions/attest-build-provenance`
(GitHub artifact attestations, Sigstore, keyless through the job's OIDC identity): the
`goreleaser` job attests every asset listed in `sha256sums.txt` and the file itself, the
`docker` job attests the image digest and pushes the attestation to ghcr. The image is
built with `provenance: mode=max` and `sbom: true`; GoReleaser publishes one SPDX SBOM
per platform binary (`sboms:`, syft installed at a pinned version and archive sha256).
`install.sh` fails when `sha256sums.txt` cannot be downloaded, as `install.ps1` already
did.

**Why.** `sha256sums.txt` is published by the same job as the binaries: whoever can
replace an asset can replace the manifest with it, so it proves integrity, not origin.
An attestation ties a file to this repository's workflow and commit, and needs no
long-lived signing key to guard. In `install.sh` a failed manifest request installed
unverified, so dropping that one request was enough to skip verification; the premise
that older tags have no manifest is false (every release since v0.1.0 ships one), so
there is no boundary to keep and no reason to tolerate its absence.

**Alternatives rejected.**
- A long-lived signing key (cosign key pair, GPG): a secret to store, rotate and lose.
- Making the installers verify attestations: requires `gh` on every machine that installs; the checksum stays the installers' contract and attestation is for whoever has `gh`.
- A version boundary below which a missing manifest is tolerated: no such release exists.
- Cataloging the archives (`artifacts: archive`) for the SBOMs: it skips the raw darwin/linux assets, which are not archives.
- Attesting the image in `preview.yml`: previews are not releases and nothing verifies them.

**Consequences.** New release assets (`cartographer_<version>_<os>_<arch>.sbom.json`)
and attestations in the repository's attestation store; no existing asset is renamed
(`test/install/goreleaser_guard.sh` pins the names and that the SBOMs exist). The jobs
that attest hold `id-token: write` and `attestations: write`, so a new step in them is
reviewed as one that can mint an identity for this repository. A bump of syft is an
edit of its version and sha256. `docs/deployment.md` §Verifying a release carries the
commands.
