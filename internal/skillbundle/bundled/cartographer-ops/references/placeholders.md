# A `{{repo:…}}` or `{{path:…}}` placeholder does not resolve

A sync warns about it. A KB's pages cite local paths as placeholders, and only this machine knows
where they are.

1. `cartographer paths list` shows each key, the KBs citing it, what the KB's `paths.yaml` says it
   points at, and its declared default.
2. A key resolves, in this order, from:
   1. `placeholders.paths` in `.cartographer.yaml`;
   2. for a repo key, a clone found under `placeholders.search_roots`, matched by the declared
      `remote` when there is one;
   3. the key's declared default in `paths.yaml`, if it exists on this machine.
3. Fix it with `cartographer paths set <kind>:<key> <path>`. For a repo, a closer
   `placeholders.search_roots` entry also works: the scan goes 4 levels down from each root, and
   the roots are `~/Documents`, `~/src`, `~/code` and `~/projects` unless changed. Then
   `cartographer sync`.
4. A key missing from the KB's `paths.yaml` is fixed in the KB, not on the client: declare it
   there with a `~/`-anchored default, so every machine resolves it with no configuration.
