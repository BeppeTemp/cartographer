// Package kb implements the data plane of the OKF knowledge base.
// Handles reading, atomic writing, and initialization of a KB on the filesystem.
package kb

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// KB represents an open knowledge base identified by its root on the filesystem.
// Always used as *KB; never copy a KB value (sync.Mutex field).
type KB struct {
	Root       string
	AutoCommit bool // if true, CommitOp creates a git commit after each write
	GitSync    bool // if true, SyncIn/SyncOut fetch/push with the "origin" remote

	// SyncInWindow is the freshness window for SyncIn (D76/WP3): if the last
	// successful SyncIn happened less than SyncInWindow ago, SyncIn is a
	// no-op — avoids a redundant fetch+pull on every write during a burst.
	// Zero disables the window (SyncIn runs on every call, pre-existing
	// behaviour).
	SyncInWindow time.Duration

	// GitAuthorName/GitAuthorEmail set the commit author identity used by
	// CommitOp and conflict-resolution commits. Empty values fall back to
	// defaultGitAuthorName/defaultGitAuthorEmail.
	GitAuthorName  string
	GitAuthorEmail string
	// GitEnv is the per-KB environment (e.g. GIT_SSH_COMMAND,
	// GIT_COMMITTER_NAME/EMAIL) layered onto git subprocesses — see
	// gitx.runGitEnv. Nil means "run with the process environment", the
	// pre-existing behaviour.
	GitEnv []string

	// SopsAgeKeyFile is the path to the SOPS age key file used to decrypt
	// this KB's secrets (e.g. via service_get resolve_secrets). Empty means
	// no per-KB key is configured — secret resolution fails clearly instead
	// of falling back to an ambient key.
	SopsAgeKeyFile string

	// AllowArtifactWrite gates the artifact_write/artifact_delete MCP tools
	// (D71): writing a provisioning artifact (skill/agent/hook/mcp) injects
	// instructions a client agent will execute, so the capability is opt-in
	// per-KB (config.KBSpec.AllowArtifactWrite), not implied by an rw token
	// alone. Default false. artifact_read/artifact_list are unaffected.
	AllowArtifactWrite bool

	// SyncOutDebounce is the debounce window for the async push worker
	// (D76/WP4): when > 0, gitWrap calls SchedulePush instead of SyncOut
	// inline, taking the push off the critical path of a write response.
	// The worker waits SyncOutDebounce after the last SchedulePush signal
	// before actually pushing, coalescing a burst of writes into one push.
	// Zero disables the worker entirely: gitWrap falls back to the
	// pre-existing synchronous SyncOut call (rollback flag) — see
	// pushworker.go.
	SyncOutDebounce time.Duration

	// OnPushConflict, if set, is invoked by the async push worker (see
	// pushworker.go, doAsyncPush) when SyncOut hits a rebase conflict, so
	// the caller (mcpserver.RegisterKBTools wires this at registration
	// time) can route it through the same conflict-registry/degraded
	// handling used for synchronous pushes. If nil, the worker only logs
	// the conflict to stderr.
	OnPushConflict func(*gitx.RebaseConflictError)

	mu sync.Mutex // serialises git operations (see gitsync.go)

	// lastSyncIn is the timestamp of the last successful SyncIn (fetch+pull).
	// It is only ever read/written while the caller holds the git lock
	// (gitWrap runs SyncIn under k.WithGitLock), so no separate mutex is
	// needed. Zero value means "never synced".
	lastSyncIn time.Time

	// pushMu guards all async-push-worker bookkeeping below (see
	// pushworker.go). It is a distinct lock from mu/WithGitLock, which the
	// worker acquires separately (and only around the actual SyncOut call,
	// never while holding pushMu) — see doAsyncPush.
	pushMu sync.Mutex
	// pushStarted is true once the worker goroutine has been launched
	// (lazily, on the first SchedulePush). FlushPush checks this without
	// starting the worker, so it stays a no-op — and starts no goroutine —
	// when SchedulePush was never called (always true if SyncOutDebounce
	// == 0, the rollback flag).
	pushStarted bool
	// pushWake nudges the worker to promptly re-read the state below
	// instead of waiting out a timer or blocking; it carries no
	// information of its own (see the package comment in pushworker.go for
	// why authoritative state lives here rather than in channel sends).
	pushWake chan struct{}
	// pushPending is true when a write has been signalled (SchedulePush)
	// but not yet pushed.
	pushPending bool
	// pushLastSignal is the time of the most recent SchedulePush call;
	// the worker debounces SyncOutDebounce after this timestamp, and every
	// new signal extends it (trailing-edge debounce/coalescing).
	pushLastSignal time.Time
	// pushRunning is true while doAsyncPush is actually executing SyncOut.
	pushRunning bool
	// pushForce, set by FlushPush, tells the worker to push immediately
	// rather than waiting out the remaining debounce window.
	pushForce bool
	// pushWaiters are FlushPush callers waiting on the current pending or
	// in-flight push cycle; each is closed once that cycle completes.
	pushWaiters []chan struct{}
}

// Default git author identity used when GitAuthorName/GitAuthorEmail are
// unset (zero-value KB, e.g. constructed directly by tests).
const (
	defaultGitAuthorName  = "cartographer"
	defaultGitAuthorEmail = "cartographer@localhost"
)

