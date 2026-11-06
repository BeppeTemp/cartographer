---
topic: client-configurator
---

# D1721 — setup classifies SSH failures from stderr, agent and key files

**Decision.** When the setup preflight's `git ls-remote` fails on an SSH remote with an authentication error, `internal/gitx` separates three cases from git's stderr, `ssh-add -l` (exit 1: agent without identities, exit 2: no agent) and the default key files `$HOME/.ssh/id_{ed25519,ecdsa,rsa}`: no key, a key the forge does not know, a passphrase-protected key no agent holds. The remedy line prints the commands and the forge's key page; setup never creates or uploads a key.

**Why.** The generic "check the key loaded in your agent" assumes the user knows what a key is. The probes are cheap, read-only and injectable. The key page comes from a host heuristic (github.com, a host containing `gitlab`, a host containing `gitea` or codeberg.org); an unknown host gets the instruction without a URL.

**Alternatives rejected.** Generating a key for the user: same principle as host keys (D173). Asking the forge API: needs a token setup does not have.

Identity files are resolved as ssh would for the host (`ssh -G`, plus the default names). When `GIT_SSH_COMMAND`, `GIT_SSH` or `core.sshCommand` is set, the key git offers is unknowable and the remedy gives the three possibilities instead of claiming "no key".

**Consequences.** A key present with no agent identity is ambiguous (passphrase or unknown key): the remedy words both possibilities and gives both fixes. The passphrase text is per OS (macOS keychain, Linux ssh-agent, Windows OpenSSH agent service plus `core.sshCommand`).
