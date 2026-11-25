---
topic: control-plane
---

# D2378 — `dashboard_patch` and `dashboard_delete` are whole-KB writes of a KB-root file, guarded by the file's hash

**Decision.** `dashboard_patch {name, if_match, ops}` and `dashboard_delete {name, if_match}` are advanced tools of the whole-KB resource class, wrapped by `gitWrap` (one commit per call) and withheld from a token with `artifacts: r` like `artifact_write` (`artifactWriteTools`). `if_match` is the hash `dashboard_get` returned, `""` to create; a mismatch, `""` on an existing name and a hash on a missing name are `stale_write`, a delete of a missing name is `not_found`. The ops are decoded and applied by `kb.DecodeDashboardOps` and `kb.ApplyDashboardOps` (all or nothing, validated as a whole); the file is stored by `KB.WriteDashboard` and removed by `KB.RemoveDashboard`, which refuse a symlink and a non-regular file as the read does. A `note` text holding a credential-shaped string is refused with the widget, line and kind, never the value. The write gate (`gatedTool`) does not apply: a dashboard is no concept and cannot add a finding it judges.

**Why.** A dashboard holds free text and filters over the whole KB: who may edit it is who may edit the KB, so a caller that reads only part of it cannot write one, and neither a result nor an error then echoes an author string or a filter definition to it (the rule of D2377). The hash is the optimistic lock the artifact tools already use. Returning the resolved dashboard shows the agent what the user sees at once.

**Alternatives rejected.** A collection class with per-widget narrowing of the result: it would let a narrowed token change widgets it cannot read. Going through `artifact_write`: its path classifier refuses `dashboards/` on purpose, and it cannot apply ops or scan notes. A write-gate entry: nothing to judge.

**Consequences.** `dashboard_patch` and `dashboard_delete` join `advancedToolNames`. The tools have their own commit subject fields (`name`). The D337 revision stamp and the UI routes come with the next change.
