---
topic: transport-auth
---

# D17 — Multi-KB: routing via query parameter

`MultiKBServer` selects the KB via `?kb=<name>`. With a single KB the parameter is optional. With multiple KBs and no parameter → 400 error. Simpler than path-based routing.