// gitAuthor returns k.GitAuthorName/GitAuthorEmail, falling back to the
// package defaults when either is empty.
func (k *KB) gitAuthor() (name, email string) {
	name, email = k.GitAuthorName, k.GitAuthorEmail
	if name == "" {
		name = defaultGitAuthorName
	}
	if email == "" {
		email = defaultGitAuthorEmail
	}
	return name, email
}

// DataRoot returns the conceptual root of the KB (index.md, log.md, archives).
// Concept paths resolved via ResolvePath are anchored here, not at Root.
// Siblings of data/ (skills/, services/) live directly under Root.
func (kb *KB) DataRoot() string {
	return filepath.Join(kb.Root, "data")
}

// ConceptData holds the result of reading a concept.
type ConceptData struct {
	Content        string
	FrontmatterRaw string
	Body           string
	ContentHash    string
}

// Open opens an existing KB by verifying that index.md exists at the root.
// As a side effect it self-migrates the local git-exclude entry for
// .cartographer/ (D62, ensureInfoExclude) — best-effort, so existing KBs
// created before D62 pick it up on first Open with no operator action.
func Open(root string) (*KB, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("Open: %w", err)
	}
	indexPath := filepath.Join(abs, "data", "index.md")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("%w: %s is not a valid KB (missing data/index.md)", okf.ErrNotFound, abs)
	}
	_ = ensureInfoExclude(abs, ".cartographer/")
	return &KB{Root: abs}, nil
}

// Init initializes a new KB by creating the minimal skeleton:
// data/{index.md,log.md}, skills/, services/, agents/, hooks/.
// If the KB already exists (data/index.md present) it is a no-op.
//
// agents/ and hooks/ are provisioning kinds (internal/provisioning, D48):
// agents/<name>.md is a single-file Claude subagent (source format — translated
// to OpenCode's native frontmatter at materialization time, D55), hooks/<name>/
// is a directory (script + hook.json). Both are optional — a KB predating D48
// with no agents/ or hooks/ directory simply yields zero artifacts of that
// kind (see provisioning.BuildManifest), so this is not a breaking change
// for existing KBs.
//
// Init generates only content directories — no AGENTS.md, no .gitignore
// (D62): the KB is always mediated by the server, never edited directly by
// an agent, so the soft agent-contract file was pure noise. Local-only state
// (.cartographer/) is excluded via .git/info/exclude instead (see
// ensureInfoExclude), never via a versioned .gitignore.
func Init(root string) (*KB, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("Init: %w", err)
	}

	// Create the top-level layout: data/ (concept root) plus its siblings.
	dataDir := filepath.Join(abs, "data")
	for _, d := range []string{dataDir,
		filepath.Join(abs, "skills"),
		filepath.Join(abs, "services"),
		filepath.Join(abs, "agents"),
		filepath.Join(abs, "hooks"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, fmt.Errorf("Init: mkdir %s: %w", filepath.Base(d), err)
		}
	}

	indexPath := filepath.Join(dataDir, "index.md")
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		indexContent := "---\ntype: Index\ntitle: Knowledge Base\n---\n# Index\n\nKB initialized.\n"
		if err := writeFileAtomic(indexPath, []byte(indexContent)); err != nil {
			return nil, fmt.Errorf("Init: write data/index.md: %w", err)
		}
	}

	logPath := filepath.Join(dataDir, "log.md")
	if _, err := os.Stat(logPath); os.IsNotExist(err) {
		if err := writeFileAtomic(logPath, []byte("# Log\n\n")); err != nil {
			return nil, fmt.Errorf("Init: write data/log.md: %w", err)
		}
	}

	// Initialize the KB as a git repository (best-effort).
	// The KB remains valid even if git is unavailable or init fails.
	// WriteConcept does NOT auto-commit: commits remain an explicit operation (commit_gate).
	if !gitx.IsRepo(abs) {
		if initErr := gitx.Init(abs); initErr == nil {
			// Initial commit: ignore ErrNothingToCommit and any other non-fatal error.
			// Init has no KB identity/env to draw on yet (the caller sets those on the
			// returned *KB after Init returns) — use the package defaults.
			_ = gitx.Commit(abs, "init: KB inizializzata", defaultGitAuthorName, defaultGitAuthorEmail)
		}
	}

	// Exclude .cartographer/ (local index/conflict state) via .git/info/exclude —
	// requires .git/ to exist, hence after the git-init step above. Never versioned
	// (D62): unlike a tracked .gitignore, this needs no commit and leaves no trace
	// for a remote clone.
	_ = ensureInfoExclude(abs, ".cartographer/")

	return &KB{Root: abs}, nil
}

