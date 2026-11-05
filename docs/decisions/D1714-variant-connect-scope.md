---
topic: client-configurator
---

# D1714 — A discovered client variant is connected with its declaring KB only

**Decision.** `setup`, `connect` and `sync` bind a client variant to the KB that declared it (the cache's `source_kb`), narrowed by the operator's `--kb` selection when there is one, and never to every mounted KB (`variantKBs`). `connect` and `setup` connect each variant on its own (`doConnectScoped`) so one shared `--kb` does not widen it; a variant the selection leaves out is reported and not connected. `sync` connects only variants newly added to the cache by that run (`variantSyncResult.Added`), so a variant the operator disconnected stays disconnected. `setup --approve-variants` and `connect --agents <base> --approve-variants` run discovery and add the variants they approved to the agents they connect. Discovery (`discoverVariants`) pulls only the selected KBs and prints why it found nothing.

**Why.** A connection hands the client the KB's tools and its token scope. A variant is declared by one KB, which asked for it and was approved for it; binding it to every mounted KB would give a KB the operator never chose to that client, and with two or more KBs the default "choose with --kb" would make the automatic connect fail instead.

**Alternatives rejected.** Every mounted KB (the default of `connect`): over-exposure. Failing with "choose with --kb": an unattended `sync` could never connect anything. Connecting every cached, installed variant on each sync: undoes a deliberate `disconnect`.

**Consequences.** A variant declared by two KBs is registered from neither (D1698), so it has no single scope. Widening a variant's binding is `cartographer connect <name> --kb <kb>` or `client bind`, an explicit act.
