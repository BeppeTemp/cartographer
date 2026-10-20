---
topic: deployment-release
---

# D905 — One credentials directory holds every secret file, and the audit log is always on and signed

**Decision.** The server reads every secret from files in one directory, `credentials_dir` (env
`CARTOGRAPHER_CREDENTIALS_DIR`, default `<data>/credentials`), found by the KB's resolved name. There are four
lookup rules. Per KB with a default: `<kb>.ssh`, `<kb>.token` and `<kb>.age`, else `default.ssh`,
`default.token` and `default.age`. Per KB only: `<kb>.signing`, since a signature names its KB. One shared
`known_hosts`. The server's `audit.key`. They replace ten settings: `git.ssh_key`, `kbs[].ssh_key`,
`git.known_hosts`, `kbs[].known_hosts`, `git.token_dir`, `sops.age_key_file`, `sops.age_key_dir`,
`kbs[].sops_age_key_file`, `kbs[].artifact_signing_seed` and `audit.key_seed`, with their environment variables.
A missing file behaves as the setting being unset did. An explicit `GIT_SSH_COMMAND` still wins over
`default.ssh`, and a KB's own `<kb>.ssh` still wins over the environment (D46).

The audit log is always on: `<data>/audit/audit.log`, which `audit.log` only relocates. It is signed with
`audit.key`, which the first start generates (`0600`, never replacing a file) when it is missing.
`audit.max_segment_bytes` defaults to 64 MiB, so retention has segments to delete. When the key can be neither
read nor generated (a read-only directory without it), `best_effort` starts with an unsigned log and a warning
naming the file to add, and `required` does not start. A log that cannot be opened does the same. A log kept
unsigned before the upgrade is signed from then on: `audit verify` accepts its unsigned entries before the
first signature and the first checkpoint written with the key, which the hash chain authenticates, and still
refuses an unsigned entry after either.

The choices the plan left to the implementation:
- **A session on `--kb` folders without data keeps neither.** When `data` is not configured (YAML or
  environment) and every mounted KB comes from `--kb` / `CARTOGRAPHER_KB` (no `kbs[]` from the file), there is
  no default credentials directory and no default audit log, because `DefaultDataDir` belongs to the native
  service and a dev or test session would collide with its files and lock. The audit log then runs only when
  `audit.log` is set, credentials are read only from `credentials_dir` when it is set, and the start logs
  `audit log: off`. `audit.mode: required` without `audit.log` does not start. A server with `data`, or with
  `kbs[]`, is unchanged. `Config.FolderSession` decides it; `CredentialsPath` and `AuditLogPath` return `""`.
- **A restart waits for the previous writer.** The log has one writer (a lock beside it). Opening it retries
  the lock for up to 10 seconds before reporting it held, so a restart whose predecessor is still exiting
  gets the log; a holder that outlasts the wait is treated as any log that cannot be opened.
- **The directory may be read-only.** The server writes into it only to generate `audit.key` and to place the
  secrets of the upgrade. A secret it cannot place is listed for the operator, and a key it cannot generate
  follows the audit mode above.
- **Permissions are warned about, never refused**, once per file per process. A file other users can read is
  reported, except `known_hosts`, whose host keys are public. A file its group can read is reported only when the server's user owns it. This narrows the plan's
  "group or others" (see Why).
- **The directory must not be in a KB.** A KB commits its whole working tree (`git add -A`), so a
  `credentials_dir` that holds `data/index.md` or lies below a directory that does, inside a `kbs[].path` or
  a remote KB's clone, or below a subdirectory of `data` (a discovered KB) refuses the start, whether that KB
  exists yet or not. The credentials directory, `credentials` and `audit` under `data` are never discovered
  as KBs.
  The same check covers the directory of the audit log and `audit.archive_dir`, so a KB named `audit` cannot
  hold the log; the error names the setting to move.
- **The defaults are not tied to a host.** `default.token`, `default.ssh` and `default.age` are offered to every
  KB without its own file, whatever its remote host. A KB whose remote is on another host should get its own
  `<kb>.token` (or `.ssh`), so the default credential is not presented to a host it was not made for.
