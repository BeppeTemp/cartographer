#!/usr/bin/env python3
"""privacy-guard: fail when text contains a private term, without storing the terms.

Terms are stored only as HMAC-SHA256 digests (key: $PRIVACY_GUARD_KEY) of a
lowercase word or of two adjacent words joined by a space. Usage:
  privacy-guard [FILE...]          scan files (or stdin); exit 1 on a match
  privacy-guard --redact FILE      rewrite FILE with matches replaced by [redacted]
  privacy-guard --add              read terms on stdin, print their digests
Digests: $PRIVACY_GUARD_DIGESTS or ~/.config/privacy-guard/digests.
"""
import hashlib, hmac, os, re, sys, unicodedata

def words(text):
    """Return [(start, end, word)] of the lowercase ASCII words of text.

    Folded one character at a time (NFKC, then lower), so the offsets stay those
    of the ORIGINAL text: text.lower() can lengthen a character (U+0130) and shift
    every later span, while a regex on the original misses a character that only
    folds to a letter (U+212A, the Kelvin sign, is a k). Only the ASCII letters and
    digits of a fold count; any other character separates words.
    """
    out, start, buf = [], None, []
    for i, ch in enumerate(text):
        f = "".join(c for c in unicodedata.normalize("NFKC", ch).lower() if c in "abcdefghijklmnopqrstuvwxyz0123456789")
        if f:
            if start is None:
                start = i
            buf.append(f)
        elif start is not None:
            out.append((start, i, "".join(buf)))
            start, buf = None, []
    if start is not None:
        out.append((start, len(text), "".join(buf)))
    return out

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
    ws = words(text)
    for i, (s, e, w) in enumerate(ws):
        if digest(k, w) in ds:
            yield s, e
        if i + 1 < len(ws) and ws[i + 1][0] - e <= 1:
            if digest(k, w + " " + ws[i + 1][2]) in ds:
                yield s, ws[i + 1][1]

def main(argv):
    k = key()
    if argv[:1] == ["--add"]:
        for t in sys.stdin.read().split("\n"):
            t = " ".join(w for _, _, w in words(t))
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
