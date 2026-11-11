---
topic: control-plane
---

# D1925 — A bug report's destination is a leaf package's pure function

**Decision.** `internal/feedback` is a leaf that Compass and Meridian can link (listed as a client package in `internal/boundaries`). `Destination` parses the three modes of a future `feedback.destination` setting: `github` (also empty), `off`, or an URL template that is `https://` (`http://` only to a loopback host), has a literal host without userinfo, contains `{title}` and uses no placeholder but `{kind}`, `{title}` and `{body}`. `SubmitURL` builds the prefilled URL for a report; past 6000 characters it carries the title only (cut rune by rune if the title alone is too long) and the caller prints the body for pasting. The repository slug is a constant that a test pins to `mkdocs.yml`'s `repo_url`.

**Why.** What a report is filed against and where it goes must be testable and shared by the client and the server, and nothing may send it: the user opens the URL and submits it. A template, not a free URL, keeps the host literal so a placeholder can never change where the report goes.

**Alternatives rejected.** The URL shapes in the skill's prose only (the status quo): no test can pin them to the repository. A destination parsed in each of the client and the server: two copies drift.

**Consequences.** A caller that gets `bodyInURL == false` must print the body itself. A custom template without `{body}` never carries the body. So far the package is not used by any command, setting or served text, and later changes of the series extend this file.