- **The upgrade copies, never moves.** The first start copies each removed setting's file, or writes its inline
  value (a `${NAME}` resolved), to its name in the directory, `0600`, never over an existing file; every
  `known_hosts` goes into the one file. The report names settings and files, never a value. What cannot be
  written is listed as the file to create, and the server YAML is not realigned (D903) until every secret is in
  place, so the next start still finds them. In a KB's `settings.yaml` the four per-KB keys are removed settings
  (`kb.SettingsRetired`).

Revises D18 and D325 (signing was opt-in and the log was off without `audit.log`), D39 (the bootstrap SSH key),
D46 (the per-KB SSH key is `<kb>.ssh`), D47 (the per-KB age key is `<kb>.age`) and D53 (`<git.token_dir>/<name>.token`
and `<sops.age_key_dir>/<name>.age` become `<name>.token` and `<name>.age` in the one directory).

**Why.** Ten settings on two or three levels each pointed at one secret file, and a deployment wired them one by
one. On Kubernetes that meant one mount per secret and a ConfigMap full of paths. A directory found by name needs
neither: one Secret mounted once, and a new KB needs only its files. The cost is that a file's name is now a
contract. Renaming a KB renames its files, and a typo in a name is a missing credential, not a load error, which
is why a failed clone names the file it looked for (`credentialHint`).

An audit trail that is off by default is missing exactly when it is needed. Signing was opt-in because it needed
a secret the server could not invent (D325), but the server can generate one, and it does so once, in the
directory it already reads. The cost: a log is now written on every installation, and a key exists that must be
backed up with the log, since a new key signs with a different public key.

The permission rule follows Kubernetes. The image does not run as root. The kubelet makes a Secret's files
root-owned, and the only way the server reads them without other users reading them too is the pod's `fsGroup`.
With `fsGroup` the files become the group's and the kubelet adds the group read bit to `defaultMode`: `0400`
becomes `0440` (`pkg/volume/volume_linux.go`, `changeFilePermission`). The plan's `defaultMode: 0400` without
`fsGroup` gives the server a file it cannot read, and with `fsGroup` its "group or others" warning would fire at
every start of a correct deployment, with advice that cannot be followed. A file another user owns
and shares with the server's group is access the platform granted. A group-readable file the server owns (the
`audit.key` it generated, a copy made for it) is not. OpenSSH draws the same line for a private key
(`sshkey_perm_ok` checks the mode only of a key the user owns); this rule keeps it for the group bit and stays
stricter for other users. The documented pod sets `fsGroup` and
`defaultMode: 0440`, and `audit.key` is a key of the Secret, because a read-only mount cannot get a generated one.

**Alternatives rejected.**
- Keeping the settings and adding a directory beside them: two homes per secret, against one home per key (P2).
- A directory per kind (`ssh/`, `tokens/`, `age/`): one Secret mount would need `items` remapping, and `known_hosts`
  and `audit.key` fit none of them.
- `default.signing`: one key would sign several KBs, and a signature would no longer say which KB it vouches for.
- Refusing a too-open file: a Kubernetes Secret is `0644` unless `defaultMode` says otherwise, so the default
  would not start. A warning tells the operator without taking the server down.
- Warning on any group or other read bit (the plan): it fires at every start of the documented pod and advises a
  mode the server cannot read.
- `defaultMode: 0400` without `fsGroup` (the plan's example): the file is root's, and the server, which is not
  root, cannot read its own key.
- Leaving `audit.key` to be generated on Kubernetes: a Secret mount is read-only, and a key generated into a
  writable volume instead would be a second home for it.
- Moving the old files: the upgrade must not break the previous version, which a rollback still runs with them.

**Consequences.** Breaking: the ten settings are removed. The upgrade places their secrets where it can and lists
the rest. A Kubernetes deployment restructures its Secrets into one mounted at `credentials_dir`, with
`fsGroup`. Every installation now writes a signed audit log and holds `audit.key`. The demo deployment's
Cloud Run secret mount is read-only and holds one file, so its entrypoint copies the token into the default
directory, which stays writable for `audit.key`.

A future change here must preserve all of these:
- no secret value in argv, a log line, an error or the upgrade report; the token stays behind the
  `credential.helper` that reads the file;
- sops stays hermetic (D260): the age key reaches it only as `SOPS_AGE_KEY_FILE`, the resolved path;
- a missing file behaves as the setting being unset;
- a file of the directory is never replaced, and generated or copied files are `0600` in a `0700` directory;
- a group-readable file another user owns is not reported, and any file other users can read is.
