package provisioning

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// codextrust.go (D193) — Codex ignores a project's `.codex/` layer unless the
// project is trusted.
//
// That makes a Codex workspace projection the one case where writing the files
// correctly is not the same as the projection being active. Reporting it as
// installed would be the false-positive class D189 exists to eliminate, so the
// projection reports itself **inactive** and says what would activate it.
//
// The trust record lives in the user's own `~/.codex/config.toml`, under a
// `[projects."<path>"]` table with a `trust_level` key. Trusting a project is
// the user's decision and `codex` has its own prompt for it, so Cartographer
// reads the record and writes it in exactly one case (D1074): TrustCodexProject,
// called only by `workspace bind codex <dir> --trust`, an explicit flag on a
// command the user ran. It appends that one table and nothing else, outside any
// managed block, and never touches a file that already says anything about the
// project. THIS IS THE SINGLE EXCEPTION to "a hand-curated user file is never
// rewritten": do not extend it to another key, another caller (sync, a timer, a
// hook, a derived binding) or another provider.

// CodexProjectTrusted reports whether codexConfigPath records workspaceDir as a
// trusted project. A missing or unreadable config is "not trusted": absence of
// evidence is never evidence of trust.
//
// The parse is deliberately narrow — it looks for the project's own table
// header and the first trust_level in it — because this is a read of someone
// else's file format and a full TOML parse would fail on syntax Cartographer
// has no business rejecting.
func CodexProjectTrusted(codexConfigPath, workspaceDir string) bool {
	f, err := os.Open(codexConfigPath)
	if err != nil {
		return false
	}
	defer f.Close()

	want := filepath.Clean(workspaceDir)
	inProject := false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inProject = isCodexProjectHeader(line, want)
			continue
		}
		if !inProject {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "trust_level" {
			continue
		}
		v := strings.Trim(strings.TrimSpace(value), `"'`)
		return strings.EqualFold(v, "trusted")
	}
	return false
}

// isCodexProjectHeader reports whether a TOML table header names this project.
func isCodexProjectHeader(line, want string) bool {
	inner := strings.TrimSuffix(strings.TrimPrefix(line, "["), "]")
	rest, ok := strings.CutPrefix(inner, "projects.")
	if !ok {
		return false
	}
	return filepath.Clean(strings.Trim(strings.TrimSpace(rest), `"'`)) == want
}

// CodexConfigPath is where the trust record is read from, given the client base
// dir. It is the same file the global MCP cell writes into.
func CodexConfigPath(baseDir string) string {
	return filepath.Join(baseDir, ".codex", "config.toml")
}

// TrustStatus is the outcome of TrustCodexProject.
type TrustStatus int

const (
	// TrustAlreadyTrusted: the config already records the project as trusted.
	TrustAlreadyTrusted TrustStatus = iota
	// TrustWritten: the table was appended.
	TrustWritten
	// TrustRefused: nothing was written; Reason says why and Line is what the
	// user can add by hand.
	TrustRefused
)

// TrustResult is what TrustCodexProject did. Line is always the exact text to
// add by hand.
type TrustResult struct {
	Status TrustStatus
	Reason string
	Line   string
}

// ManualLine is the text that records workspaceDir as trusted: what the user
// adds to the Codex config by hand, and what TrustCodexProject appends.
func ManualLine(workspaceDir string) string {
	return "[projects." + trustKey(workspaceDir) + "]\ntrust_level = \"trusted\""
}

