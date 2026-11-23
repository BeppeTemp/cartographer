# AGENTS.md — the Atlas web app

The browser app of the Atlas (`web/`). Its check is `make web-test`.

- **State read for one KB is cleared when the KB changes**: data of the previous KB is never shown as final for the new one. A view that keeps a list, a selection or a loaded page across a KB switch shows another KB's content under this KB's name; reset it in the same place that reads the new KB, and test the switch.
- **Editing any file under `web/`, these `.md` files included, needs `make web`**: the embedded bundle's `SourceHash` covers every tracked file there, and `TestBundleMatchesSource` fails on a stale `internal/webui/dist/provenance.json`.