// ensureInfoExclude adds entry to <kbDir>/.git/info/exclude if not already
// present (idempotent, exact line match; creates .git/info/ if missing).
// This is git's local-only ignore mechanism (D62): unlike .gitignore it is
// never versioned, so KB-local state (e.g. .cartographer/) never enters
// history and needs no commit. No-op, silently, if kbDir is not a git
// repository, or if .git is not a plain directory — e.g. a "gitdir: ..."
// pointer file, as used by git worktrees — a case intentionally left
// unhandled here.
func ensureInfoExclude(kbDir, entry string) error {
	gitDirInfo, statErr := os.Stat(filepath.Join(kbDir, ".git"))
	if statErr != nil || !gitDirInfo.IsDir() {
		return nil
	}

	infoDir := filepath.Join(kbDir, ".git", "info")
	if err := os.MkdirAll(infoDir, 0o755); err != nil {
		return fmt.Errorf("ensureInfoExclude: mkdir .git/info: %w", err)
	}

	excludePath := filepath.Join(infoDir, "exclude")
	data, readErr := os.ReadFile(excludePath)
	existing := ""
	if readErr == nil {
		existing = string(data)
	} else if !os.IsNotExist(readErr) {
		return fmt.Errorf("ensureInfoExclude: read exclude: %w", readErr)
	}

	for _, line := range strings.Split(existing, "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}

	newContent := existing
	if len(newContent) > 0 && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += entry + "\n"
	return os.WriteFile(excludePath, []byte(newContent), 0o644)
}

// ResolvePath resolves a concept path relative to the KB data root, verifying:
//   - no escape from the base (../)
//   - no absolute path
//
// Paths are anchored at DataRoot() (the conceptual root) by default. The
// services/ tree is a first-class concept root included in WalkConcepts but
// lives as a sibling of data/ under Root, so it is anchored at Root.
func (kb *KB) ResolvePath(relPath string, writeMode bool) (string, error) {
	base := kb.DataRoot()
	clean := filepath.Clean(relPath)
	if clean == "services" || strings.HasPrefix(clean, "services"+string(os.PathSeparator)) {
		base = kb.Root
	}
	return safeJoin(base, relPath)
}

// ResolveRootPath resolves a path relative to the KB root (kb.Root itself,
// not DataRoot()), verifying the same invariants as ResolvePath (no absolute
// path, no escape from the base). Used by the provisioning-artifact tools
// (skills/, agents/, hooks/, mcp/, instructions.md — D71), which live at the
// KB root as siblings of data/, not inside it.
func (kb *KB) ResolveRootPath(relPath string) (string, error) {
	return safeJoin(kb.Root, relPath)
}

// safeJoin joins relPath onto base after cleaning it, rejecting absolute
// paths and any path that would resolve outside base (e.g. via "../").
// Factored out of ResolvePath so ResolveRootPath (D71) shares the exact same
// guard instead of duplicating it.
func safeJoin(base, relPath string) (string, error) {
	if filepath.IsAbs(relPath) {
		return "", fmt.Errorf("%w: absolute path not allowed: %s", okf.ErrInvalidPath, relPath)
	}
	abs := filepath.Join(base, relPath)
	rel, err := filepath.Rel(base, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: path escapes root: %s", okf.ErrInvalidPath, relPath)
	}
	return abs, nil
}

// ReadRaw reads the text of a file by its path relative to the KB root.
func (kb *KB) ReadRaw(relPath string) (string, error) {
	abs, err := kb.ResolvePath(relPath, false)
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", okf.ErrNotFound, relPath)
		}
		return "", fmt.Errorf("ReadRaw %s: %w", relPath, err)
	}
	return string(data), nil
}

// ReadIndex reads the contents of index.md in a folder (path relative to root).
// If folderRelPath is empty, reads the root index.md.
func (kb *KB) ReadIndex(folderRelPath string) (string, error) {
	var indexRel string
	if folderRelPath == "" || folderRelPath == "." {
		indexRel = "index.md"
	} else {
		indexRel = filepath.Join(folderRelPath, "index.md")
	}
	return kb.ReadRaw(indexRel)
}

// ReadConcept reads a concept by ID and returns it with raw frontmatter, body, and hash.
func (kb *KB) ReadConcept(id okf.ConceptID) (*ConceptData, error) {
	relPath, _, err := kb.resolveConceptRelPath(id, false)
	if err != nil {
		return nil, err
	}
	content, err := kb.ReadRaw(relPath)
	if err != nil {
		return nil, err
	}
	fm, body, _ := okf.SplitFrontmatter(content)
	hash := okf.ContentHash(content)
	return &ConceptData{
		Content:        content,
		FrontmatterRaw: fm,
		Body:           body,
		ContentHash:    hash,
	}, nil
}

// resolveConceptRelPath resolves id to the relative path (from the data root)
// of the file that actually holds it, honouring the expanded-concept
// fallback (D77 WP2): a concept born as "<id>.md" can be expanded (see
// ExpandConcept) into a directory "<id>/" whose "index.md" then holds the
// same content under the same ID — expansion never changes an ID, so every
// read/write caller must resolve through here instead of assuming
// okf.IDToPath.
//
// If "<id>.md" does not exist but "<id>/index.md" does, the latter is the
// concept (expanded=true). If neither or only the direct form exists, the
// direct form "<id>.md" is returned (expanded=false) — for a nonexistent
// concept this lets the caller's own I/O surface ErrNotFound as before.
//
// On the write path (writeMode=true), both forms existing at once is
// rejected as ambiguous (expanded_ambiguous) rather than silently preferring
// one; on the read path the direct form wins silently (lint's
// expanded_ambiguous check is the place that surfaces this instead).
func (kb *KB) resolveConceptRelPath(id okf.ConceptID, writeMode bool) (relPath string, expanded bool, err error) {
	direct := okf.IDToPath(id)
	directAbs, err := kb.ResolvePath(direct, false)
	if err != nil {
		return "", false, err
	}
	_, statErr := os.Stat(directAbs)
	directExists := statErr == nil

	expandedRel := filepath.Join(string(id), "index.md")
	expandedAbs, err := kb.ResolvePath(expandedRel, false)
	if err != nil {
		return "", false, err
	}
	_, statErr = os.Stat(expandedAbs)
	expandedExists := statErr == nil

	if directExists && expandedExists && writeMode {
		return "", false, fmt.Errorf("expanded_ambiguous: both %s and %s exist for concept %s", direct, expandedRel, id)
	}
	if !directExists && expandedExists {
		return expandedRel, true, nil
	}
	return direct, false, nil
}

