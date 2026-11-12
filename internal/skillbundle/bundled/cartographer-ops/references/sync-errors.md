# Writes fail or a KB does not push

Read `sync_status` first (it is not in `tools/list`: call it by name, with the `kb` argument). Its
`state` and `last_error` say which of the three cases below applies. Report what you find; the
recovery is the user's to choose.

## `degraded`, and `branch` differs from `remote_default_branch`

The KB's clone is on a branch other than its canonical one, and Cartographer refuses to write
rather than fork the KB on the remote. Reads keep working. `branch_source` says where the
canonical branch comes from:

- `remote`: the remote's default branch.
- `settings`: the `branch` of the `settings.yaml` committed on the default branch.
- `server`: `kbs[].branch` of the server file.

A clean clone with nothing unpushed is switched back by the next sync on its own. When it is
not, `last_error` ends with why (uncommitted changes, N commits unpushed, branch never pushed),
names both branches and gives the clone path. Show it to the user. The ways out:

- merge the stray branch into the default branch on the remote (a pull or merge request), or
  check out the default branch in the clone;
- with `branch_source: settings`: check out the configured branch, or change `branch` on the
  default branch;
- with `server`: check it out, or change or remove `kbs[].branch`.

Then `cartographer service restart`. Never merge, rebase, push or delete a branch yourself.

## `failed`, the remote refused the push because the branch is protected

Cartographer pushes to the branch itself and never opens a pull request. The commit is safe in
the clone. The way out is the user's: unprotect the branch on the remote, or set `branch` in
`settings.yaml` on the default branch so writes go to another one. Do not retry in a loop: the
remote's answer will not change.

## `failed`, the remote refused the push because of who authored or committed

A membership or private-email rule: `last_error` names the identities. The branch is not the
problem.

1. Check that `git.identities.default` (or a `git.identities` entry for the KB) is set in the
   server file, and that the service was restarted. Commits made by the server identity are
   re-authored by the next sync.
2. Commits authored by a token need `cartographer kb reauthor <kb>` on the server host. Run it
   without `--apply` first and show the user the commits it would change. Run it with `--apply`
   only on their consent. It refuses anything already published.

Never rewrite those commits with git by hand.
