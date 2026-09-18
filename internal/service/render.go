// Package service manages the cartographer MCP server as a native per-user
// service: a launchd agent on macOS, a systemd user unit on Linux, a per-user
// Scheduled Task on Windows (D217). It replaces local Docker deployment (D73).
//
// The package separates pure file generation (Render*, DefaultServerYAML,
// the path functions) from platform command execution (Manager, which runs
// launchctl/systemctl/PowerShell through an injectable runner) so the
// generation half is trivially unit-testable and the execution half is
// testable via a stubbed runner.
package service

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// packageManagerPrefixes are the directories a service's PATH must include
// beyond the platform default, in this order: the package managers that install
// the external binaries Cartographer shells out to (sops for secret
// resolution, git). Curated and fixed rather than copied from the installing
// user's shell, which would bake that user's whole environment into a service
// definition.
var packageManagerPrefixes = []string{"/opt/homebrew/bin", "/usr/local/bin", "/opt/local/bin"}

// platformDefaultPath is the minimal PATH a launchd job or a systemd user unit
// gets when the definition sets none.
var platformDefaultPath = []string{"/usr/bin", "/bin", "/usr/sbin", "/sbin"}

// servicePATH is the PATH written into the generated service definitions. A
// launchd job inherits a minimal PATH and a systemd user unit an equally short
// one, so a Homebrew-installed sops sits outside both and every secret
// resolution fails with "sops binary not found in PATH" — from the definition
// `service install` itself generated. The binary's own directory comes first:
// whatever installed Cartographer is the most likely place to hold its
// companions.
func servicePATH(binPath string) string {
	var dirs []string
	seen := map[string]bool{}
	add := func(d string) {
		if d == "" || d == "." || seen[d] {
			return
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	if binPath != "" {
		// The result is the PATH of a launchd job or a systemd user unit, so it
		// is a unix path list whatever host renders it: splitting the binary's
		// directory off with the host's separator would spell it with
		// backslashes when the definition is generated from Windows.
		add(path.Dir(filepath.ToSlash(binPath)))
	}
	for _, d := range packageManagerPrefixes {
		add(d)
	}
	for _, d := range platformDefaultPath {
		add(d)
	}
	return strings.Join(dirs, ":")
}

// RenderLaunchdPlist renders the launchd agent plist that runs
// `<binPath> serve --config <configPath>`, logging stdout/stderr to logPath.
func RenderLaunchdPlist(binPath, configPath, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.cartographer.serve</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>serve</string>
		<string>--config</string>
		<string>%s</string>
	</array>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, binPath, configPath, servicePATH(binPath), logPath, logPath)
}

// RenderSystemdUnit renders the systemd user unit that runs
// `<binPath> serve --config <configPath>`, restarting on failure. Logs go to
// journald (the systemd default), no explicit log path. PATH is set from the
// same servicePATH the launchd plist uses, so the two definitions cannot drift.
func RenderSystemdUnit(binPath, configPath string) string {
	return fmt.Sprintf(`[Unit]
Description=Cartographer MCP server

[Service]
Environment=PATH=%s
ExecStart=%s serve --config %s
Restart=on-failure

[Install]
WantedBy=default.target
`, servicePATH(binPath), binPath, configPath)
}

// xmlEscape escapes the three characters that are not legal in XML element
// text. A path is user data — `C:\R&D\bin\cartographer.exe` is a legal path —
// and an unescaped ampersand makes the whole definition unparseable, which
// Register-ScheduledTask reports as a malformed task rather than as a bad path.
//
// Quotes are deliberately left alone. They need no escaping in element text, and
// every value rendered here goes into element text, never into an attribute. A
// `&quot;` in <Arguments> would be valid XML and would still be the opposite of
// what Task Scheduler's own export writes, which is what an operator compares
// against when a task misbehaves.
func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// quotePath wraps a path in double quotes so a path containing a space survives
// as one argument. It is unconditional rather than conditional on whitespace:
// the value goes into <Arguments>, which Task Scheduler hands to the process as
// a single command line, and a quoted path is correct whether or not it needs to
// be — while a conditionally quoted one makes the reader (EffectiveConfigPath)
// carry two cases instead of one.
func quotePath(p string) string { return `"` + p + `"` }

// RenderWindowsTaskXML renders the Task Scheduler definition of the server
// task: `<binPath> serve --config <configPath> --log-file <logPath>`, started
// at the user's logon and restarted on failure. Sibling of RenderLaunchdPlist
// and RenderSystemdUnit (D217).
//
// Four Task Scheduler defaults would each break a long-running server, so each
// is overridden explicitly and pinned by a test:
//
//   - ExecutionTimeLimit defaults to 3 days, after which the task is killed;
//     PT0S means no limit.
//   - StopIfGoingOnBatteries and DisallowStartIfOnBatteries default to true on
//     a laptop, which would stop the MCP server the moment the charger comes
//     out.
//   - IdleSettings/StopOnIdleEnd defaults to stopping the task when the machine
//     stops being idle.
//   - MultipleInstancesPolicy defaults to IgnoreNew only in recent exports; it
//     is declared so a second Start-ScheduledTask can never produce two servers
//     competing for the same port and the same KB lock.
//
// There is deliberately **no PATH**: the format cannot express one (an Exec
// action has Command, Arguments and WorkingDirectory, and nothing else), and it
// does not need to. The D156 fix exists because a launchd job inherits a minimal
// PATH that excludes Homebrew, so a Homebrew `sops` was invisible; a Scheduled
// Task registered with an interactive token inherits the user's environment,
// where the machine and user PATH from the registry — which is where winget,
// Scoop and Chocolatey put their shims — is already present.
//
// The encoding declaration says UTF-8 and the bytes are UTF-8. Task Scheduler's
// own export writes UTF-16, and `schtasks /Create /XML` demands it with a BOM,
// which is exactly why this file is registered through
// Register-ScheduledTask -Xml with the definition as a string instead: the
// declaration then describes the file honestly for whoever opens it, and
// EffectiveConfigPath can read it back with an ordinary UTF-8 read.
func RenderWindowsTaskXML(binPath, configPath, logPath string) string {
	args := fmt.Sprintf("serve --config %s --log-file %s", quotePath(configPath), quotePath(logPath))
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Cartographer MCP server</Description>
    <URI>\%s\%s</URI>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RunOnlyIfNetworkAvailable>false</RunOnlyIfNetworkAvailable>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
    <AllowStartOnDemand>true</AllowStartOnDemand>
    <Enabled>true</Enabled>
    <Hidden>false</Hidden>
    <RunOnlyIfIdle>false</RunOnlyIfIdle>
    <WakeToRun>false</WakeToRun>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Priority>7</Priority>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(windowsTaskFolder), xmlEscape(windowsServeTaskName), xmlEscape(binPath), xmlEscape(args))
}

// DefaultServerYAML renders the minimal `cartographer serve --config` YAML
// generated by `cartographer service install` when no config file exists yet
// at the target path. See config.example.yaml for the full set of options.
func DefaultServerYAML(dataDir, httpAddr string) string {
	return fmt.Sprintf(`# Generated by "cartographer service install".
# See config.example.yaml (repo root) for the full set of options.
http: %q
data: %q
init: true
# Set both values to a forge account when remote push rules validate authors.
# See config.example.yaml for identity and other git options.
git:
  # author_name: "Your Name"
  # author_email: "you@example.com"
`, httpAddr, dataDir)
}