// ListArchives returns the names of first-level subdirectories of the data root,
// excluding reserved files (index.md, log.md, _map.md, _archive.md) and hidden dirs.
func (kb *KB) ListArchives() ([]string, error) {
	entries, err := os.ReadDir(kb.DataRoot())
	if err != nil {
		return nil, fmt.Errorf("ListArchives: %w", err)
	}
	var archives []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		archives = append(archives, name)
	}
	return archives, nil
}

// ListExpanded returns the subdirectories of a map (its expanded concepts;
// path relative to root).
func (kb *KB) ListExpanded(archive string) ([]string, error) {
	archiveAbs, err := kb.ResolvePath(archive, false)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(archiveAbs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: archive %s", okf.ErrNotFound, archive)
		}
		return nil, fmt.Errorf("ListExpanded: %w", err)
	}
	var dossiers []string
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dossiers = append(dossiers, e.Name())
		}
	}
	return dossiers, nil
}

// WriteFileAtomic writes data to relPath atomically (write to temp + rename).
func (kb *KB) WriteFileAtomic(relPath string, data []byte) error {
	abs, err := kb.ResolvePath(relPath, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("WriteFileAtomic: mkdir: %w", err)
	}
	return writeFileAtomic(abs, data)
}

// writeFileAtomic is the internal implementation: writes to a temp file in the same
// directory then renames atomically.
func writeFileAtomic(dest string, data []byte) error {
	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".tmp-wiki-")
	if err != nil {
		return fmt.Errorf("writeFileAtomic: create temp: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writeFileAtomic: write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("writeFileAtomic: close: %w", err)
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("writeFileAtomic: rename: %w", err)
	}
	return nil
}

// AppendLog prepends an entry to log.md (newest-on-top).
// The timestamp is provided by the caller to ensure testability.
func (kb *KB) AppendLog(entry string, ts time.Time) error {
	logPath := filepath.Join(kb.DataRoot(), "log.md")

	existing := ""
	data, err := os.ReadFile(logPath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("AppendLog: read log.md: %w", err)
	}
	if err == nil {
		existing = string(data)
	}

	tsStr := ts.UTC().Format(time.RFC3339)
	entryBlock := fmt.Sprintf("## %s\n\n%s\n\n", tsStr, entry)

	// Insert after the first line (# Log header) if present.
	var newContent string
	if strings.HasPrefix(existing, "# Log\n") {
		rest := existing[6:] // skip "# Log\n"
		rest = strings.TrimLeft(rest, "\n")
		if rest == "" {
			newContent = "# Log\n\n" + entryBlock
		} else {
			newContent = "# Log\n\n" + entryBlock + rest
		}
	} else {
		newContent = entryBlock + existing
	}

	return writeFileAtomic(logPath, []byte(newContent))
}

// LogTail reads the last n entries relevant to relPath. An "entry" starts with "## ".
// If relPath is empty, uses the root log. n=0 uses the default (20).
//
// Entries are never written per-directory (AppendLog always writes to root,
// prefixing "[<path>] " when a path is given — see toolLogAppend): to keep
// them discoverable, a non-empty relPath returns (a) the entries of
// "<relPath>/log.md" if that file exists and has any, followed by (b) the
// root-log entries whose text starts with "[<relPath>] ", up to n total.
func (kb *KB) LogTail(relPath string, n int) (string, error) {
	if n <= 0 {
		n = 20
	}
	relPath = strings.Trim(strings.ReplaceAll(relPath, "\\", "/"), "/")

	if relPath == "" || relPath == "." {
		content, err := kb.ReadRaw("log.md")
		if err != nil {
			return "", err
		}
		return extractLogEntries(content, n), nil
	}

	var entries [][]string

	dirLogRel := filepath.Join(relPath, "log.md")
	if dirContent, err := kb.ReadRaw(dirLogRel); err == nil {
		entries = append(entries, parseLogEntries(dirContent)...)
	} else if !errors.Is(err, okf.ErrNotFound) {
		return "", err
	}

	rootContent, err := kb.ReadRaw("log.md")
	if err != nil {
		return "", err
	}
	prefix := "[" + relPath + "] "
	for _, e := range parseLogEntries(rootContent) {
		if strings.HasPrefix(entryText(e), prefix) {
			entries = append(entries, e)
		}
	}

	if len(entries) > n {
		entries = entries[:n]
	}
	return formatLogEntries(entries), nil
}

