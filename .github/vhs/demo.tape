# Demo GIF for the README — recorded with vhs (https://github.com/charmbracelet/vhs).
# Do not run directly: use .github/vhs/record.sh, which builds the binary,
# starts a demo server on :8080 with two KBs and sets an isolated HOME before
# invoking vhs.

Output docs/assets/demo.gif

Set FontSize 15
Set Width 1100
Set Height 665
Set Padding 14
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
# The GIF loads at the top of the README on every visit, so frames are the
# budget: 24fps is plenty for a terminal recording and roughly halves the file
# against the 50fps default.
Set Framerate 24

Type "cartographer version"
Enter
Sleep 1.2s

# --kb is explicit on purpose: on a server mounting two or more KBs a first
# connect requires the choice (D190), and an explicit binding is the state the
# documentation recommends — it is also what makes the dashboard's `kbs` line
# read `(explicit)` rather than the implicit all-known fallback.
Type "cartographer connect claude --server-url http://127.0.0.1:8080/mcp --kb homelab-kb"
Enter
Sleep 3s

Type "cartographer status"
Enter
Sleep 3s

Type "clear"
Enter
Sleep 500ms

# Interactive TUI dashboard (no args in a TTY): four-line server panel, then one
# card per detected provider with its binding and per-kind breakdown.
Type "cartographer"
Enter
Sleep 8s

Type "q"
Sleep 1s
