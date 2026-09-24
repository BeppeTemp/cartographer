#!/usr/bin/env python3
"""privacy-guard: fail when text contains a private term, without storing the terms.

Terms are stored only as HMAC-SHA256 digests (key: $PRIVACY_GUARD_KEY) of a
lowercase word or of two adjacent words joined by a space. Usage:
  privacy-guard [FILE...]          scan files (or stdin); exit 1 on a match
  privacy-guard --redact FILE      rewrite FILE with matches replaced by [redacted]
  privacy-guard --add              read terms on stdin, print their digests
Digests: $PRIVACY_GUARD_DIGESTS or ~/.config/privacy-guard/digests.
"""
import hashlib, hmac, os, re, sys

WORD = re.compile(r"[a-z0-9]+")

def key():
    # The env var is the standard; ~/.zshrc.local (generated from chezmoi's
    # custom_envs) covers agents whose shell did not load the profile.
    k = os.environ.get("PRIVACY_GUARD_KEY", "")
    if not k:
        try:
            m = re.search(r"^export PRIVACY_GUARD_KEY=\"?([0-9a-f]+)", open(os.path.expanduser("~/.zshrc.local")).read(), re.M)
            k = m.group(1) if m else ""
        except OSError:
            pass
    if not k:
        print("privacy-guard: PRIVACY_GUARD_KEY is not set", file=sys.stderr)
        sys.exit(2)
    return k.encode()

def digest(k, s):
    return hmac.new(k, s.encode(), hashlib.sha256).hexdigest()

def load():
    p = os.environ.get("PRIVACY_GUARD_DIGESTS", os.path.expanduser("~/.config/privacy-guard/digests"))
    try:
        with open(p) as f:
            return {l.split()[0] for l in f if l.strip() and not l.startswith("#")}
    except OSError as e:
        print(f"privacy-guard: cannot read digests: {e}", file=sys.stderr)
        sys.exit(2)

def spans(text, k, ds):
    """Yield (start, end) of every matching word or word pair."""
    low = text.lower()
    words = [(m.start(), m.end(), m.group()) for m in WORD.finditer(low)]
    for i, (s, e, w) in enumerate(words):
        if digest(k, w) in ds:
            yield s, e
        if i + 1 < len(words) and words[i + 1][0] - e <= 1:
            if digest(k, w + " " + words[i + 1][2]) in ds:
                yield s, words[i + 1][1]

def main(argv):
    k = key()
    if argv[:1] == ["--add"]:
        for t in sys.stdin.read().split("\n"):
            t = " ".join(WORD.findall(t.lower()))
            if t:
                print(digest(k, t))
        return 0
    ds = load()
    if argv[:1] == ["--redact"]:
        path = argv[1]
        text = open(path).read()
        for s, e in sorted(set(spans(text, k, ds)), reverse=True):
            text = text[:s] + "[redacted]" + text[e:]
        open(path, "w").write(text)
        return 0
    sources = [(p, open(p, errors="replace").read()) for p in argv] or [("stdin", sys.stdin.read())]
    hits = 0
    for name, text in sources:
        for s, e in spans(text, k, ds):
            line = text.count("\n", 0, s) + 1
            print(f"privacy-guard: {name}:{line}: private term at column {s - text.rfind(chr(10), 0, s)}", file=sys.stderr)
            hits += 1
    return 1 if hits else 0

if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