// parseLogEntries splits a log.md content into entries; each entry is the
// slice of lines starting with its "## <timestamp>" heading.
func parseLogEntries(content string) [][]string {
	lines := strings.Split(content, "\n")
	var entries [][]string
	var current []string
	inEntry := false

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") {
			if inEntry && len(current) > 0 {
				entries = append(entries, current)
			}
			current = []string{line}
			inEntry = true
		} else if inEntry {
			current = append(current, line)
		}
	}
	if inEntry && len(current) > 0 {
		entries = append(entries, current)
	}
	return entries
}

// entryText returns the first non-empty line of an entry's body, i.e. the
// line right after the "## <timestamp>" heading — where AppendLog puts the
// (possibly "[<path>] "-prefixed) entry text.
func entryText(entry []string) string {
	for _, line := range entry[1:] {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// formatLogEntries renders entries back to the "## ..." block format used by log.md.
func formatLogEntries(entries [][]string) string {
	var sb strings.Builder
	for i, e := range entries {
		sb.WriteString(strings.Join(e, "\n"))
		if i < len(entries)-1 {
			sb.WriteString("\n\n")
		}
	}
	return strings.TrimRight(sb.String(), "\n")
}

// extractLogEntries extracts the first n entries (## ...) from a log.md.
func extractLogEntries(content string, n int) string {
	entries := parseLogEntries(content)
	if len(entries) > n {
		entries = entries[:n]
	}
	return formatLogEntries(entries)
}

// ExpandedCount counts the subdirectories of an archive (for atlas_overview).
func (kb *KB) ExpandedCount(archive string) (int, error) {
	dossiers, err := kb.ListExpanded(archive)
	if err != nil {
		return 0, err
	}
	return len(dossiers), nil
}

// ConceptCount recursively counts the non-reserved .md files (concepts) inside an
// archive, regardless of nesting depth (for atlas_overview).
func (kb *KB) ConceptCount(archive string) (int, error) {
	archiveAbs, err := kb.ResolvePath(archive, false)
	if err != nil {
		return 0, err
	}
	count := 0
	err = filepath.WalkDir(archiveAbs, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if filepath.Ext(d.Name()) != ".md" {
			return nil
		}
		if okf.IsReserved(filepath.Base(path)) {
			return nil
		}
		count++
		return nil
	})
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("%w: archive %s", okf.ErrNotFound, archive)
		}
		return 0, fmt.Errorf("ConceptCount: %w", err)
	}
	return count, nil
}

// ValidationError describes a single OKF validation error.
type ValidationError struct {
	Path    string // path relative to the KB
	Message string
}

// maxConceptDepth is the maximum number of ConceptID segments allowed for
// concepts written under data/ (archivio/dossier/concept — see
// docs/data-plane.md §Gerarchia). Enforced by WriteConcept (D72 WP4);
// concept_move inherits it because it writes via WriteConcept. Reads are
// unaffected, so legacy KBs with deeper paths remain readable. The
// services/ tree (a sibling of data/, resolved at kb.Root — see
// ResolvePath) is exempt: it has its own, shallower shape.
const maxConceptDepth = 3

// isServicesID reports whether id belongs to the services/ tree, which is
// exempt from the data/ depth guard (see maxConceptDepth) and from implicit
// dossier stubbing (it has no archivio/dossier structure).
func isServicesID(id okf.ConceptID) bool {
	idStr := string(id)
	return idStr == "services" || strings.HasPrefix(idStr, "services/")
}

