#!/usr/bin/env python3
"""A system lister that reports verdicts, not metrics, from a Prometheus HTTP API.

It speaks protocol v1 of the worker's exec recipes (docs/worker-recipes.md):
one JSON request on stdin, mode "preflight" or "list", the answer on stdout.
The computing happens at the source, in PromQL; this script only turns each
answer into one word of a fixed scale. A value that moves inside its band
changes nothing, so the group keeps its revision and the poll spends no token.
A value, a timestamp or a count never leaves this script, and it never uses the
request's source.locator to choose an endpoint, a file or a query.

Usage: lister.py <checks.json>   (the recipe's exec.args name the file)

Environment (all supplied by the recipe's exec.env):
  METRICS_URL       the base URL of the API
  METRICS_TOKEN     optional, a bearer token
"""
import fnmatch
import http.client
import json
import math
import os
import sys
import urllib.error
import urllib.parse
import urllib.request

# The scale, best to worst: the worker's model reads a change of word and needs no
# threshold to say which way it went. A rule that cannot be evaluated hides its
# state, so it is worse than a warning and better than an alert that fired.
SCALE = ("ok", "watch", "no data", "bad")
BANDED = ("ok", "watch", "bad")  # "no data" is what a query answers, never a band
# Under these flags the endpoint itself takes writes, whatever the identity.
WRITE_FLAGS = ("web.enable-admin-api", "web.enable-lifecycle", "web.enable-remote-write-receiver",
               "web.enable-otlp-receiver")


class Failure(Exception):
    """A short message for stderr and a non-zero exit. Never a credential."""


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None  # urllib would repeat the Authorization header to the new host


OPENER = urllib.request.build_opener(NoRedirect)


def api(path, params=None):
    """Returns the data of a successful answer; anything else is a Failure, because
    a listing with a line missing would read as a change of the system."""
    base = os.environ.get("METRICS_URL", "").rstrip("/")
    if not base:
        raise Failure("METRICS_URL is not set")
    url = base + path + ("?" + urllib.parse.urlencode(params) if params else "")
    headers = {"Accept": "application/json"}
    if os.environ.get("METRICS_TOKEN"):
        headers["Authorization"] = "Bearer " + os.environ["METRICS_TOKEN"]
    try:
        with OPENER.open(urllib.request.Request(url, headers=headers), timeout=60) as resp:
            answer = json.load(resp)
    except urllib.error.HTTPError as e:
        raise Failure("%s answered HTTP %d" % (path, e.code))
    except (OSError, ValueError, http.client.HTTPException):
        raise Failure("%s gave no usable answer" % path)
    if not isinstance(answer, dict) or answer.get("status") != "success" or not isinstance(answer.get("data"), (dict, list)):
        raise Failure("%s did not answer success" % path)
    return answer["data"]


def check_signal(key, signal):
    name = signal.get("name") if isinstance(signal, dict) else None
    if not isinstance(name, str) or not name or not isinstance(signal.get("query"), str):
        raise Failure("group %s: a signal needs a name and a query" % key)
    bands = signal.get("bands")
    if not isinstance(bands, list) or not bands or not all(isinstance(b, dict) and b.get("verdict") in BANDED for b in bands):
        raise Failure("signal %s/%s: every band needs a verdict among %s" % (key, name, ", ".join(BANDED)))
    edges = [b.get("below") for b in bands[:-1]]
    if "below" in bands[-1] or not all(isinstance(e, (int, float)) and not isinstance(e, bool) for e in edges) or edges != sorted(edges):
        raise Failure("signal %s/%s: bands need ascending below edges and a last band without one" % (key, name))


def load_checks(path):
    try:
        with open(path, encoding="utf-8") as f:
            doc = json.load(f)
        groups = doc["groups"]
    except (OSError, ValueError, KeyError, TypeError):
        raise Failure("cannot read the checks file")
    if not isinstance(groups, list) or not all(isinstance(g, dict) and isinstance(g.get("key"), str) for g in groups):
        raise Failure("the checks file needs a list of groups with a key")
    for group in groups:
        if "alert_labels" not in group and not group.get("signals"):
            raise Failure("group %s has neither alert_labels nor signals" % group["key"])
        for signal in group.get("signals") or []:
            check_signal(group["key"], signal)
    identity = doc.get("identity")  # here, not in exec.env: the runner redacts those values from what is printed
    return groups, identity if isinstance(identity, str) and identity else "anonymous"


