package provisioning

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/BeppeTemp/cartographer/internal/procattr"
)

// The logic behind the Cline dispatcher files (clinehooks.go): `cartographer
// hook dispatch --provider cline --event <Event>` reads Cline's hook payload,
// runs the Cartographer hooks of that event (the bootstrap hook, write-findings
// and the KB's, all materialized under clineHookRoots) as Claude-style commands,
// and answers in Cline's three-field result (cancel, contextModification,
// errorMessage). A hook must never break a session: every outcome other than a
// stated block is an empty result.

// clineHookFilesPrefix is where a Cline hook's own files live, relative to the
// base dir and in slash form. The only thing Cartographer puts in
// Documents/Cline/Hooks is a dispatcher.
const clineHookFilesPrefix = ".cline/cartographer-hooks/"

// clineHookEvents maps the hook.json vocabulary to the Cline event whose
// dispatcher runs it.
var clineHookEvents = map[string]string{
	"SessionStart":     "TaskStart",
	"UserPromptSubmit": "UserPromptSubmit",
	"PreToolUse":       "PreToolUse",
	"PostToolUse":      "PostToolUse",
}

// clineEventNames are the Cline events that may have a dispatcher, sorted.
var clineEventNames = []string{"PostToolUse", "PreToolUse", "TaskStart", "UserPromptSubmit"}

// clineHookRoots are the directories (slash form, relative to the base dir)
// whose subdirectories are hooks. A variant of Cline appends its own.
var clineHookRoots = []string{strings.TrimSuffix(clineHookFilesPrefix, "/")}

// Seams: the budget Cline's 30 s kill leaves us, and where the SessionStart
// markers live.
var (
	clineHookBudget = 25 * time.Second
	clineTempDir    = os.TempDir
)

const (
	clineMarkerTTL    = 12 * time.Hour
	clineMaxContext   = 50000 // Cline caps contextModification at this many characters
	clineMarkerPrefix = "cartographer-cline-"
)

// ClineDispatchResult is Cline's hook result: the only three fields its
// extension reads. Empty fields are omitted, so no effect marshals to {}.
type ClineDispatchResult struct {
	Cancel              bool   `json:"cancel,omitempty"`
	ContextModification string `json:"contextModification,omitempty"`
	ErrorMessage        string `json:"errorMessage,omitempty"`
}

// clineHook is one materialized hook that has a Cline event.
type clineHook struct {
	name, dir, clineEvent string
	spec                  hookSpec
	matcher               *regexp.Regexp // nil: every tool
}

// clineHooks lists the hooks on disk that a Cline dispatcher can run: a
// hook.json that loads, whose event Cline fires, and whose matcher is a valid
// regular expression. exclude is a hook name left out (the one being removed).
// Sorted by root, then name: the order is deterministic.
func clineHooks(baseDir, exclude string) []clineHook {
	var out []clineHook
	for _, root := range clineHookRoots {
		rootDir := filepath.Join(baseDir, filepath.FromSlash(root))
		entries, err := os.ReadDir(rootDir)
		if err != nil {
			continue
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if !e.IsDir() || e.Name() == exclude {
				continue
			}
			dir := filepath.Join(rootDir, e.Name())
			spec, err := loadHookSpec(dir)
			ce, ok := clineHookEvents[spec.Event]
			if err != nil || !ok {
				continue
			}
			h := clineHook{name: e.Name(), dir: dir, clineEvent: ce, spec: spec}
			if m := strings.TrimSpace(spec.Matcher); m != "" && spec.Event != "SessionStart" {
				// Unanchored and case-insensitive, like Pi's: Cline's tools are
				// lowercase, the KB's matchers are Claude's.
				if h.matcher, err = regexp.Compile("(?i)" + m); err != nil {
					continue
				}
			}
			out = append(out, h)
		}
	}
	return out
}