// titleFromKebab derives a title from a kebab-case name by splitting on "-"
// and capitalizing each word (e.g. "smart-home" -> "Smart Home"). Used for
// the index.md stub generated on implicit dossier creation (D72 WP4).
func titleFromKebab(name string) string {
	parts := strings.Split(name, "-")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

// stubExpandedIndex writes an index.md for an implicitly created
// expanded-concept directory (D72 WP4): type Index, title derived from the
// kebab-case directory name. Called from WriteConcept only when the
// directory itself did not exist before the triggering write, so it can
// never overwrite an existing index.md. Writes via writeFileAtomic directly
// (not WriteConcept) to avoid recursing into the depth guard / stub logic
// above.
func stubExpandedIndex(dirAbs string) error {
	title := titleFromKebab(filepath.Base(dirAbs))
	content := "---\ntype: Index\ntitle: " + title + "\n---\n# " + title + "\n"
	return writeFileAtomic(filepath.Join(dirAbs, "index.md"), []byte(content))
}

// WriteConcept writes a concept to the KB with OKF semantic validation and optimistic concurrency.
// If ifMatch is non-empty and the file exists, the current ContentHash must match ifMatch.
// If ifMatch is non-empty and the file does not exist, returns ErrStaleWrite.
// If ifMatch is empty, overwrites without concurrency check.
// Returns the hash of the content actually written.
//
// D72 WP4: two additional invariants on the write path (not on reads, so
// legacy KBs remain readable as-is):
//   - depth guard: concepts under data/ (excluding services/) are capped at
//     maxConceptDepth segments (map/concept/child);
//   - implicit expansion stubbing: if this write creates a new map/concept
//     directory that did not exist before, an index.md stub is generated for
//     it (see stubExpandedIndex), so index_get never fails on a real
//     expanded concept.
func (kb *KB) WriteConcept(id okf.ConceptID, fm *okf.Frontmatter, body string, ifMatch string) (string, error) {
	if id == "" {
		return "", fmt.Errorf("%w: empty ConceptID", okf.ErrInvalidConcept)
	}

	relPath, expanded, err := kb.resolveConceptRelPath(id, true)
	if err != nil {
		return "", err
	}

	// The reserved-name check applies to the direct form only: an expanded
	// concept legitimately resolves to "<id>/index.md", whose base name
	// (index.md) is otherwise reserved (D77 WP2).
	if !expanded && okf.IsReserved(filepath.Base(relPath)) {
		return "", fmt.Errorf("%w: %s is a reserved file", okf.ErrInvalidConcept, filepath.Base(relPath))
	}

	if fm.Type() == "" {
		return "", fmt.Errorf("%w: type field is required", okf.ErrInvalidConcept)
	}

	inServices := isServicesID(id)
	segments := strings.Split(string(id), "/")
	if !inServices && len(segments) > maxConceptDepth {
		return "", fmt.Errorf("%w: concept depth (%d segments) exceeds the max of %d (map/concept/child): %s",
			okf.ErrInvalidPath, len(segments), maxConceptDepth, id)
	}

	absPath, err := kb.ResolvePath(relPath, true)
	if err != nil {
		return "", err
	}

	_, statErr := os.Stat(absPath)
	fileExists := statErr == nil

	if fileExists && ifMatch != "" {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return "", fmt.Errorf("WriteConcept: read existing file: %w", err)
		}
		currentHash := okf.ContentHash(string(data))
		if currentHash != ifMatch {
			return "", fmt.Errorf("%w", okf.ErrStaleWrite)
		}
	} else if !fileExists && ifMatch != "" {
		return "", fmt.Errorf("%w: file not found", okf.ErrStaleWrite)
	}

	// Ensure body ends with \n.
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	content := "---\n" + fm.Serialize() + "\n---\n" + body

	// Detect implicit expansion (map/concept, level 2) *before* MkdirAll:
	// only stub index.md when the expanded directory itself is new.
	var newExpandedDir string
	if !inServices && len(segments) == maxConceptDepth {
		dirAbs := filepath.Dir(absPath)
		if _, err := os.Stat(dirAbs); os.IsNotExist(err) {
			newExpandedDir = dirAbs
		}
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0o755); err != nil {
		return "", fmt.Errorf("WriteConcept: mkdir: %w", err)
	}

	if err := writeFileAtomic(absPath, []byte(content)); err != nil {
		return "", fmt.Errorf("WriteConcept: %w", err)
	}

	if newExpandedDir != "" {
		if err := stubExpandedIndex(newExpandedDir); err != nil {
			return "", fmt.Errorf("WriteConcept: stub expanded index.md: %w", err)
		}
	}

	return okf.ContentHash(content), nil
}

// DeleteConcept permanently removes a concept's file from the KB (its
// "<id>.md" form or, for an expanded concept, its "<id>/index.md" form — see
// resolveConceptRelPath, D77 WP2). Rejects an empty ConceptID and reserved
// files (index.md, log.md, _map.md, _archive.md, AGENTS.md). Returns
// ErrNotFound if the file does not exist. Does not update inbound links or
// any index — callers are responsible for that (see concept_delete in
// mcpserver). Deleting an expanded concept removes only its index.md,
// leaving any satellite concepts under "<id>/" in place.
func (kb *KB) DeleteConcept(id okf.ConceptID) error {
	if id == "" {
		return fmt.Errorf("%w: empty ConceptID", okf.ErrInvalidConcept)
	}

	relPath, expanded, err := kb.resolveConceptRelPath(id, true)
	if err != nil {
		return err
	}

	if !expanded && okf.IsReserved(filepath.Base(relPath)) {
		return fmt.Errorf("%w: %s is a reserved file", okf.ErrInvalidConcept, filepath.Base(relPath))
	}

	absPath, err := kb.ResolvePath(relPath, true)
	if err != nil {
		return err
	}

	if _, err := os.Stat(absPath); os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", okf.ErrNotFound, id)
	}

	if err := os.Remove(absPath); err != nil {
		return fmt.Errorf("DeleteConcept: %w", err)
	}

	return nil
}

// ExpandConcept promotes a concept born as "<id>.md" into a directory
// "<id>/" whose "index.md" holds the same content (D77 WP2): the ID never
// changes — resolveConceptRelPath resolves reads and writes to the new
// location transparently — so no backlink rewrite is needed, unlike
// concept_move. Preconditions:
//   - id has exactly two segments (map/concept): expanding a child would let
//     its own children exceed maxConceptDepth once it grows satellites;
//   - the concept exists in its direct "<id>.md" form;
//   - "<id>/" does not already exist (the concept is not already expanded).
//
// The inverse (concept_collapse) is intentionally not implemented (YAGNI —
// see docs/decisions.md D77).
func (kb *KB) ExpandConcept(id okf.ConceptID) error {
	segments := strings.Split(string(id), "/")
	if len(segments) != 2 {
		return fmt.Errorf("%w: concept_expand requires an id with exactly 2 segments (map/concept), got %d: %s",
			okf.ErrInvalidPath, len(segments), id)
	}

	direct := okf.IDToPath(id)
	directAbs, err := kb.ResolvePath(direct, true)
	if err != nil {
		return err
	}
	dirAbs, err := kb.ResolvePath(string(id), true)
	if err != nil {
		return err
	}

	// "already expanded" takes precedence over "not found": once expanded,
	// "<id>.md" no longer exists (it was moved), so checking direct-existence
	// first would misreport a second expand attempt as not_found.
	if _, statErr := os.Stat(dirAbs); statErr == nil {
		return fmt.Errorf("already_expanded: %s is already expanded", id)
	}
	if _, statErr := os.Stat(directAbs); os.IsNotExist(statErr) {
		return fmt.Errorf("%w: concept %s", okf.ErrNotFound, id)
	}

	if err := os.MkdirAll(dirAbs, 0o755); err != nil {
		return fmt.Errorf("ExpandConcept: mkdir: %w", err)
	}
	if err := os.Rename(directAbs, filepath.Join(dirAbs, "index.md")); err != nil {
		return fmt.Errorf("ExpandConcept: move %s: %w", id, err)
	}
	return nil
}

