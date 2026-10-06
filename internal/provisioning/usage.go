package provisioning

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Usage scanning (D326): which skills and agents does a client actually load?
// The server never sees it — a skill reaches a client from the files sync
// materialised, and the client reads them from its own disk — so the only
// evidence is in the client's own session transcripts. This file reads them,
// read-only, and reduces them to an aggregate.
//
// What is read, and what never is: only structured tool-call records (a tool
// name and its arguments, a Kiro file-read operation, a Codex function call).
// A user message, an assistant message's text, a reasoning block or any other
// record is skipped without being decoded into anything the scanner keeps. The
// only strings it compares are lockfile paths and skill names it already
// knows; nothing taken from a transcript leaves this function except a
// timestamp and a count.

// DefaultUsageWindow is how far back a transcript is read: older files are not
// opened, and older events inside a recent file are not counted.
const DefaultUsageWindow = 90 * 24 * time.Hour

// maxTranscriptLine bounds one transcript line. A tool result can embed a
// whole file; a line past this is skipped, never buffered.
const maxTranscriptLine = 16 << 20

// ArtifactUsage is the usage signal for one artifact across the sessions of
// every supported provider.
type ArtifactUsage struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // "skill" or "agent"
	// Provider is the provider of the most recent signal.
	Provider string `json:"provider"`
	// Source is the artifact's provenance as the lockfile records it:
	// "kb:<name>" or "bundle".
	Source string `json:"source"`
	// LastUsed is the latest activation, or — when Count is 0 — the latest
	// time a client loaded the catalogue the artifact was listed in (Codex),
	// which proves it was available, not that it was used.
	LastUsed time.Time `json:"last_used"`
	// Count is the number of activations inside the window, summed over
	// providers. The client computes it; the server replaces, never adds.
	Count int `json:"count"`
}

// UsageKey is the map key ScanUsage uses: kind and name, because a skill and
// an agent may share a name.
func UsageKey(kind, name string) string { return kind + "/" + name }

// usageScanner is one provider's transcript reader. It returns the events it
// found for the artifacts it was handed.
type usageScanner func(base string, arts []usageTarget, cutoff time.Time) []usageEvent

// usageTarget is one managed artifact of one provider: the absolute paths of
// every file the lockfile records for it.
type usageTarget struct {
	kind, name, source string
	paths              []string
}

// usageEvent is one sighting of an artifact in a transcript.
type usageEvent struct {
	key      string // UsageKey
	at       time.Time
	catalog  bool // a catalogue load, not an activation
	provider string
}

// usageScanners declares the coverage of every provider the registry knows: a
// provider with no documented, readable transcript is an explicit stub with
// its reason, so adding support later is a one-function change.
var usageScanners = map[string]usageScanner{
	"claude":      scanClaude,
	"codex":       scanCodex,
	"kiro":        scanKiro,
	"opencode":    scanNone("opencode: no session transcript (a JS plugin sees session events but writes none)"),
	"hermes":      scanNone("hermes: no session transcript, and skills arrive through an inbox, not a scannable directory"),
	"antigravity": scanNone("antigravity: no documented session transcript"),
	"crush":       scanNone("crush: no documented session transcript"),
}

// UsageProviders reports which providers contribute what, for kb_status and
// the docs: full per-activation, partial (catalogue timestamp plus path
// reads), or none.
func UsageProviders() (supported, partial, unsupported []string) {
	supported = []string{"claude", "kiro"}
	partial = []string{"codex"}
	unsupported = []string{"antigravity", "crush", "hermes", "opencode"}
	return
}

func scanNone(reason string) usageScanner {
	return func(string, []usageTarget, time.Time) []usageEvent {
		slog.Debug("usage scan skipped", "reason", reason)
		return nil
	}
}