def reading(key, signal):
    """The verdict of one signal. A query must answer one series: a second one would
    leave the choice to the order of an answer, so it is the definition's error."""
    data = api("/api/v1/query", {"query": signal["query"]})
    label = "%s/%s" % (key, signal["name"])
    series = data.get("result") if isinstance(data, dict) and data.get("resultType") == "vector" else None
    if series is None:
        raise Failure("signal %s did not answer an instant vector" % label)
    if len(series) > 1:
        raise Failure("signal %s answered %d series: aggregate it in the query" % (label, len(series)))
    if not series:
        return "no data"
    try:
        value = float(series[0]["value"][1])
    except (KeyError, IndexError, TypeError, ValueError):
        raise Failure("signal %s answered a sample that is not a number" % label)
    if math.isnan(value):
        return "no data"
    for band in signal["bands"]:
        if "below" not in band or value < band["below"]:
            return band["verdict"]
    return signal["bands"][-1]["verdict"]


def rule_verdict(rule):
    if rule.get("health") == "err":
        return "no data"
    return "bad" if rule.get("state") == "firing" else "ok"  # pending is not a fact yet


def alerts(rule_groups, labels):
    """The worst verdict per alert name: one name often has a rule per severity."""
    found = {}
    for group in rule_groups:
        for rule in group.get("rules") or []:
            if rule.get("type") == "alerting" and rule.get("name") and all((rule.get("labels") or {}).get(k) == v for k, v in labels.items()):
                worst = max(found.get(rule["name"], "ok"), rule_verdict(rule), key=SCALE.index)
                found[rule["name"]] = worst
    return found


def content(group, rule_groups):
    lines = []
    if "alert_labels" in group:
        found = alerts(rule_groups, group["alert_labels"])
        lines += ["alert %s: %s" % (n, found[n]) for n in sorted(found)] or ["alerts: none"]
    lines += ["%s: %s" % (s["name"], reading(group["key"], s)) for s in group.get("signals") or []]
    return "".join(line + "\n" for line in lines)


def listing(req, checks):
    include = req.get("source", {}).get("include") or []
    cap = int(req.get("max_content_bytes") or 0)
    chosen = [g for g in checks if not include or any(fnmatch.fnmatchcase(g["key"], p) for p in include)]
    rule_groups = []
    if any("alert_labels" in g for g in chosen):
        rule_groups = api("/api/v1/rules", {"type": "alert"}).get("groups") or []
    out = []
    for group in chosen:
        text = content(group, rule_groups)
        if cap and len(text.encode("utf-8")) > cap:
            raise Failure("group %s is over max_content_bytes (%d): split it in the checks file" % (group["key"], cap))
        out.append({"entry": {"key": group["key"], "title": group.get("title") or group["key"], "content": text}})
    out.append({"next_cursor": ""})
    return out


def preflight(identity):
    flags = api("/api/v1/status/flags")
    if not isinstance(flags, dict) or WRITE_FLAGS[0] not in flags:
        return {"read_only": False, "identity": identity, "detail": "the endpoint does not report its flags"}
    open_flags = [f for f in WRITE_FLAGS if str(flags.get(f)).lower() == "true"]
    if open_flags:
        return {"read_only": False, "identity": identity, "detail": "the endpoint takes writes: " + ", ".join(open_flags)}
    api("/api/v1/rules", {"type": "alert"})  # an endpoint that can read nothing is a misconfiguration
    return {"read_only": True, "identity": identity,
            "detail": "checked: no write flag among %d, rules readable" % len(WRITE_FLAGS)}


def main():
    try:
        if len(sys.argv) != 2:
            raise Failure("usage: lister.py <checks.json>")
        req = json.load(sys.stdin)
        checks, identity = load_checks(sys.argv[1])
        mode = req.get("mode")
        if mode == "preflight":
            lines = [preflight(identity)]
        elif mode == "list":
            lines = listing(req, checks)
        else:
            raise Failure("unknown mode")
    except Failure as e:
        sys.stderr.write("lister: %s\n" % e)
        return 2
    except ValueError:
        sys.stderr.write("lister: the request is not JSON\n")
        return 2
    out = sys.stdout.buffer  # LF endings on every OS
    for line in lines:
        out.write(json.dumps(line, sort_keys=True, ensure_ascii=False).encode("utf-8") + b"\n")
    return 0


if __name__ == "__main__":
    sys.exit(main())