// mapDescriptorCandidates are the filenames a Map/Journal descriptor can
// have, in precedence order (D77 WP1): "_map.md" is the current shape;
// "_archive.md" is read-compat for KBs predating the Atlas/Map/Journal
// rename (never migrated automatically — see docs/plans/atlas-hierarchy.md
// WP6).
var mapDescriptorCandidates = []string{"_map.md", "_archive.md"}

// mapDescriptorRelPath returns the relative path (from the data root) of the
// existing descriptor for the given archive/map directory, preferring
// "_map.md" over the legacy "_archive.md". If neither exists, it returns the
// current form's path so the caller's own read surfaces ErrNotFound.
func (kb *KB) mapDescriptorRelPath(archive string) (string, error) {
	for _, name := range mapDescriptorCandidates {
		rel := filepath.Join(archive, name)
		abs, err := kb.ResolvePath(rel, false)
		if err != nil {
			return "", err
		}
		if _, statErr := os.Stat(abs); statErr == nil {
			return rel, nil
		}
	}
	return filepath.Join(archive, mapDescriptorCandidates[0]), nil
}

// CreateMap creates a map or journal with minimal structure: _map.md,
// index.md, log.md (D77 WP1 — replaces the former CreateArchive/
// "_archive.md" pair, which is now read-compat only, never written).
// name must be a kebab-case segment; the map must not already exist.
// kind must be "map" or "journal"; empty defaults to "map". If ontologyMode
// is empty, defaults to "flexible".
func (kb *KB) CreateMap(name, title, kind string, conceptTypes []string, ontologyMode string) error {
	if _, err := okf.PathToID(name + ".md"); err != nil {
		return fmt.Errorf("%w: invalid map name %q", okf.ErrInvalidPath, name)
	}

	mapAbs := filepath.Join(kb.DataRoot(), name)
	if _, err := os.Stat(mapAbs); err == nil {
		return fmt.Errorf("CreateMap: map %q already exists", name)
	}

	if kind == "" {
		kind = "map"
	}
	if kind != "map" && kind != "journal" {
		return fmt.Errorf("CreateMap: invalid kind %q (must be \"map\" or \"journal\")", kind)
	}

	if ontologyMode == "" {
		ontologyMode = "flexible"
	}

	if err := os.MkdirAll(mapAbs, 0o755); err != nil {
		return fmt.Errorf("CreateMap: mkdir: %w", err)
	}

	var mapFM strings.Builder
	mapFM.WriteString("type: Map\n")
	mapFM.WriteString("title: " + title + "\n")
	mapFM.WriteString("kind: " + kind + "\n")
	if len(conceptTypes) > 0 {
		mapFM.WriteString("concept_types: [" + strings.Join(conceptTypes, ", ") + "]\n")
	}
	mapFM.WriteString("ontology_mode: " + ontologyMode + "\n")
	mapMD := "---\n" + strings.TrimRight(mapFM.String(), "\n") + "\n---\n# " + title + "\n"
	if err := writeFileAtomic(filepath.Join(mapAbs, "_map.md"), []byte(mapMD)); err != nil {
		return fmt.Errorf("CreateMap: write _map.md: %w", err)
	}

	indexMD := "---\ntype: Index\ntitle: " + title + "\n---\n# " + title + "\n"
	if err := writeFileAtomic(filepath.Join(mapAbs, "index.md"), []byte(indexMD)); err != nil {
		return fmt.Errorf("CreateMap: write index.md: %w", err)
	}

	if err := writeFileAtomic(filepath.Join(mapAbs, "log.md"), []byte("# Log\n\n")); err != nil {
		return fmt.Errorf("CreateMap: write log.md: %w", err)
	}

	return nil
}

// ReadArchiveMeta reads and parses the frontmatter of an archive/map's
// descriptor, preferring "_map.md" over the legacy "_archive.md"
// (mapDescriptorRelPath, D77 WP1). A legacy "_archive.md" with no explicit
// "kind" field is treated as kind: map, so callers never see a Map without a
// kind.
func (kb *KB) ReadArchiveMeta(archive string) (*okf.Frontmatter, error) {
	relPath, err := kb.mapDescriptorRelPath(archive)
	if err != nil {
		return nil, fmt.Errorf("ReadArchiveMeta %s: %w", archive, err)
	}
	content, err := kb.ReadRaw(relPath)
	if err != nil {
		return nil, fmt.Errorf("ReadArchiveMeta %s: %w", archive, err)
	}
	fmRaw, _, ok := okf.SplitFrontmatter(content)
	if !ok {
		return nil, fmt.Errorf("ReadArchiveMeta %s: missing frontmatter in %s", archive, filepath.Base(relPath))
	}
	parsed, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return nil, fmt.Errorf("ReadArchiveMeta %s: %w", archive, err)
	}
	if _, hasKind := parsed.Get("kind"); !hasKind {
		parsed.Set("kind", "map")
	}
	return parsed, nil
}

