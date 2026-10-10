package service

// The scheduled doctor session (D369): a daily job that runs one agent client
// headless so a KB nobody opens an agent on still gets its kb-doctor session.
// It reuses the machinery of the sync timer (D140) with its own label, unit
// names and task, so it never collides with the server unit or the sync timer
// and `doctor unschedule` removes exactly these files and nothing else.
//
// The job is `<cartographer> doctor run ...`, never the client directly: the
// client's headless flags, the timeout and the declaration to the server live in
// Go, once, and the unit stays a one-line command on all three schedulers.

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	doctorLaunchdLabel   = "com.cartographer.doctor"
	doctorSystemdService = "cartographer-doctor.service"
	doctorSystemdTimer   = "cartographer-doctor.timer"
	windowsDoctorTask    = "Doctor"
)

// DoctorTimerSpec is everything a definition is rendered from. Render* are pure
// functions of it, so the definitions are byte-stable across two installs.
type DoctorTimerSpec struct {
	// BinPath is the cartographer binary the job runs.
	BinPath string
	// Args are the arguments after BinPath (`doctor run --kb ...`). No element
	// may contain a double quote, a newline or a NUL: every scheduler quotes
	// differently and none of the three has an escape this package trusts.
	Args []string
	// Hour and Minute are the local time of day the job fires.
	Hour, Minute int
	// PathEnv is the PATH of the job on unix schedulers (a launchd job or a
	// systemd user unit inherits a minimal one, and a client is often a script
	// whose interpreter sits next to it). Ignored on Windows.
	PathEnv string
	// LogPath receives the job's output on darwin and windows (linux logs to the
	// journal).
	LogPath string
}

// Validate rejects what no renderer can express safely.
func (s DoctorTimerSpec) Validate() error {
	if s.Hour < 0 || s.Hour > 23 || s.Minute < 0 || s.Minute > 59 {
		return fmt.Errorf("service: time of day %02d:%02d out of range", s.Hour, s.Minute)
	}
	for _, a := range append([]string{s.BinPath}, s.Args...) {
		if strings.ContainsAny(a, "\"\n\r\x00") {
			return fmt.Errorf("service: doctor job argument %q contains a quote or control character", a)
		}
	}
	return nil
}

func DoctorLaunchdPlistPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", doctorLaunchdLabel+".plist"), nil
}

func DoctorLaunchdLogPath() (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "cartographer", "doctor.log"), nil
}

func DoctorSystemdServicePath() (string, error) { return systemdUserPath(doctorSystemdService) }
func DoctorSystemdTimerPath() (string, error)   { return systemdUserPath(doctorSystemdTimer) }
func DoctorWindowsTaskPath() (string, error)    { return windowsTaskFile("doctor.xml") }
func DoctorWindowsLogPath() (string, error)     { return windowsLogFile("doctor.log") }

// systemdQuote quotes one ExecStart word: systemd splits on whitespace, expands
// `%` specifiers and `$` variables, and honours backslashes inside quotes.
func systemdQuote(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `%%`, `$`, `$$`)
	return `"` + r.Replace(s) + `"`
}

// RenderDoctorLaunchdPlist renders the launchd agent that fires daily at
// Hour:Minute. StartCalendarInterval jobs missed while the machine slept run on
// wake, which is the analogue of systemd's Persistent=true.
func RenderDoctorLaunchdPlist(s DoctorTimerSpec) string {
	var args strings.Builder
	for _, a := range append([]string{s.BinPath}, s.Args...) {
		fmt.Fprintf(&args, "\t\t<string>%s</string>\n", xmlEscape(a))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Hour</key>
		<integer>%d</integer>
		<key>Minute</key>
		<integer>%d</integer>
	</dict>
	<key>RunAtLoad</key>
	<false/>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>%s</string>
	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, doctorLaunchdLabel, args.String(), s.Hour, s.Minute, xmlEscape(s.PathEnv), xmlEscape(s.LogPath), xmlEscape(s.LogPath))
}

// RenderDoctorSystemdService renders the oneshot unit the timer activates.
func RenderDoctorSystemdService(s DoctorTimerSpec) string {
	words := []string{systemdQuote(s.BinPath)}
	for _, a := range s.Args {
		words = append(words, systemdQuote(a))
	}
	return fmt.Sprintf(`[Unit]
Description=Cartographer scheduled kb-doctor session

[Service]
Type=oneshot
Environment="PATH=%s"
ExecStart=%s
`, strings.NewReplacer(`%`, `%%`).Replace(s.PathEnv), strings.Join(words, " "))
}

