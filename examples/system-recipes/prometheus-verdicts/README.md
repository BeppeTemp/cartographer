# Verdict lister for a metrics endpoint

A recipe of `shape: system` that reports what a Prometheus HTTP API **concludes**, never what it measures
([worker-recipes.md](../../../docs/worker-recipes.md) §Verdicts, not metrics). A Python 3 script (standard library
only) and a file of checks; it is an example in the repository, not part of the binary.

Files: `lister.py` (the recipe's command), `checks.json` (the groups and signals, yours to replace), `instructions.md`
(the recipe's instruction text), `system-review.md` (the instructions of the periodic review), `recipe.yaml` (the
`recipes.<name>` and `tasks` blocks).

## What it does

- **Preflight.** It reads `/api/v1/status/flags` and answers `read_only: true` only if the endpoint reports its flags
  and none of `web.enable-admin-api`, `web.enable-lifecycle`, `web.enable-remote-write-receiver` or
  `web.enable-otlp-receiver` is on, and `/api/v1/rules` can be read. A compatible backend that does not serve the flags
  is refused: it needs its own question. The `identity` is the `identity` key of the checks file. This guards against a
  mistake, not against the script: bind a read-only token and read what it grants.
- **List.** One entry per group of `checks.json`, one line per alert rule and per signal:

  ```text
  alert ApiDown: ok
  alert HighErrorRate: bad
  error-ratio: watch
  ```

- **The scale** is `ok`, `watch`, `no data`, `bad`, best to worst, the same for every line. A firing rule is `bad`, a
  pending one `ok` (it is not a fact yet), a rule whose `health` is `err` `no data`. A name with several rules takes
  the worst. A query with an empty answer or `NaN` is `no data`.
- **Never emitted**: a sample, a timestamp, a count of firing series, a label of a series. A move inside a band changes
  no byte, so the group keeps its revision and the poll spends no token.
- **Every failure fails the whole listing and names what failed**: a query that errors, answers more than one series
  (aggregate it in the PromQL), or holds a non-number. A line left out would read as a change of the system.
- A redirect is a failure: `urllib` would repeat the token to the new host. It **never uses the request's
  `source.locator`**; the endpoint and the token come from the environment.

## The checks file

```json
{"identity": "metrics-viewer",
 "groups": [{"key": "service/api", "title": "API service", "alert_labels": {"service": "api"},
             "signals": [{"name": "error-ratio", "query": "...",
                          "bands": [{"below": 1.5, "verdict": "ok"}, {"below": 3, "verdict": "watch"}, {"verdict": "bad"}]}]}]}
```

| Key | Meaning |
|---|---|
| `identity` | Optional, the name the preflight reports. It is here and not in `exec.env` because the worker redacts those values from what a lister prints |
| `groups[].key`, `title` | The group, as in §System recipes: one responsibility a page owns, never a person |
| `groups[].alert_labels` | Optional: the alert rules whose labels hold all of these. A group with none matching prints `alerts: none` |
| `signals[].name`, `query` | The line's name and an instant query that answers one series |
| `signals[].bands` | Read in order: the first whose `below` exceeds the value decides, and the last has no `below`. `verdict` is `ok`, `watch` or `bad` |

The value can run either way: `days-to-full` in `checks.json` goes from `bad` up to `ok`. Compute at the source, in the
query, what a band must compare: a window against its baseline, a trend. Put a long window in a recording rule
(`sli:api_error_ratio:rate28d` in the example): a query over 28 days at every poll costs the server, not the worker.
Make bands wide and windows long: the lister keeps no state, so a band that flaps is one unit per flap.

## The periodic review

The lister has no memory and neither does a unit, so the review is a task of `kind: prompt`
([deployment.md](../../../docs/deployment.md) §Prompt tasks). `instructions.md` has every worsened line recorded in a
dated gap `gaps/verdict-<group>-<line>`; `system-review.md`, weekly, closes those whose newest line ends in `-> ok`,
and raises as a new gap what no single line shows: a threshold that flaps, a state nobody acted on, several groups
that moved together. It proposes no threshold and no fix, and it has no tool on the system.

## Try it

1. Create the map the gaps live in, `gaps` here, admitting `Contradiction`. Another name means changing it in
   `instructions.md` and `system-review.md`.
2. Put the script and the files where the worker can run them (a derived image with Python 3, or a mounted read-only
   volume), add `recipe.yaml` to the worker config with the two `.md` files in the config directory, and set
   `METRICS_URL` and `METRICS_TOKEN` in the worker's environment. Name a `model` your config declares.
3. Register the system once: `source_register` with `source_shape: system`, `recipe: metrics-verdicts` and
   `locator: metrics://production` (a label, nothing selects an endpoint from it), list the Source in an IngestPlan's
   `sources`, and set `feed_interval` to an hour or more with `concept_patch`.

The first poll creates one unit per group; later polls create units only for groups where a verdict changed.

This path is hours late by design. Paging stays in Alertmanager and the series stay in the metrics store.
