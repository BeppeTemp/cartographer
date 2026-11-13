---
topic: deployment-release
---

# D2038 — The minimum supported git is 2.43

**Decision.** Cartographer supports git 2.43 and later, on the server and on the client. Every git call that takes a revision from outside ends its options with `--end-of-options` where the git of that version accepts it; `git reset --hard` does not, so `ResetHardTo` refuses a ref that starts with `-` and passes `<ref> --`.

**Why.** The documented floor was 2.24 (the release that introduced `--end-of-options`), but it was never tested: the lander's gate runs git 2.43 (Ubuntu 24.04), where `git reset --hard --end-of-options <ref>` fails with "option --end-of-options must come before non-option arguments" (later git versions accept it). Raising the floor to the oldest git the gate exercises makes the documented contract the tested one. 2.43 is also the git of the oldest distribution the images and the Windows/WSL setup target.

**Alternatives rejected.** Keeping 2.24 and testing it: needs a second git in CI for a version no supported platform ships. Dropping `--end-of-options` on every call: loses the argument-injection protection where git does accept it.

**Consequences.** A new git call that takes a user-controlled revision is checked against git 2.43 (the gate does it) before it uses `--end-of-options`; on `reset` the ref is validated instead. Failure paths that rely on git's message text (the index.lock refusal) must not depend on it alone: 2.43 prints it for some commands only, so the hint also looks up the repository's own `index.lock`.
