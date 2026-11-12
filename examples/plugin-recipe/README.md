# example-wiki

A Starlark recipe plugin: `surveyor.star` registers `example-pages`, which lists the pages of an example wiki over
HTTP, and `tests/` holds its scenario and the recorded answers of the wiki.

```
cartographer plugin validate examples/plugin-recipe
cartographer plugin test examples/plugin-recipe
```

`plugin test` runs the recipe against `tests/example-pages.yaml` with no network and then holds it to the conformance
checks (`docs/plugins.md` §Testing a surveyor side). To use it, vendor it with `plugin vendor`, pin the printed digest
in the Surveyor config and map `WIKI_TOKEN` under `plugins.example-wiki.env`.