// trustKey renders a path as a TOML key. A backslash path (Windows) is a literal
// string, which isCodexProjectHeader trims like a basic one; otherwise a basic
// string. Paths the quoted forms cannot carry are rejected by trustableKey
// before anything is written; here they only get a best-effort rendering for
// the line shown to the user.
func trustKey(dir string) string {
	if strings.Contains(dir, `\`) && !strings.ContainsAny(dir, "'\n\r") {
		return "'" + dir + "'"
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(dir, `\`, `\\`), `"`, `\"`) + `"`
}

// trustableKey reports why dir cannot be written as a key, or "".
func trustableKey(dir string) string {
	for _, r := range dir {
		if r == '"' || unicode.IsControl(r) {
			return "the path contains a quote or a control character that a TOML key cannot carry here"
		}
	}
	if strings.Contains(dir, `\`) && strings.Contains(dir, "'") {
		return "the path contains both a backslash and a single quote"
	}
	return ""
}

// TrustCodexProject records workspaceDir as a trusted project in the Codex
// config at codexConfigPath, by appending one table (see the file header: this
// is the only write of its kind and it is not to be extended).
//
// It is textual, minimal and idempotent: the existing bytes are an exact prefix
// of the result, the table is never written inside a managed block (Codex
// rewrites that file and drops comments, markers included, D99) and never when
// the file already says anything about the project or declares `projects` in
// another form (a second declaration is a duplicate key, which stops Codex from
// starting; an explicit "untrusted" is a refusal that is never overridden). The
// path is Lstat-checked component by component (D148) and the write goes
// through a temp file renamed over the original.
func TrustCodexProject(codexConfigPath, workspaceDir string) (TrustResult, error) {
	dir := filepath.Clean(workspaceDir)
	line := ManualLine(dir)
	refuse := func(why string) (TrustResult, error) {
		return TrustResult{Status: TrustRefused, Reason: why, Line: line}, nil
	}

	if CodexProjectTrusted(codexConfigPath, dir) {
		return TrustResult{Status: TrustAlreadyTrusted, Line: line}, nil
	}
	if why := trustableKey(dir); why != "" {
		return refuse(why)
	}
	codexDir := filepath.Dir(codexConfigPath)
	base := filepath.Dir(codexDir)
	if err := safewrite.CheckPath(base, codexConfigPath); err != nil {
		return refuse(err.Error() + " (a dotfile manager often links this file; add the lines yourself)")
	}

	existing, err := os.ReadFile(codexConfigPath)
	mode := os.FileMode(0o600)
	switch {
	case err == nil:
		if fi, serr := os.Stat(codexConfigPath); serr == nil {
			mode = fi.Mode().Perm()
		}
		if why := conflictingDeclaration(string(existing), dir); why != "" {
			return refuse(why)
		}
	case os.IsNotExist(err):
		existing = nil
	default:
		return TrustResult{Line: line}, fmt.Errorf("read %s: %w", codexConfigPath, err)
	}

	out := append([]byte(nil), existing...)
	if len(out) > 0 {
		if out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
		out = append(out, '\n')
	}
	out = append(out, line...)
	out = append(out, '\n')

	if err := safewrite.MkdirAllNoFollow(base, codexDir, 0o755); err != nil {
		return TrustResult{Line: line}, err
	}
	if err := replaceFile(codexConfigPath, out, mode); err != nil {
		return TrustResult{Line: line}, err
	}
	return TrustResult{Status: TrustWritten, Line: line}, nil
}

// conflictingDeclaration returns why content must not get a second declaration
// for dir, or "". The tests are line-prefix and substring tests: no TOML parser.
// Any mention of the path counts, a comment included (the conservative reading).
func conflictingDeclaration(content, dir string) string {
	// The spellings a config may carry for one path: native, TOML-escaped
	// backslashes (a basic string) and forward slashes. All three name the same
	// project to Codex, so a second table under any of them is a duplicate.
	for _, spelling := range []string{dir, strings.ReplaceAll(dir, `\`, `\\`), filepath.ToSlash(dir)} {
		if strings.Contains(content, spelling) {
			return "the config already mentions this path (an existing declaration, possibly \"untrusted\", is never overridden)"
		}
	}
	for _, raw := range strings.Split(content, "\n") {
		l := strings.TrimSpace(raw)
		if strings.HasPrefix(l, "[") {
			inner := strings.TrimSpace(strings.TrimPrefix(l, "["))
			if inner == "projects]" || strings.HasPrefix(inner, "projects ]") {
				return "the config declares a [projects] table of its own"
			}
			continue
		}
		for _, p := range []string{"projects", `"projects"`, "'projects'"} {
			rest, ok := strings.CutPrefix(l, p)
			if !ok {
				continue
			}
			rest = strings.TrimSpace(rest)
			if strings.HasPrefix(rest, ".") || strings.HasPrefix(rest, "=") {
				return "the config declares projects in a form other than a [projects.\"<path>\"] table (a dotted key or an inline table); a second declaration would stop Codex from starting"
			}
		}
	}
	return ""
}

// replaceFile writes data to a temp file beside path and renames it over path,
// so a crash cannot leave a half-written config.
func replaceFile(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config.toml.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	return nil
}