// ScanUsage reads the local session transcripts of each supported provider
// and returns per-artifact usage, keyed by UsageKey. The lockfile says which
// paths to look for; baseDir is where the clients keep their transcripts
// (the home directory the lockfile lives in). window bounds how far back it
// reads. It is read-only (D148): nothing under a client directory is written.
// usageScan false is the operator's opt-out and returns nil at once.
func ScanUsage(lockfile LockFile, baseDir string, window time.Duration, usageScan bool) (map[string]ArtifactUsage, error) {
	if !usageScan {
		return nil, nil
	}
	if window <= 0 {
		window = DefaultUsageWindow
	}
	cutoff := time.Now().Add(-window)

	// Provider -> targets. A workspace projection's files count for the
	// provider that materialised them, exactly like the global ones.
	targets := map[string]map[string]*usageTarget{}
	addLock := func(provider string, lock Lock) {
		root := LockBaseDir(lock, baseDir)
		for _, m := range lock.Managed {
			if (m.Kind != "skill" && m.Kind != "agent") || m.Path == "" {
				continue
			}
			byKey := targets[provider]
			if byKey == nil {
				byKey = map[string]*usageTarget{}
				targets[provider] = byKey
			}
			k := UsageKey(m.Kind, m.Name)
			t := byKey[k]
			if t == nil {
				t = &usageTarget{kind: m.Kind, name: m.Name, source: m.Source}
				byKey[k] = t
			}
			p := m.Path
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, filepath.FromSlash(p))
			}
			t.paths = append(t.paths, p)
		}
	}
	for provider, lock := range lockfile.Providers {
		addLock(provider, lock)
	}
	for _, ws := range lockfile.Workspaces {
		for provider, lock := range ws.Providers {
			addLock(provider, lock)
		}
	}

	type acc struct {
		usage        ArtifactUsage
		lastActivity time.Time
		lastCatalog  time.Time
		catalogProv  string
	}
	accs := map[string]*acc{}
	providers := make([]string, 0, len(targets))
	for p := range targets {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	for _, provider := range providers {
		scan, known := usageScanners[provider]
		if !known {
			slog.Debug("usage scan skipped", "reason", provider+": provider unknown to the usage scanner")
			continue
		}
		byKey := targets[provider]
		list := make([]usageTarget, 0, len(byKey))
		for _, t := range byKey {
			list = append(list, *t)
		}
		sort.Slice(list, func(i, j int) bool {
			return UsageKey(list[i].kind, list[i].name) < UsageKey(list[j].kind, list[j].name)
		})
		for _, ev := range scan(baseDir, list, cutoff) {
			t := byKey[ev.key]
			if t == nil {
				continue
			}
			a := accs[ev.key]
			if a == nil {
				a = &acc{usage: ArtifactUsage{Name: t.name, Kind: t.kind, Source: t.source}}
				accs[ev.key] = a
			}
			if ev.catalog {
				if ev.at.After(a.lastCatalog) {
					a.lastCatalog, a.catalogProv = ev.at, provider
				}
				continue
			}
			a.usage.Count++
			if ev.at.After(a.lastActivity) {
				a.lastActivity, a.usage.Provider = ev.at, provider
			}
		}
	}
	out := make(map[string]ArtifactUsage, len(accs))
	for key, a := range accs {
		u := a.usage
		switch {
		case u.Count > 0:
			u.LastUsed = a.lastActivity
		case !a.lastCatalog.IsZero():
			u.LastUsed, u.Provider = a.lastCatalog, a.catalogProv
		default:
			continue
		}
		out[key] = u
	}
	return out, nil
}

// --- shared transcript plumbing ---