// RenderDoctorSystemdTimer renders the daily timer. Persistent catches up a run
// missed while the machine was off.
func RenderDoctorSystemdTimer(s DoctorTimerSpec) string {
	return fmt.Sprintf(`[Unit]
Description=Cartographer scheduled kb-doctor session timer

[Timer]
OnCalendar=*-*-* %02d:%02d:00
Persistent=true

[Install]
WantedBy=timers.target
`, s.Hour, s.Minute)
}

// RenderWindowsDoctorTaskXML renders the daily Scheduled Task. The StartBoundary
// is a fixed date in the past (as for the sync task) so the definition is
// byte-stable; StartWhenAvailable runs a missed slot as soon as the machine is on.
func RenderWindowsDoctorTaskXML(s DoctorTimerSpec) string {
	words := make([]string, 0, len(s.Args))
	for _, a := range s.Args {
		words = append(words, quotePath(a))
	}
	return fmt.Sprintf(windowsTaskXMLDeclaration+`
<Task version="1.2" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Cartographer scheduled kb-doctor session</Description>
    <URI>\%s\%s</URI>
  </RegistrationInfo>
  <Triggers>
    <CalendarTrigger>
      <StartBoundary>2000-01-01T%02d:%02d:00</StartBoundary>
      <Enabled>true</Enabled>
      <ScheduleByDay>
        <DaysInterval>1</DaysInterval>
      </ScheduleByDay>
    </CalendarTrigger>
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
    <ExecutionTimeLimit>PT3H</ExecutionTimeLimit>
    <Priority>7</Priority>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, xmlEscape(windowsTaskFolder), xmlEscape(windowsDoctorTask), s.Hour, s.Minute, xmlEscape(s.BinPath), xmlEscape(strings.Join(words, " ")))
}

// DoctorTimerStatus reports the installed state of the scheduled doctor.
type DoctorTimerStatus struct {
	Installed bool
	Active    bool
	// Path is the plist (darwin), timer unit (linux) or task definition
	// (windows) backing it.
	Path string
	// Definition is the job's own definition file (plist, service unit or task
	// XML): its arguments are the only record of which client and KB it runs.
	Definition string
}

// doctorJobSpec builds the spec the renderers take: the binary, the logs and
// the PATH are the Manager's business, not the caller's.
func doctorJobSpec(binPath string, args []string, hour, minute int, clientBinDir string) (DoctorTimerSpec, error) {
	s := DoctorTimerSpec{BinPath: binPath, Args: args, Hour: hour, Minute: minute}
	switch goos {
	case "darwin":
		p, err := DoctorLaunchdLogPath()
		if err != nil {
			return s, err
		}
		s.LogPath = p
	case "windows":
		p, err := DoctorWindowsLogPath()
		if err != nil {
			return s, err
		}
		s.LogPath = p
	}
	s.PathEnv = servicePATH(binPath)
	if clientBinDir != "" && goos != "windows" {
		s.PathEnv = path.Clean(filepath.ToSlash(clientBinDir)) + ":" + s.PathEnv
	}
	return s, s.Validate()
}

// writeOwnedFile writes one of the doctor timer's own definition files. It
// refuses a symlink at the final path: os.WriteFile would open the link's
// target, and a user who symlinked this name into a git checkout would see that
// file rewritten (D148). Parent directories are not refused when they are links:
// dotfile managers legitimately link ~/.config.
func writeOwnedFile(p string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; remove it and retry", p)
	}
	return os.WriteFile(p, data, 0o644)
}

// InstallDoctorTimer schedules `<this binary> <args...>` daily at hour:minute,
// local time. clientBinDir, when set, joins the job's PATH. Idempotent:
// re-running overwrites the definition and re-registers.
func (m *Manager) InstallDoctorTimer(args []string, hour, minute int, clientBinDir string) error {
	binPath, err := osExecutable()
	if err != nil {
		return fmt.Errorf("service: resolve binary path: %w", err)
	}
	spec, err := doctorJobSpec(resolveStableBinPath(binPath), args, hour, minute, clientBinDir)
	if err != nil {
		return err
	}
	return m.installDoctorSpec(spec)
}

func (m *Manager) installDoctorSpec(spec DoctorTimerSpec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	switch goos {
	case "darwin":
		plistPath, err := DoctorLaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor plist path: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o755); err != nil {
			return fmt.Errorf("service: create log dir: %w", err)
		}
		if err := writeOwnedFile(plistPath, []byte(RenderDoctorLaunchdPlist(spec))); err != nil {
			return fmt.Errorf("service: write doctor plist: %w", err)
		}
		uid := os.Getuid()
		// Best-effort: bootout fails when the job was not loaded yet.
		m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", uid, doctorLaunchdLabel))
		if _, err := m.run("launchctl", "bootstrap", fmt.Sprintf("gui/%d", uid), plistPath); err != nil {
			return fmt.Errorf("service: launchctl bootstrap doctor timer: %w", err)
		}
		return nil
	case "linux":
		servicePath, err := DoctorSystemdServicePath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor unit path: %w", err)
		}
		timerPath, err := DoctorSystemdTimerPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor timer path: %w", err)
		}
		if err := writeOwnedFile(servicePath, []byte(RenderDoctorSystemdService(spec))); err != nil {
			return fmt.Errorf("service: write doctor unit: %w", err)
		}
		if err := writeOwnedFile(timerPath, []byte(RenderDoctorSystemdTimer(spec))); err != nil {
			os.Remove(servicePath)
			return fmt.Errorf("service: write doctor timer: %w", err)
		}
		if _, err := m.run("systemctl", "--user", "daemon-reload"); err != nil {
			return fmt.Errorf("service: systemctl daemon-reload: %w", err)
		}
		if _, err := m.run("systemctl", "--user", "enable", "--now", doctorSystemdTimer); err != nil {
			return fmt.Errorf("service: systemctl enable --now %s: %w", doctorSystemdTimer, err)
		}
		return nil
	case "windows":
		if err := os.MkdirAll(filepath.Dir(spec.LogPath), 0o755); err != nil {
			return fmt.Errorf("service: create log dir: %w", err)
		}
		taskPath, err := DoctorWindowsTaskPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor task path: %w", err)
		}
		if err := writeOwnedFile(taskPath, []byte(RenderWindowsDoctorTaskXML(spec))); err != nil {
			return fmt.Errorf("service: write doctor task definition: %w", err)
		}
		return m.registerWindowsTask(windowsDoctorTask, taskPath)
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// UninstallDoctorTimer unregisters and removes the definition(s). Removing a
// timer that is not installed is a success.
func (m *Manager) UninstallDoctorTimer() error {
	switch goos {
	case "darwin":
		plistPath, err := DoctorLaunchdPlistPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor plist path: %w", err)
		}
		m.run("launchctl", "bootout", fmt.Sprintf("gui/%d/%s", os.Getuid(), doctorLaunchdLabel))
		if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove doctor plist: %w", err)
		}
		return nil
	case "linux":
		servicePath, err := DoctorSystemdServicePath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor unit path: %w", err)
		}
		timerPath, err := DoctorSystemdTimerPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor timer path: %w", err)
		}
		m.run("systemctl", "--user", "disable", "--now", doctorSystemdTimer)
		for _, p := range []string{timerPath, servicePath} {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("service: remove %s: %w", filepath.Base(p), err)
			}
		}
		_, err = m.run("systemctl", "--user", "daemon-reload")
		return err
	case "windows":
		taskPath, err := DoctorWindowsTaskPath()
		if err != nil {
			return fmt.Errorf("service: resolve doctor task path: %w", err)
		}
		m.unregisterWindowsTask(windowsDoctorTask)
		if err := os.Remove(taskPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("service: remove doctor task definition: %w", err)
		}
		return nil
	default:
		return fmt.Errorf("service: unsupported platform %q", goos)
	}
}

// DoctorTimerStatus reports whether the scheduled doctor is installed and
// active. The file on disk is the authority on "installed"; a missing
// launchctl/systemctl leaves Active false rather than failing.
func (m *Manager) DoctorTimerStatus() (DoctorTimerStatus, error) {
	var st DoctorTimerStatus
	read := func(p string) {
		if data, err := os.ReadFile(p); err == nil {
			st.Installed = true
			st.Definition = string(data)
		}
	}
	switch goos {
	case "darwin":
		p, err := DoctorLaunchdPlistPath()
		if err != nil {
			return st, err
		}
		st.Path = p
		read(p)
		if _, err := m.run("launchctl", "print", fmt.Sprintf("gui/%d/%s", os.Getuid(), doctorLaunchdLabel)); err == nil {
			st.Active = true
		}
	case "linux":
		timerPath, err := DoctorSystemdTimerPath()
		if err != nil {
			return st, err
		}
		servicePath, err := DoctorSystemdServicePath()
		if err != nil {
			return st, err
		}
		st.Path = timerPath
		read(servicePath)
		if _, err := os.Stat(timerPath); err != nil {
			st.Installed = false
		}
		if out, err := m.run("systemctl", "--user", "is-active", doctorSystemdTimer); err == nil && len(out) > 0 {
			st.Active = true
		}
	case "windows":
		p, err := DoctorWindowsTaskPath()
		if err != nil {
			return st, err
		}
		st.Path = p
		read(p)
		st.Active = m.windowsTaskRegistered(windowsDoctorTask)
	default:
		return st, fmt.Errorf("service: unsupported platform %q", goos)
	}
	return st, nil
}
