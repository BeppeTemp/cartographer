package main

// --log-file (D217): a Scheduled Task action is an executable plus arguments and
// has nowhere to declare a log destination — Windows has no journald, and the
// launchd plist's StandardOutPath/StandardErrorPath have no counterpart in the
// task schema. Wrapping the action in `cmd.exe /c "… >> log 2>&1"` would work,
// and it would also turn <Arguments> into a nested quoted command line that
// EffectiveConfigPath has to parse to recover --config, which is the one thing
// that must stay trivially readable. So the process takes the path itself and
// the argv stays flat.
//
// The flag is additive on every platform: absent, nothing changes.

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

// openAppendLog opens path for appending, creating its parent directory.
//
// Append and never rotate, on purpose: launchd does not rotate either, and a
// rotation policy invented here would be a second source of truth for something
// no other platform has. A path that cannot be opened is an error the caller
// must fail on — a service that silently logs nowhere is exactly the failure
// mode this flag exists to prevent.
func openAppendLog(path string) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("log file %s: create directory: %w", path, err)
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("log file %s: %w", path, err)
	}
	return f, nil
}

// redirectServerLog sends the standard logger to path. Stdout is deliberately
// left alone: in stdio transport it carries the MCP protocol itself
// (serveStdio's s.Run(os.Stdin, os.Stdout)), and redirecting it would move the
// protocol into the log file. Everything `serve` reports goes through the log
// package, which writes to stderr — that is what moves.
func redirectServerLog(path string) (*os.File, error) {
	f, err := openAppendLog(path)
	if err != nil {
		return nil, err
	}
	log.SetOutput(f)
	return f, nil
}

// redirectClientOutput sends the standard logger *and* stdout/stderr to path.
// Unlike `serve`, a `sync` run reports itself with fmt.Print to stdout and has
// no protocol on it, so capturing only the log package would leave the actual
// report in a scheduled task's void. Both streams are reassigned before any
// output is produced.
func redirectClientOutput(path string) (*os.File, error) {
	f, err := openAppendLog(path)
	if err != nil {
		return nil, err
	}
	log.SetOutput(f)
	os.Stdout = f
	os.Stderr = f
	return f, nil
}