// transcriptFiles lists the *.jsonl files under root modified after cutoff
// whose base name satisfies match. A missing root is not an error: the client
// is simply not installed here.
func transcriptFiles(root string, cutoff time.Time, match func(name string) bool) []string {
	var files []string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || !d.Type().IsRegular() || !strings.HasSuffix(d.Name(), ".jsonl") || !match(d.Name()) {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil && info.ModTime().After(cutoff) {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files
}

// eachLine streams a file line by line. A line longer than maxTranscriptLine
// is dropped and the stream continues; an unreadable file is skipped.
func eachLine(path string, fn func(line []byte)) {
	f, err := os.Open(path)
	if err != nil {
		slog.Debug("usage scan: transcript unreadable", "err", err)
		return
	}
	defer f.Close()
	r := bufio.NewReaderSize(f, 256<<10)
	var buf []byte
	over := false
	for {
		chunk, err := r.ReadSlice('\n')
		if !over {
			if len(buf)+len(chunk) > maxTranscriptLine {
				over, buf = true, buf[:0]
				slog.Debug("usage scan: oversized transcript line skipped")
			} else {
				buf = append(buf, chunk...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if len(buf) > 0 && !over {
			fn(buf)
		}
		buf, over = buf[:0], false
		if err != nil {
			return
		}
	}
}

// pathNeedles returns the byte strings that mean "this artifact's file was
// touched" in a transcript: each managed path in its native and its
// forward-slash spelling (a Windows client may write either), each plain,
// JSON-escaped once (a path inside a JSON string) and twice (Codex stores a
// call's arguments as a JSON string inside the JSON record, so a Windows
// backslash arrives quadrupled). On a POSIX path every form is the same.
func pathNeedles(paths []string) [][]byte {
	var out [][]byte
	seen := map[string]bool{}
	for _, p := range paths {
		for _, sp := range []string{p, filepath.ToSlash(p)} {
			once := strings.Trim(mustJSON(sp), `"`)
			twice := strings.Trim(mustJSON(once), `"`)
			for _, s := range []string{sp, once, twice} {
				if s != "" && !seen[s] {
					seen[s] = true
					out = append(out, []byte(s))
				}
			}
		}
	}
	return out
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// pathMatcher finds which targets a blob of tool-call arguments names.
type pathMatcher struct {
	targets []usageTarget
	needles [][][]byte // parallel to targets
}

func newPathMatcher(targets []usageTarget) *pathMatcher {
	m := &pathMatcher{targets: targets}
	for _, t := range targets {
		m.needles = append(m.needles, pathNeedles(t.paths))
	}
	return m
}

// match returns the keys of the targets whose path appears in blob, once each.
func (m *pathMatcher) match(blob []byte) []string {
	var keys []string
	for i, t := range m.targets {
		for _, n := range m.needles[i] {
			if bytes.Contains(blob, n) {
				keys = append(keys, UsageKey(t.kind, t.name))
				break
			}
		}
	}
	return keys
}

// parseTranscriptTime reads the timestamp forms the clients write: RFC3339 as
// a string, or Unix seconds / milliseconds as a number.
func parseTranscriptTime(raw json.RawMessage) (time.Time, bool) {
	if len(raw) == 0 {
		return time.Time{}, false
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return time.Time{}, false
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			return t, true
		}
		return time.Time{}, false
	}
	n, err := strconv.ParseFloat(string(raw), 64)
	if err != nil || n <= 0 {
		return time.Time{}, false
	}
	if n > 1e11 { // milliseconds
		return time.UnixMilli(int64(n)), true
	}
	return time.Unix(int64(n), 0), true
}

func fileMTime(path string) time.Time {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// --- Claude Code ---

// claudeNonUseTools are tools whose arguments may name a skill file without
// the agent having used the skill: writing it, or handing text to a
// sub-agent, which is prompt content.
var claudeNonUseTools = map[string]bool{
	"Edit": true, "Write": true, "MultiEdit": true, "NotebookEdit": true,
	"Task": true, "Agent": true, "Skill": true,
}

// scanClaude reads <base>/.claude/projects/**/*.jsonl. Two signals: the
// explicit Skill tool ({skill: "<name>"}), and any other tool call whose
// arguments carry a materialised path (a Read of SKILL.md, a shell cat).
// Sub-agent activations (Task/Agent with subagent_type) count for agents.
func scanClaude(base string, targets []usageTarget, cutoff time.Time) []usageEvent {
	m := newPathMatcher(targets)
	skills, agents := map[string]string{}, map[string]string{}
	for _, t := range targets {
		if t.kind == "skill" {
			skills[t.name] = UsageKey(t.kind, t.name)
		} else {
			agents[t.name] = UsageKey(t.kind, t.name)
		}
	}
	var events []usageEvent
	files := transcriptFiles(filepath.Join(base, ".claude", "projects"), cutoff, func(string) bool { return true })
	for _, file := range files {
		mtime := fileMTime(file)
		eachLine(file, func(line []byte) {
			// Cheap reject before any decoding: a tool call is the only
			// thing this scanner wants.
			if !bytes.Contains(line, []byte(`"tool_use"`)) {
				return
			}
			var rec struct {
				Type      string          `json:"type"`
				Timestamp json.RawMessage `json:"timestamp"`
				Message   struct {
					Content json.RawMessage `json:"content"`
				} `json:"message"`
			}
			if json.Unmarshal(line, &rec) != nil || rec.Type != "assistant" {
				return
			}
			if len(rec.Message.Content) == 0 || rec.Message.Content[0] != '[' {
				return
			}
			at, ok := parseTranscriptTime(rec.Timestamp)
			if !ok {
				at = mtime
			}
			if at.Before(cutoff) {
				return
			}
			var blocks []struct {
				Type  string          `json:"type"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if json.Unmarshal(rec.Message.Content, &blocks) != nil {
				return
			}
			for _, b := range blocks {
				if b.Type != "tool_use" {
					continue
				}
				switch b.Name {
				case "Skill":
					var in struct {
						Skill string `json:"skill"`
					}
					if json.Unmarshal(b.Input, &in) == nil {
						if key, ok := skills[in.Skill]; ok {
							events = append(events, usageEvent{key: key, at: at, provider: "claude"})
						}
					}
				case "Task", "Agent":
					var in struct {
						SubagentType string `json:"subagent_type"`
					}
					if json.Unmarshal(b.Input, &in) == nil {
						if key, ok := agents[in.SubagentType]; ok {
							events = append(events, usageEvent{key: key, at: at, provider: "claude"})
						}
					}
				}
				if claudeNonUseTools[b.Name] {
					continue
				}
				for _, key := range m.match(b.Input) {
					events = append(events, usageEvent{key: key, at: at, provider: "claude"})
				}
			}
		})
	}
	return events
}

// --- Codex ---

// codexCallTypes are the response_item payload types that are tool calls. A
// "message" or "reasoning" item is conversation content and is never read.
var codexCallTypes = map[string]bool{
	"function_call": true, "local_shell_call": true, "custom_tool_call": true, "mcp_tool_call": true,
}

// scanCodex reads <base>/.codex/sessions/**/rollout-*.jsonl. Codex records no
// per-activation event: the world_state event proves the skill catalogue was
// loaded (a catalogue sighting, Count stays 0), and a tool call whose
// arguments carry a materialised path is a real use.
func scanCodex(base string, targets []usageTarget, cutoff time.Time) []usageEvent {
	m := newPathMatcher(targets)
	var events []usageEvent
	files := transcriptFiles(filepath.Join(base, ".codex", "sessions"), cutoff, func(n string) bool { return strings.HasPrefix(n, "rollout-") })
	for _, file := range files {
		mtime := fileMTime(file)
		eachLine(file, func(line []byte) {
			isState := bytes.Contains(line, []byte(`"world_state"`))
			isItem := bytes.Contains(line, []byte(`"response_item"`))
			if !isState && !isItem {
				return
			}
			var rec struct {
				Type      string          `json:"type"`
				Timestamp json.RawMessage `json:"timestamp"`
				Payload   json.RawMessage `json:"payload"`
			}
			if json.Unmarshal(line, &rec) != nil {
				return
			}
			at, ok := parseTranscriptTime(rec.Timestamp)
			if !ok {
				at = mtime
			}
			if at.Before(cutoff) {
				return
			}
			switch rec.Type {
			case "world_state":
				var p struct {
					State struct {
						HostSkills json.RawMessage `json:"host_skills"`
					} `json:"state"`
				}
				if json.Unmarshal(rec.Payload, &p) != nil || !nonEmptyJSON(p.State.HostSkills) {
					return
				}
				for _, t := range targets {
					if t.kind == "skill" {
						events = append(events, usageEvent{key: UsageKey(t.kind, t.name), at: at, catalog: true, provider: "codex"})
					}
				}
			case "response_item":
				var p struct {
					Type      string          `json:"type"`
					Arguments json.RawMessage `json:"arguments"`
					Action    json.RawMessage `json:"action"`
					Input     json.RawMessage `json:"input"`
				}
				if json.Unmarshal(rec.Payload, &p) != nil || !codexCallTypes[p.Type] {
					return
				}
				for _, blob := range [][]byte{p.Arguments, p.Action, p.Input} {
					for _, key := range m.match(blob) {
						events = append(events, usageEvent{key: key, at: at, provider: "codex"})
					}
				}
			}
		})
	}
	return events
}

func nonEmptyJSON(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "[]" && s != "{}" && s != `""`
}

// --- Kiro ---

// scanKiro reads <base>/.kiro/sessions/**/*.jsonl. When the model activates a
// skill it reads the SKILL.md with the built-in file-read tool, recorded as
// kind.BuiltIn.FileRead.operations[].path (a shell tool record carries the
// path in its command instead). Only the `kind` object of a record is looked
// at; message text is not. The record shape is pinned by a fixture and the
// Kiro version it was seen on is declared in docs/harnesses.md.
func scanKiro(base string, targets []usageTarget, cutoff time.Time) []usageEvent {
	m := newPathMatcher(targets)
	var events []usageEvent
	files := transcriptFiles(filepath.Join(base, ".kiro", "sessions"), cutoff, func(string) bool { return true })
	for _, file := range files {
		mtime := fileMTime(file)
		eachLine(file, func(line []byte) {
			if !bytes.Contains(line, []byte(`"kind"`)) {
				return
			}
			var rec struct {
				Timestamp json.RawMessage `json:"timestamp"`
				CreatedAt json.RawMessage `json:"created_at"`
				Kind      json.RawMessage `json:"kind"`
			}
			if json.Unmarshal(line, &rec) != nil || len(rec.Kind) == 0 {
				return
			}
			at, ok := parseTranscriptTime(rec.Timestamp)
			if !ok {
				at, ok = parseTranscriptTime(rec.CreatedAt)
			}
			if !ok {
				at = mtime
			}
			if at.Before(cutoff) {
				return
			}
			var blobs [][]byte
			var kind struct {
				BuiltIn struct {
					FileRead struct {
						Operations []struct {
							Path string `json:"path"`
						} `json:"operations"`
					} `json:"FileRead"`
					ExecuteBash struct {
						Command string `json:"command"`
					} `json:"ExecuteBash"`
					Shell struct {
						Command string `json:"command"`
					} `json:"Shell"`
				} `json:"BuiltIn"`
			}
			if json.Unmarshal(rec.Kind, &kind) != nil {
				return
			}
			for _, op := range kind.BuiltIn.FileRead.Operations {
				blobs = append(blobs, []byte(op.Path))
			}
			blobs = append(blobs, []byte(kind.BuiltIn.ExecuteBash.Command), []byte(kind.BuiltIn.Shell.Command))
			seen := map[string]bool{}
			for _, blob := range blobs {
				if len(blob) == 0 {
					continue
				}
				for _, key := range m.match(blob) {
					if !seen[key] { // one record is one use, however many of its operations name the file
						seen[key] = true
						events = append(events, usageEvent{key: key, at: at, provider: "kiro"})
					}
				}
			}
		})
	}
	return events
}
