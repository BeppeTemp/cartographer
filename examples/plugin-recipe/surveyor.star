# A feed recipe in Starlark: it lists the pages of an example wiki that changed
# since a cursor. The wiki is not real; tests/example-pages.yaml records its answers,
# so `cartographer plugin test examples/plugin-recipe` runs this with no network.
BASE = "https://wiki.example.com/api/pages"

def spec(req):
    return {
        "title": "Example wiki",
        "description": "Lists the pages of an example wiki, with their text.",
        "shapes": ["feed"],
        "verbs": ["spec", "check", "list"],
        "settings": {},
        "env": ["WIKI_TOKEN"],
    }

def headers():
    # The token is the Surveyor variable the config maps to WIKI_TOKEN; it is sent,
    # never printed or put in an entry.
    return {"Authorization": "Bearer " + env.get("WIKI_TOKEN")}

def check(req):
    resp = http.request("GET", BASE + "?limit=1", headers = headers())
    return {"ok": resp["status"] == 200, "detail": "the wiki answered " + str(resp["status"])}

def lst(req):
    since = req.get("cursor") or ""
    resp = http.request("GET", BASE + "?since=" + since, headers = headers())
    if resp["status"] != 200:
        fail("the wiki answered " + str(resp["status"]))
    pages = json.decode(resp["body"])["pages"]
    entries = [{
        "key": p["id"],
        "title": p["title"],
        "revision": str(p["version"]),
        "change": "modified" if since else "added",
        "content": p["text"],
    } for p in pages]
    # The cursor is opaque to the Surveyor: here the time of the last page seen.
    return {"entries": entries, "next_cursor": pages[-1]["updated"] if pages else since}

recipe("example-pages", lst, spec = spec, check = check)
