A group is one responsibility of the system: a service or a class of hosts. Its unit carries one line per signal,
`<signal>: <verdict>`, and one per alert rule, `alert <name>: <verdict>`. A verdict is one word of the scale `ok`,
`watch`, `no data`, `bad`, from best to worst. There are no values: a line changes only when its verdict does.

The targets are the pages the plan's ownership table says own that service's state: a service page, an environment
page. Extract the verdict lines into the dated state section. Verify that the section states the Observed block with
its date and that every line that moved towards `bad` has its history line in an open gap. The executor is `any`.

Copy these rules into the body word for word, for every group:

- For each line of the Change that moved towards `bad`, add `<date> <line>: <from> -> <to>` under `## History` of the
  gap `gaps/verdict-<group>-<line>` (group and line in lowercase, every other character a hyphen). The gap is an
  `open_question` Contradiction whose `involves` is the target page: create it if it is missing, set
  `resolution_status: open` if it is resolved.
- For each line that moved towards `ok`, add the same history line if the gap exists, and create nothing.
- On a first observation nothing has moved: treat a line that is not `ok` as moved from `ok`.
