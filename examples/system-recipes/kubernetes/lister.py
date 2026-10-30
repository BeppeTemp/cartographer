#!/usr/bin/env python3
"""A system lister for Kubernetes: the reference recipe of `shape: system`.

It speaks protocol v1 of the worker's exec recipes (docs/worker-recipes.md):
one JSON request on stdin, mode "preflight" or "list", the answer on stdout.
It talks to the cluster only through kubectl and never uses the request's
source.locator to choose a context, namespace, file or command.

Environment (all supplied by the recipe's exec.env):
  KUBECTL              the kubectl binary (default: kubectl)
  KUBE_CONTEXT         the context to use (default: the current one)
  KUBECONFIG           read by kubectl itself
  LISTER_FIXTURES_DIR  tests only: read answers from files named after the
                       kubectl argument vector instead of running kubectl
"""
import fnmatch
import json
import os
import subprocess
import sys

KINDS = ["Deployment", "StatefulSet", "DaemonSet", "CronJob", "Service", "Ingress", "PersistentVolumeClaim", "ConfigMap"]
RESOURCES = ("deployments.apps,statefulsets.apps,daemonsets.apps,cronjobs.batch,services,"
             "ingresses.networking.k8s.io,persistentvolumeclaims,configmaps")
# What the lister reads: it lists these resources and the namespaces.
READS = ["deployments.apps", "statefulsets.apps", "daemonsets.apps", "cronjobs.batch", "services",
         "ingresses.networking.k8s.io", "persistentvolumeclaims", "configmaps"]
# What an identity must not be able to do: write to anything the lister reads (and to pods and
# namespaces), read a Secret in any way, or open a shell, an attach or a port-forward into a pod.
WRITES = ["pods", "namespaces"] + READS
# (verb, resource, wanted answer). The identity passes only if every "no" is a no and every "yes" a
# yes; an identity that can read nothing is a misconfiguration, not a pass.
QUESTIONS = ([("*", "*", "no")]
             + [(v, "secrets", "no") for v in ("get", "list", "watch")]
             + [("create", r, "no") for r in ("pods/exec", "pods/attach", "pods/portforward")]
             + [(v, r, "no") for r in WRITES for v in ("create", "update", "patch", "delete")]
             + [("list", r, "yes") for r in ["namespaces"] + READS])


class Failure(Exception):
    """A short message for stderr and a non-zero exit. Never a credential."""


def kubectl(args):
    """Returns (exit status, stdout). Fixtures replace kubectl in the tests."""
    fixtures = os.environ.get("LISTER_FIXTURES_DIR")
    if fixtures:
        name = "_".join(a.replace("*", "all").replace("/", "-") for a in args)
        for ext in (".json", ".txt"):
            path = os.path.join(fixtures, name + ext)
            if os.path.isfile(path):
                with open(path, encoding="utf-8", newline="") as f:
                    return 0, f.read()
        raise Failure("no fixture for kubectl " + " ".join(args))
    cmd = [os.environ.get("KUBECTL") or "kubectl"]
    if os.environ.get("KUBE_CONTEXT"):
        cmd += ["--context", os.environ["KUBE_CONTEXT"]]
    try:
        p = subprocess.run(cmd + args, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, timeout=120)
    except (OSError, subprocess.SubprocessError):
        raise Failure("cannot run kubectl")
    return p.returncode, p.stdout.decode("utf-8", "replace")


def kubectl_json(args):
    code, out = kubectl(args)
    if code != 0:
        raise Failure("kubectl %s failed (exit %d)" % (" ".join(args[:2]), code))
    try:
        return json.loads(out)
    except ValueError:
        raise Failure("kubectl %s answered something that is not JSON" % " ".join(args[:2]))


def can_i(verb, resource):
    code, out = kubectl(["auth", "can-i", verb, resource])
    answer = out.strip()
    # kubectl exits 1 on "no": that is an answer, not a failure.
    if answer not in ("yes", "no") or code not in (0, 1):
        raise Failure("kubectl auth can-i %s %s gave no yes or no" % (verb, resource))
    return answer


def preflight():
    identity = "unknown"
    cfg = kubectl_json(["config", "view", "--minify", "-o", "json"])
    users = cfg.get("users") or []
    if users and isinstance(users[0], dict):
        identity = str(users[0].get("name") or identity)
    for verb, resource, want in QUESTIONS:
        if can_i(verb, resource) != want:
            return {"read_only": False, "identity": identity,
                    "detail": "can-i %s %s answered %s, expected %s" % (verb, resource, "yes" if want == "no" else "no", want)}
    return {"read_only": True, "identity": identity,
            "detail": "checked: no * on *, no get/list/watch on secrets, no create on pods/exec, pods/attach or pods/portforward, "
                      "no write verb on %d resources, list allowed on %d resources" % (len(WRITES), len(READS) + 1)}


