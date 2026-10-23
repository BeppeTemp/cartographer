# Security Policy

## Reporting a vulnerability

Please report suspected vulnerabilities **privately** via
[GitHub Security Advisories](https://github.com/BeppeTemp/cartographer/security/advisories/new)
("Report a vulnerability"). Do not open a public issue.

You can expect an acknowledgement on a best-effort basis (this is a personal
project with a single maintainer). Coordinated disclosure is appreciated:
please allow a reasonable window for a fix and a release before publishing
details.

## Scope

Cartographer is an MCP server that can be exposed over HTTP with bearer-token
authentication, scopes/RBAC, and an Ed25519-signed audit log. Reports of most
interest:

- authentication or scope bypass on the HTTP transport;
- cross-KB isolation failures in multi-KB mode (`?kb=` routing);
- path traversal or writes escaping the KB root;
- secret leakage (SOPS-managed values are referenced, never stored in
  plaintext — anything that surfaces plaintext secrets is a bug);
- audit-log integrity issues (hash chain, signature);
- supply-chain defects: a CI workflow that runs PR code with secrets, an installer
  or the release pipeline that does not verify what it downloads, a bundled skill
  that makes a user's agent send KB content elsewhere.

## What the maintainer runs

Every push to `main` and a weekly schedule run govulncheck, `npm audit` on the
`web/` production dependencies, actionlint, zizmor and CodeQL (`security.yml`);
Dependabot proposes dependency, action and base-image updates. A failed scan
opens a public issue that carries only a link to the run; a vulnerability that
is reachable in our own code is handled through a private advisory instead. No release
ships while a Dependabot or code scanning alert is open, or while a draft advisory
has a fix waiting to land (`CONTRIBUTING.md` §Releases).

## Verifying a release

Release assets and the container image carry GitHub artifact attestations (Sigstore,
keyless, tied to this repository's `release.yml`). With the `gh` CLI:

```bash
gh attestation verify <downloaded-asset> --repo BeppeTemp/cartographer
gh attestation verify oci://ghcr.io/beppetemp/cartographer:<tag> --repo BeppeTemp/cartographer
```

Each release also ships an SPDX SBOM per platform binary. `install.sh` and
`install.ps1` verify `sha256sums.txt` and refuse to install when it is missing;
that catches a corrupted download, the attestation catches a replaced one.

## Supported versions

Only the latest release is supported with security fixes.
