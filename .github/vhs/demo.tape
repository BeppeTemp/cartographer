# Demo GIF for the README — recorded with vhs (https://github.com/charmbracelet/vhs).
# Do not run directly: use .github/vhs/record.sh, which builds the binary,
# starts a demo server on :8080 and sets an isolated HOME before invoking vhs.

Output docs/assets/demo.gif

Set FontSize 15
Set Width 1100
Set Height 620
Set Padding 14
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms

Type "cartographer version"
Enter
Sleep 1.2s

Type "cartographer connect claude --server-url http://127.0.0.1:8080/mcp"
Enter
Sleep 2.5s

Type "cartographer status"
Enter
Sleep 2.5s

Type "clear"
Enter
Sleep 500ms

# Interactive TUI dashboard (no args in a TTY)
Type "cartographer"
Enter
Sleep 6s

Type "q"
Sleep 1s