// clinePayload is the part of Cline's stdin payload the dispatcher reads
// (docs/harnesses.md, bundle 4.1.26). Every tool parameter is a string.
type clinePayload struct {
	TaskID         string   `json:"taskId"`
	WorkspaceRoots []string `json:"workspaceRoots"`
	PreToolUse     struct {
		ToolName   string            `json:"toolName"`
		Parameters map[string]string `json:"parameters"`
	} `json:"preToolUse"`
	PostToolUse struct {
		ToolName   string            `json:"toolName"`
		Parameters map[string]string `json:"parameters"`
		Result     string            `json:"result"`
		Success    bool              `json:"success"`
	} `json:"postToolUse"`
	UserPromptSubmit struct {
		Prompt string `json:"prompt"`
	} `json:"userPromptSubmit"`
}

// DispatchClineHooks runs the hooks of one Cline event (a name of
// clineEventNames) for the payload on stdin, and merges their outcomes the way
// Cline merges its own: cancel OR-ed, texts joined. Anything it cannot read or
// run is an empty result.
//
// Outcomes (Pi's contract, with Cline's channel): PreToolUse exit 2 cancels,
// and Cline stops the whole task, there is no per-call skip; PostToolUse exit 2
// with stderr is context, never cancel (a stopped task would lose the agent's
// write); SessionStart and UserPromptSubmit exit 0 with stdout is context; any
// other status, a spawn error or a timeout has no effect.
func DispatchClineHooks(baseDir, event string, stdin []byte) ClineDispatchResult {
	var p clinePayload
	if json.Unmarshal(stdin, &p) != nil {
		return ClineDispatchResult{}
	}
	toolName, params := "", map[string]string(nil)
	switch event {
	case "PreToolUse":
		toolName, params = p.PreToolUse.ToolName, p.PreToolUse.Parameters
	case "PostToolUse":
		// Claude fires PostToolUse on success only.
		if !p.PostToolUse.Success {
			return ClineDispatchResult{}
		}
		toolName, params = p.PostToolUse.ToolName, p.PostToolUse.Parameters
	}
	// An MCP tool is <server>__<tool> in Cline; the KB's matchers and
	// isWriteFindingsTool know mcp__<server>__<tool>.
	mcpName := toolName
	if strings.Contains(toolName, "__") && !strings.HasPrefix(toolName, "mcp__") {
		mcpName = "mcp__" + toolName
	}
	var selected []clineHook
	for _, h := range clineHooks(baseDir, "") {
		if h.clineEvent != event {
			continue
		}
		if h.matcher != nil && event != "TaskStart" && !h.matcher.MatchString(toolName) && !h.matcher.MatchString(mcpName) {
			continue
		}
		selected = append(selected, h)
	}
	if len(selected) == 0 {
		return ClineDispatchResult{}
	}
	cwd := ""
	if len(p.WorkspaceRoots) > 0 {
		cwd = p.WorkspaceRoots[0]
	}
	// TaskStart may fire once per turn (probe P1 is open): the bootstrap hook
	// runs `cartographer sync`, so it runs once per task.
	if event == "TaskStart" && p.TaskID != "" && !clineFirstTaskStart(p.TaskID) {
		return ClineDispatchResult{}
	}
	sh := clineShell()
	if sh == "" {
		fmt.Fprintln(os.Stderr, "cartographer: no sh found to run the Cline hooks (Git for Windows provides one)")
		return ClineDispatchResult{}
	}

	var res ClineDispatchResult
	var ctxs, errs []string
	perHook := clineHookBudget / time.Duration(len(selected))
	for _, h := range selected {
		payload := map[string]any{"hook_event_name": h.spec.Event, "cwd": cwd, "session_id": p.TaskID}
		switch event {
		case "TaskStart":
			payload["source"] = "startup"
		case "UserPromptSubmit":
			payload["prompt"] = p.UserPromptSubmit.Prompt
		default:
			payload["tool_name"] = mcpName
			payload["tool_input"] = clineToolInput(params)
			payload["tool_use_id"] = ""
			if event == "PostToolUse" {
				payload["tool_response"] = p.PostToolUse.Result
			}
		}
		in, err := json.Marshal(payload)
		if err != nil {
			continue
		}
		status, stdout, stderr, ok := runClineHook(sh, clineHookCommand(h), h.dir, cwd, in, perHook)
		if !ok {
			continue
		}
		switch {
		case event == "PreToolUse" && status == 2:
			res.Cancel = true
			if stderr == "" {
				stderr = "blocked by hook " + h.name
			}
			errs = append(errs, stderr)
		case event == "PostToolUse" && status == 2 && stderr != "":
			ctxs = append(ctxs, stderr)
		case (event == "TaskStart" || event == "UserPromptSubmit") && status == 0 && stdout != "":
			ctxs = append(ctxs, stdout)
		}
	}
	res.ContextModification = capRunes(strings.Join(ctxs, "\n\n"), clineMaxContext)
	res.ErrorMessage = strings.Join(errs, "\n")
	return res
}