def kv(d):
    return ",".join("%s=%s" % (k, d[k]) for k in sorted(d or {}))


def containers(pod_spec):
    cs = sorted((pod_spec or {}).get("containers") or [], key=lambda c: c.get("name", ""))
    names = ",".join("%s:%s" % (c.get("name", ""), c.get("image", "")) for c in cs)
    ports = sorted({p.get("containerPort") for c in cs for p in (c.get("ports") or []) if isinstance(p.get("containerPort"), int)})
    return names, ",".join(str(p) for p in ports)


def workload(kind, item):
    spec = item.get("spec") or {}
    if kind == "CronJob":
        pod = ((spec.get("jobTemplate") or {}).get("spec") or {}).get("template", {}).get("spec")
        extra = "schedule=%s" % spec.get("schedule", "")
    else:
        pod = (spec.get("template") or {}).get("spec")
        extra = "" if kind == "DaemonSet" else "replicas=%s" % spec.get("replicas", 1)
    names, ports = containers(pod)
    parts = [kind, item["metadata"]["name"]] + ([extra] if extra else [])
    return " ".join(parts + ["containers=" + names, "ports=" + ports, "labels=" + kv(item["metadata"].get("labels"))])


def describe(kind, item):
    spec, name = item.get("spec") or {}, item["metadata"]["name"]
    if kind in ("Deployment", "StatefulSet", "DaemonSet", "CronJob"):
        return workload(kind, item)
    if kind == "Service":
        ports = ",".join("%s/%s" % (p.get("port"), p.get("protocol", "TCP")) for p in spec.get("ports") or [])
        return "Service %s type=%s ports=%s selector=%s" % (name, spec.get("type", "ClusterIP"), ports, kv(spec.get("selector")))
    if kind == "Ingress":
        hosts = sorted({r.get("host", "") for r in spec.get("rules") or []})
        backends = sorted({"%s:%s" % (b["service"].get("name", ""), (b["service"].get("port") or {}).get("number", (b["service"].get("port") or {}).get("name", "")))
                           for r in spec.get("rules") or [] for p in ((r.get("http") or {}).get("paths") or [])
                           for b in [p.get("backend") or {}] if b.get("service")})
        return "Ingress %s hosts=%s backends=%s" % (name, ",".join(hosts), ",".join(backends))
    if kind == "PersistentVolumeClaim":
        return "PersistentVolumeClaim %s class=%s size=%s" % (name, spec.get("storageClassName", ""), ((spec.get("resources") or {}).get("requests") or {}).get("storage", ""))
    return "ConfigMap %s" % name  # names only: the data is never read


def namespace_content(ns):
    lst = kubectl_json(["get", RESOURCES, "-n", ns, "-o", "json"])
    lines = []
    for item in lst.get("items") or []:
        kind = item.get("kind")
        if kind in KINDS and item.get("metadata", {}).get("name"):  # a Secret is neither requested nor kept
            lines.append((KINDS.index(kind), item["metadata"]["name"], describe(kind, item)))
    lines.sort(key=lambda t: (t[0], t[1]))
    return "".join(t[2] + "\n" for t in lines) or "no objects\n"


def listing(req):
    include = req.get("source", {}).get("include") or []
    cap = int(req.get("max_content_bytes") or 0)
    names = sorted(i["metadata"]["name"] for i in kubectl_json(["get", "namespaces", "-o", "json"]).get("items") or [])
    out = []
    for ns in names:
        key = "namespace/" + ns
        if include and not any(fnmatch.fnmatchcase(key, g) for g in include):
            continue
        content = namespace_content(ns)
        if cap and len(content.encode("utf-8")) > cap:
            raise Failure("namespace %s is over max_content_bytes (%d): filter it out with include" % (ns, cap))
        out.append({"entry": {"key": key, "title": "Namespace " + ns, "content": content}})
    out.append({"next_cursor": ""})
    return out


def main():
    try:
        req = json.load(sys.stdin)
        mode = req.get("mode")
        if mode == "preflight":
            lines = [preflight()]
        elif mode == "list":
            lines = listing(req)
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
