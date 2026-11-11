# The example plugin: one command, one tool and one sync handler. Everything it
# touches is granted by cartographer-plugin.yaml: a KB read and one folder under
# home. It runs no process and reads no clock at load.
load("lib/greeting.star", "greet")

def hello(args):
    name = args[0] if args else "world"
    print(greet(settings.greeting, name))

def echo(params):
    hits = kb.call("search", kb = params["kb"], query = params["text"])
    return {"echo": params["text"], "hits": hits}

def after_sync(ctx):
    files.write("{home}/.example-forks/last-sync.txt", "synced " + str(ctx.installed) + " installed\n")

command("hello", hello, "Print a greeting built from the greeting setting", usage = "[name]")
tool(
    "echo",
    echo,
    "Search a KB for the given text and return it with the hits",
    params = {
        "type": "object",
        "properties": {
            "kb": {"type": "string", "description": "The KB to search"},
            "text": {"type": "string", "description": "What to search for"},
        },
        "required": ["kb", "text"],
    },
    writes = False,
)
on("sync.after", after_sync)