// clineHookCommand is the command line `sh -c` runs for h: the one registerPiHook
// builds, forward slashes in the executable on Windows (D267, #1632).
func clineHookCommand(h clineHook) string {
	return posixShellHookCommand(resolveHookCommand(h.spec.Command, h.dir), hookHostWindows)
}

// clineToolInput is tool_input: a parameter whose string is a JSON object or
// array is decoded back, a scalar stays the string Cline sent.
func clineToolInput(params map[string]string) map[string]any {
	out := make(map[string]any, len(params))
	for k, v := range params {
		out[k] = v
		if t := strings.TrimSpace(v); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
			var decoded any
			if json.Unmarshal([]byte(t), &decoded) == nil {
				out[k] = decoded
			}
		}
	}
	return out
}

// runClineHook runs `sh -c command` with the payload on stdin, in the
// workspace root when it exists (else the hook's own directory). ok is false for
// a spawn error or a timeout.
func runClineHook(sh, command, hookDir, cwd string, payload []byte, timeout time.Duration) (status int, stdout, stderr string, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := procattr.Hide(exec.CommandContext(ctx, sh, "-c", command))
	cmd.Dir = hookDir
	if fi, err := os.Stat(cwd); cwd != "" && err == nil && fi.IsDir() {
		cmd.Dir = cwd
	}
	cmd.Stdin = bytes.NewReader(payload)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	cmd.WaitDelay = time.Second // a grandchild holding the pipes must not outlive the budget
	err := cmd.Run()
	if ctx.Err() != nil {
		return 0, "", "", false
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &ee):
		status = ee.ExitCode()
	default:
		return 0, "", "", false
	}
	return status, strings.TrimSpace(so.String()), strings.TrimSpace(se.String()), true
}

// clineShell finds the `sh` a KB hook command needs (a Claude-style command,
// the same dependency as Pi's `sh -c`); on Windows, also Git for Windows.
func clineShell() string {
	if p, err := exec.LookPath("sh"); err == nil {
		return p
	}
	if !hookHostWindows {
		return ""
	}
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
		for _, rel := range []string{`Git\usr\bin\sh.exe`, `Git\bin\sh.exe`} {
			if root := os.Getenv(env); root != "" {
				if p := filepath.Join(root, rel); fileExists(p) {
					return p
				}
			}
		}
	}
	return ""
}

// clineFirstTaskStart reports whether this is the first TaskStart of taskID
// within clineMarkerTTL: it creates a marker file in the temp dir (O_EXCL, so a
// planted link is never followed) and removes the stale ones. A temp dir that
// cannot be written answers true: the sync runs, it never silently does not.
func clineFirstTaskStart(taskID string) bool {
	dir := clineTempDir()
	if old, _ := filepath.Glob(filepath.Join(dir, clineMarkerPrefix+"*")); old != nil {
		for _, m := range old {
			if fi, err := os.Lstat(m); err == nil && time.Since(fi.ModTime()) > clineMarkerTTL {
				_ = os.Remove(m)
			}
		}
	}
	sum := sha256.Sum256([]byte(taskID))
	f, err := os.OpenFile(filepath.Join(dir, clineMarkerPrefix+hex.EncodeToString(sum[:])[:16]), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return !errors.Is(err, os.ErrExist)
	}
	_ = f.Close()
	return true
}

func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}