// Validate validates .md files in the scope (path relative to the KB; if empty, the entire KB).
// Returns the list of validation errors without stopping at the first one.
// Returns a Go error only for serious I/O errors.
func (kb *KB) Validate(scope string) ([]ValidationError, error) {
	if scope == "" {
		scope = "."
	}

	files, err := kb.listMDFiles(scope)
	if err != nil {
		return nil, fmt.Errorf("Validate: list files: %w", err)
	}

	// Archive metadata cache: name → frontmatter (nil if not found or unreadable).
	archiveMeta := map[string]*okf.Frontmatter{}

	var errs []ValidationError

	for _, rel := range files {
		base := filepath.Base(rel)

		// index.md and log.md: only verify they are non-empty.
		if base == "index.md" || base == "log.md" {
			content, err := kb.ReadRaw(rel)
			if err != nil {
				return nil, fmt.Errorf("Validate: read %s: %w", rel, err)
			}
			if strings.TrimSpace(content) == "" {
				errs = append(errs, ValidationError{Path: rel, Message: "empty file"})
			}
			continue
		}

		// _map.md (current) / _archive.md (legacy, D77 WP1): verify it has
		// frontmatter with the type matching its descriptor shape.
		if base == "_map.md" || base == "_archive.md" {
			content, err := kb.ReadRaw(rel)
			if err != nil {
				return nil, fmt.Errorf("Validate: read %s: %w", rel, err)
			}
			fmRaw, _, ok := okf.SplitFrontmatter(content)
			if !ok {
				errs = append(errs, ValidationError{Path: rel, Message: "missing frontmatter"})
				continue
			}
			parsed, err := okf.ParseFrontmatter(fmRaw)
			if err != nil {
				errs = append(errs, ValidationError{Path: rel, Message: "unparseable frontmatter: " + err.Error()})
				continue
			}
			wantType := "Map"
			if base == "_archive.md" {
				wantType = "Archive" // legacy descriptor shape, pre-D77
			}
			if parsed.Type() != wantType {
				errs = append(errs, ValidationError{Path: rel, Message: "type: expected " + wantType + ", got: " + parsed.Type()})
			}
			continue
		}

		// Normal concept: verify frontmatter and required type field.
		content, err := kb.ReadRaw(rel)
		if err != nil {
			return nil, fmt.Errorf("Validate: read %s: %w", rel, err)
		}

		fmRaw, _, ok := okf.SplitFrontmatter(content)
		if !ok {
			errs = append(errs, ValidationError{Path: rel, Message: "missing or unparseable frontmatter"})
			continue
		}

		parsed, err := okf.ParseFrontmatter(fmRaw)
		if err != nil {
			errs = append(errs, ValidationError{Path: rel, Message: "unparseable frontmatter: " + err.Error()})
			continue
		}

		if parsed.Type() == "" {
			errs = append(errs, ValidationError{Path: rel, Message: "type field is required"})
			continue
		}

		// Check strict ontology for concepts inside an archive.
		sep := string(filepath.Separator)
		parts := strings.SplitN(rel, sep, 2)
		if len(parts) < 2 {
			continue // top-level file: no archive to check
		}
		archiveName := parts[0]

		// Load (or use cached) archive metadata.
		if _, cached := archiveMeta[archiveName]; !cached {
			meta, readErr := kb.ReadArchiveMeta(archiveName)
			if readErr != nil {
				archiveMeta[archiveName] = nil // archive without a descriptor: skip ontology
			} else {
				archiveMeta[archiveName] = meta
			}
		}

		meta := archiveMeta[archiveName]
		if meta == nil {
			continue
		}

		ontMode, _ := meta.Get("ontology_mode")
		modeStr, _ := ontMode.(string)
		if modeStr == "strict" {
			ctVal, ok := meta.Get("concept_types")
			if ok {
				ctList, ok := ctVal.([]string)
				if ok {
					allowed := make(map[string]bool, len(ctList))
					for _, ct := range ctList {
						allowed[ct] = true
					}
					if !allowed[parsed.Type()] {
						errs = append(errs, ValidationError{
							Path:    rel,
							Message: fmt.Sprintf("type %q not allowed in archive %s (strict)", parsed.Type(), archiveName),
						})
					}
				}
			}
		}
	}

	return errs, nil
}

// listMDFiles recursively lists .md files in a directory relative to the data root.
// Returned paths are relative to DataRoot() so they can be passed back to ReadRaw.
func (kb *KB) listMDFiles(relDir string) ([]string, error) {
	absDir, err := kb.ResolvePath(relDir, false)
	if err != nil {
		return nil, err
	}
	var files []string
	err = filepath.WalkDir(absDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(p, ".md") {
			rel, _ := filepath.Rel(kb.DataRoot(), p)
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}
