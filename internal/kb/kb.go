// Package kb implements the data plane of the OKF knowledge base.
// Handles reading, atomic writing, and initialization of a KB on the filesystem.
package kb

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	gopath "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// KB represents an open knowledge base identified by its root on the filesystem.
// Always used as *KB; never copy a KB value (sync.Mutex field).
type KB struct {
	Root string
	// AuthName is the mounted logical name used by authorization policy. It is
	// set by the transport at mount time and never contains a bearer secret.
	AuthName   string
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
	// GitAuthorExplicit is true only when Cartographer configuration supplied a
	// complete identity. When false CommitOp lets Git resolve its native author.
	GitAuthorExplicit bool
	// GitEnv is the per-KB environment (e.g. GIT_SSH_COMMAND,
	// GIT_COMMITTER_NAME/EMAIL) layered onto git subprocesses — see
	// gitx.runGitEnv. Nil means "run with the process environment", the
	// pre-existing behaviour.
	GitEnv []string

	// ServerGit is non-nil only for the opt-in server profile. Its dedicated
	// working branch is the only branch this process may push (D117).
	ServerGit *ServerGitConfig
	// ServerMergeLint is wired by the MCP layer to run the full lint during a
	// server-profile PR finalization. Keeping it injectable avoids a kb↔lint
	// import cycle while preserving an in-lock gate.
	ServerMergeLint func() error

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

	// SiblingRoots maps every other KB the same server mounts to its absolute
	// root (D316). Set by the HTTP server at mount time when it mounts more
	// than one KB; nil otherwise. Lint reads it for cross_kb_path: an
	// artifact hard-coding a sibling's local path breaks on every other
	// machine. Injected rather than discovered, so lint stays a function of
	// its inputs.
	SiblingRoots map[string]string

	// AutoRepair and DoctorIntervalDays are the per-KB doctor settings
	// (D299, config.KBSpec): the checks `kb repair --apply` may apply
	// unattended, and the days after the last kb-doctor session before the
	// next is proposed (0 = never proposed).
	AutoRepair         []string
	DoctorIntervalDays int

	// UsageStaleDays is the per-KB threshold (D326, config.KBSpec) past which
	// an artifact no client has used is reported by the artifact_unused lint:
	// 0 disables the finding. Set by serve; a KB opened any other way has it
	// off, which is the safe reading of "nobody asked".
	UsageStaleDays int

	// ToolPrefix is the effective MCP tool-name prefix this KB was mounted
	// with (D102), or empty when unprefixed. Discovered is true when the KB was
	// found by scanning the data directory rather than declared in a kbs[]
	// entry, in which case every per-KB setting sits at its zero value and no
	// configuration can change that without adding the entry.
	//
	// Both exist so kb_status can answer "what am I allowed to do in this KB,
	// and what is switched off" — a question an agent could not previously ask
	// from any client surface (D151).
	ToolPrefix string
	Discovered bool

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

	// OnSyncIn, if set, runs after a successful SyncIn that changed HEAD.
	// mcpserver uses it to reconcile derived indexes with pulled KB files.
	OnSyncIn func()

	mu sync.Mutex // serialises git operations (see gitsync.go)

	// lastSyncIn is the timestamp of the last successful SyncIn (fetch+pull).
	// Read-side sync checks it before taking mu, so lastSyncInMu protects this
	// small freshness cache independently of the git-operation lock. Zero value
	// means "never synced".
	lastSyncInMu sync.RWMutex
	lastSyncIn   time.Time
	// lastFetchFail is when SyncIn's fetch last failed (same lock). Reads skip
	// the fetch for ReadFetchBackoff after it, so a remote that is down costs
	// one bounded fetch, not one per queued call (#348).
	lastFetchFail time.Time
	// remoteDefault is the canonical branch last resolved from the remote by
	// a local-profile sync (same lock): the remote's default branch, "" when
	// unknown (D264).
	remoteDefault string
	// gitattrsChecked records that ensureGitAttributes ran in this process
	// (D311). Guarded by mu, the git-operation lock.
	gitattrsChecked bool

	gitStatusMu sync.RWMutex
	gitStatus   GitStatus

	// pushMu guards all async-push-worker bookkeeping below (see
	// pushworker.go). It is a distinct lock from mu/WithGitLock, which the
	// worker acquires separately (and only around the actual SyncOut call,
	// never while holding pushMu) — see doAsyncPush.
	pushMu sync.Mutex

	// graphMu guards graph, the stat-validated link-graph cache (D241). The
	// views it publishes are immutable, so readers hold it only to validate.
	graphMu sync.Mutex
	graph   *graphCache
	// prMu guards prCache, the whole-KB PageRank percentiles of one graph
	// view generation (D251).
	prMu    sync.Mutex
	prCache *pageRankPercentiles
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

// GitStatus is the durable-in-process snapshot exposed by sync_status.
// UnpushedCommits is nil when no trustworthy remote-tracking comparison exists.
type GitStatus struct {
	State           string     `json:"state"`
	LastError       string     `json:"last_error,omitempty"`
	LastAttemptAt   *time.Time `json:"last_attempt_at,omitempty"`
	HeadSHA         string     `json:"head_sha,omitempty"`
	UnpushedCommits *int       `json:"unpushed_commits"`
	IdentityWarning bool       `json:"identity_warning,omitempty"`
	Attempts        int        `json:"attempts"`
	// Branch is the checked-out branch ("" when HEAD is detached).
	Branch string `json:"branch,omitempty"`
	// RemoteDefaultBranch is the KB's canonical branch as last resolved from
	// the remote ("" when unknown); a Branch that differs blocks writes (D264).
	RemoteDefaultBranch string `json:"remote_default_branch,omitempty"`
}

// Default git author identity used when GitAuthorName/GitAuthorEmail are
// unset (zero-value KB, e.g. constructed directly by tests).
const (
	defaultGitAuthorName  = "cartographer"
	defaultGitAuthorEmail = "cartographer@localhost"

	// DefaultGitAuthorName/DefaultGitAuthorEmail are the same values, exported
	// so a caller resolving an identity can tell whether it fell back to the
	// product default and warn before a forge rejects the push (D156).
	DefaultGitAuthorName  = defaultGitAuthorName
	DefaultGitAuthorEmail = defaultGitAuthorEmail
)

// gitAuthor returns k.GitAuthorName/GitAuthorEmail, falling back to the
// package defaults when either is empty.
func (k *KB) gitAuthor() (name, email string) {
	return k.GitAuthorName, k.GitAuthorEmail
}

// SetGitStatus records a status transition without taking the git-operation lock.
func (k *KB) SetGitStatus(state string, err error) {
	k.setGitStatus(state, err, 0)
}

func (k *KB) setGitStatus(state string, err error, attempts int) {
	now := time.Now().UTC()
	s := GitStatus{State: state, LastAttemptAt: &now, Attempts: attempts}
	if err != nil {
		s.LastError = err.Error()
	}
	if sha, shaErr := gitx.HeadSHA(k.Root); shaErr == nil {
		s.HeadSHA = sha
	}
	if branch, _ := gitx.Branch(k.Root); branch != "" {
		if n, known, countErr := gitx.AheadCount(k.Root, "origin", branch); countErr == nil && known {
			s.UnpushedCommits = &n
		}
	}
	_, remote := k.hasRemote()
	s.IdentityWarning = ShouldWarnGitIdentity(k.GitSync, remote, k.GitAuthorEmail)
	k.gitStatusMu.Lock()
	k.gitStatus = s
	k.gitStatusMu.Unlock()
}

// GitStatusSnapshot recomputes divergence for an accurate post-restart view.
func (k *KB) GitStatusSnapshot() GitStatus {
	k.gitStatusMu.RLock()
	s := k.gitStatus
	k.gitStatusMu.RUnlock()
	if s.State == "" {
		if !k.GitSync || !gitx.IsRepo(k.Root) {
			s.State = "disabled"
		} else if _, ok := k.hasRemote(); !ok {
			s.State = "no_remote"
		} else {
			s.State = "clean"
		}
	}
	if sha, err := gitx.HeadSHA(k.Root); err == nil {
		s.HeadSHA = sha
	}
	s.UnpushedCommits = nil
	if branch, _ := gitx.Branch(k.Root); branch != "" {
		if n, known, err := gitx.AheadCount(k.Root, "origin", branch); err == nil && known {
			s.UnpushedCommits = &n
			if s.State == "clean" && n > 0 {
				s.State = "pending"
			}
		}
	}
	s.Branch, _ = gitx.Branch(k.Root)
	s.RemoteDefaultBranch = k.RemoteDefaultBranch()
	_, remote := k.hasRemote()
	s.IdentityWarning = ShouldWarnGitIdentity(k.GitSync, remote, k.GitAuthorEmail)
	return s
}

// ShouldWarnGitIdentity reports whether a synchronised remote KB still uses
// Cartographer's placeholder author identity.
func ShouldWarnGitIdentity(gitSync, hasRemote bool, authorEmail string) bool {
	return gitSync && hasRemote && authorEmail == defaultGitAuthorEmail
}

// MarkPushPending keeps an existing failure visible until a push succeeds,
// and so a "degraded" write on the local base after a failed fetch (D311).
func (k *KB) MarkPushPending() {
	k.gitStatusMu.Lock()
	if k.gitStatus.State != "failed" && k.gitStatus.State != "degraded" {
		k.gitStatus = GitStatus{State: "pending"}
	}
	k.gitStatusMu.Unlock()
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
	// The advisory process lock is local state too (D155): never versioned, and
	// excluded the same way rather than through a tracked .gitignore.
	_ = ensureInfoExclude(abs, LockFileName)
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
// initAuthorName/initAuthorEmail carry the identity InitWithIdentity supplies
// to the KB's initial commit, for the duration of that call. Init leaves them
// empty and the package defaults apply, which is the pre-D156 behaviour.
var (
	initAuthorName  string
	initAuthorEmail string
)

// InitWithIdentity is Init with an explicit author for the KB's initial commit.
// `cartographer kb create` uses it: the initial commit is the one a forge with
// an author-membership push rule rejects, and by then the scaffold already
// exists, so the identity has to be right the first time (D156).
// An empty name or email falls back to the package defaults.
func InitWithIdentity(root, authorName, authorEmail string) (*KB, error) {
	initAuthorName, initAuthorEmail = authorName, authorEmail
	defer func() { initAuthorName, initAuthorEmail = "", "" }()
	return Init(root)
}

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

	// The root index carries the KB's name (D144): on a client that flattens
	// the MCP tool namespace, atlas_overview's first line is what lets an
	// agent notice it is reading the wrong KB. Only on creation — an existing
	// index.md is never rewritten.
	indexPath := filepath.Join(dataDir, "index.md")
	created := false
	if _, err := os.Stat(indexPath); os.IsNotExist(err) {
		created = true
		name := filepath.Base(abs)
		indexContent := "---\ntype: Index\ntitle: " + name + "\n---\n# " + name + "\n\nKB initialized.\n"
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

	// data/.gitignore (D316): junk an editor or an interpreter leaves behind
	// stays out of the KB from the first commit. Only for a KB this call
	// creates (no root index yet): Init on an existing KB must not leave an
	// untracked file for an unrelated write to commit.
	ignorePath := filepath.Join(dataDir, ".gitignore")
	if _, err := os.Lstat(ignorePath); created && os.IsNotExist(err) {
		if err := writeFileAtomic(ignorePath, []byte(dataGitignore())); err != nil {
			return nil, fmt.Errorf("Init: write data/.gitignore: %w", err)
		}
	}

	// Initialize the KB as a git repository (best-effort).
	// The KB remains valid even if git is unavailable or init fails.
	// WriteConcept does NOT auto-commit: commits remain an explicit operation (commit_gate).
	// An existing repository with an unborn HEAD — the clone of an empty
	// remote that server bootstrap produces — gets the same treatment as a new
	// one: HEAD pinned to main and an initial commit. Skipping it left the KB
	// on whatever branch the host's git named, with nothing to push, and the
	// first sync failing on a missing remote ref (D264).
	if !gitx.IsRepo(abs) || gitx.HeadUnborn(abs) {
		if initErr := gitx.Init(abs); initErr == nil {
			// Initial commit. Its error is returned (only ErrNothingToCommit is
			// benign): swallowing it left `kb create` pushing a branch that does
			// not exist, and the push error blamed authentication (D265).
			// The author is the caller's (InitWithIdentity) when it supplied one:
			// a forge with an author push rule rejects the product default, and
			// the rejection lands on the very first push (D156).
			// .gitattributes is part of the initial commit (D311): it gives
			// log.md the union merge on every clone, which a file in
			// .git/info/attributes would not. Only on a new repository —
			// on an existing one it would be left uncommitted.
			if _, err := writeGitAttributes(abs); err != nil {
				return nil, fmt.Errorf("Init: write %s: %w", gitAttributesFile, err)
			}
			name, email := initAuthorName, initAuthorEmail
			if name == "" || email == "" {
				name, email = defaultGitAuthorName, defaultGitAuthorEmail
			}
			if err := gitx.Commit(abs, "init: KB initialized", name, email); err != nil && !errors.Is(err, gitx.ErrNothingToCommit) {
				return nil, fmt.Errorf("Init: initial commit: %w", err)
			}
		}
	}

	// Exclude .cartographer/ (local index/conflict state) via .git/info/exclude —
	// requires .git/ to exist, hence after the git-init step above. Never versioned
	// (D62): unlike a tracked .gitignore, this needs no commit and leaves no trace
	// for a remote clone.
	_ = ensureInfoExclude(abs, ".cartographer/")
	// The advisory process lock is local state too (D155): never versioned, and
	// excluded here as well as in Open so it can never show up as a dirty working
	// tree — which is what blocks a server-profile PR reconciliation.
	_ = ensureInfoExclude(abs, LockFileName)

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

// ServicesNamespace is the first ID segment of the service-descriptor
// namespace. Unlike every map, it is rooted at kb.Root, not DataRoot():
// ResolvePath is the one place that picks the root, and every operation that
// turns a concept ID into a physical path must go through it — a direct
// filepath.Join(kb.DataRoot(), id) lands in data/services/, which no read
// ever looks at (D269). For the same reason it is not a valid map or journal
// name (see CreateMapWithContract).
const ServicesNamespace = "services"

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
	if clean == ServicesNamespace || strings.HasPrefix(clean, ServicesNamespace+string(os.PathSeparator)) {
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
	if isAbsAnyPlatform(relPath) {
		return "", fmt.Errorf("%w: absolute path not allowed: %s", okf.ErrInvalidPath, relPath)
	}
	abs := filepath.Join(base, relPath)
	rel, err := filepath.Rel(base, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%w: path escapes root: %s", okf.ErrInvalidPath, relPath)
	}
	return abs, nil
}

// isAbsAnyPlatform reports whether relPath is absolute in *either* platform's
// spelling. filepath.IsAbs alone answers only for the host, and a KB path is
// not a host path: "/etc/passwd" is a leading-slash escape that a Windows host
// would call relative and happily join onto the KB root, and `C:\Windows\...`
// the same on unix. A KB path is always relative and slash-separated, so
// anything that looks absolute anywhere is rejected everywhere.
func isAbsAnyPlatform(relPath string) bool {
	if relPath == "" {
		return false
	}
	if relPath[0] == '/' || relPath[0] == '\\' {
		return true
	}
	if len(relPath) >= 3 && relPath[1] == ':' && (relPath[2] == '/' || relPath[2] == '\\') {
		c := relPath[0]
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
	}
	return filepath.IsAbs(relPath)
}

// readRawHook, when non-nil, is called with every path ReadRaw is asked for.
// It is nil in production and set only from export_test.go, so a test can
// prove which files an operation reads (D248).
var readRawHook func(relPath string)

// ReadRaw reads the text of a file by its path relative to the KB root.
func (kb *KB) ReadRaw(relPath string) (string, error) {
	if readRawHook != nil {
		readRawHook(relPath)
	}
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
		indexRel = gopath.Join(folderRelPath, "index.md")
	}
	return kb.ReadRaw(indexRel)
}

// IndexHash reads the root or a Map/Journal's curated index.md — the same
// "path" convention as ReadIndex (empty/"." = root) — and returns its
// content alongside its content-hash. It is the bounded read half of the
// index-patch data plane (D122 WP1): unlike ReadIndex, which serves any
// folder including an expanded concept's own index.md, IndexHash accepts
// only the root and an existing Map/Journal descriptor (see
// curatedIndexRelPath) and is what index_patch reads before applying an
// edit.
func (kb *KB) IndexHash(path string) (content, hash string, err error) {
	relPath, err := kb.curatedIndexRelPath(path)
	if err != nil {
		return "", "", err
	}
	content, err = kb.ReadRaw(relPath)
	if err != nil {
		return "", "", err
	}
	return content, okf.ContentHash(content), nil
}

// PatchIndex atomically replaces the content of a root or Map/Journal curated
// index.md — the bounded write half of IndexHash (D122 WP1). ifMatch must
// equal the index's current content-hash (as returned by IndexHash); a
// mismatch fails with ErrStaleWrite and leaves the file untouched. newContent
// is the full, already-materialized replacement text — the MCP layer applies
// old_string/new_string edits before calling this, the same division of
// labor WriteConcept/concept_patch use. Returns the new content-hash.
func (kb *KB) PatchIndex(path, ifMatch, newContent string) (string, error) {
	relPath, err := kb.curatedIndexRelPath(path)
	if err != nil {
		return "", err
	}
	absPath, err := kb.ResolvePath(relPath, true)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("%w: %s", okf.ErrNotFound, relPath)
		}
		return "", fmt.Errorf("PatchIndex: read %s: %w", relPath, err)
	}
	if okf.ContentHash(string(data)) != ifMatch {
		return "", fmt.Errorf("%w", okf.ErrStaleWrite)
	}

	if newContent != "" && !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	if err := writeFileAtomic(absPath, []byte(newContent)); err != nil {
		return "", fmt.Errorf("PatchIndex: %w", err)
	}
	return okf.ContentHash(newContent), nil
}

// curatedIndexRelPath resolves an index-patch "path" argument (root when
// empty, or a Map/Journal name) to its index.md, relative to the data root —
// the bounded resolution shared by IndexHash and PatchIndex (D122 WP1). It
// accepts only:
//   - the root index (""/"."): "index.md";
//   - an existing Map/Journal's index ("<name>", one segment): "<name>/index.md",
//     provided "<name>" carries a _map.md/_archive.md descriptor
//     (mapDescriptorRelPath).
//
// Everything else is rejected: a nested/multi-segment path, a Map/Journal
// name with no descriptor, or a symlink anywhere along the resolved path
// (rejectIndexSymlinks) — ResolvePath's existing traversal/absolute-path
// guard applies transitively since every returned path is reconstructed from
// a validated single segment, never echoed from the caller directly. A path
// that names an existing expanded concept's own index ("<map>/<concept>",
// two segments) is redirected with an actionable error instead of silently
// falling through to "not a Map/Journal": that index belongs to
// concept_patch, not this primitive.
func (kb *KB) curatedIndexRelPath(path string) (string, error) {
	clean := strings.Trim(filepath.ToSlash(filepath.Clean(strings.ReplaceAll(path, "\\", "/"))), "/")
	if clean == "." {
		clean = ""
	}

	if clean == "" {
		if err := rejectIndexSymlinks(kb.DataRoot(), "index.md"); err != nil {
			return "", err
		}
		return "index.md", nil
	}

	segments := strings.Split(clean, "/")
	if len(segments) == 2 {
		if _, expanded, err := kb.resolveConceptRelPath(okf.ConceptID(clean), false); err == nil && expanded {
			return "", fmt.Errorf("expanded_index: %s/index.md belongs to expanded concept %q; use concept_patch(id=%q) instead", clean, clean, clean)
		}
	}
	if len(segments) != 1 {
		return "", fmt.Errorf("%w: %q is not the root or a Map/Journal index", okf.ErrInvalidPath, path)
	}

	archive := segments[0]
	if _, err := okf.PathToID(archive + ".md"); err != nil {
		return "", fmt.Errorf("%w: invalid map/journal name %q", okf.ErrInvalidPath, archive)
	}
	descRel, err := kb.mapDescriptorRelPath(archive)
	if err != nil {
		return "", err
	}
	descAbs, err := kb.ResolvePath(descRel, false)
	if err != nil {
		return "", err
	}
	if _, statErr := os.Stat(descAbs); statErr != nil {
		return "", fmt.Errorf("%w: map/journal %q", okf.ErrNotFound, archive)
	}

	indexRel := gopath.Join(archive, "index.md")
	if err := rejectIndexSymlinks(kb.DataRoot(), indexRel); err != nil {
		return "", err
	}
	return indexRel, nil
}

// rejectIndexSymlinks walks each path component from base and fails if any
// component is a symlink — the same defence used for expanded-concept assets
// (resolveAsset, asset.go) and provisioning artifacts
// (mcpserver.rejectArtifactSymlinks), applied here to the root/Map
// index-patch path.
func rejectIndexSymlinks(base, relPath string) error {
	current := base
	for _, part := range strings.Split(filepath.ToSlash(relPath), "/") {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: symlink in path %q", okf.ErrInvalidPath, relPath)
		}
	}
	return nil
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

// ConceptRelPath returns the data-root-relative path of the file that actually
// holds id, honouring the expanded-concept fallback, plus whether that concept
// is expanded. A concept whose path cannot be resolved yields the direct form
// okf.IDToPath(id) and false, so a caller never has to branch on an error.
//
// It exists for lint (D149), which must resolve a body's relative links against
// the file that contains them: for an expanded concept the ID is "map/concept"
// but the body lives in "map/concept/index.md", and those two bases differ by
// one level.
func (kb *KB) ConceptRelPath(id okf.ConceptID) (relPath string, expanded bool) {
	rel, exp, err := kb.resolveConceptRelPath(id, false)
	if err != nil {
		return okf.IDToPath(id), false
	}
	return rel, exp
}

// ConceptLocation is where a concept ID lives on disk, resolved in its own
// namespace (DataRoot() for maps, kb.Root for services/ — see ResolvePath).
type ConceptLocation struct {
	// File is the absolute path of the file that holds the concept:
	// "<id>.md", or "<id>/index.md" when Expanded. For a concept that does
	// not exist it is the direct form, so a Lstat on it answers "occupied?".
	File string
	// Dir is the absolute path of the expanded-concept directory "<id>/",
	// whether or not it exists (an asset-only directory can exist without an
	// owner concept).
	Dir string
	// Expanded reports that the concept is held by "<id>/index.md".
	Expanded bool
}

// LocateConcept resolves id to its physical location for a caller that
// moves or removes files itself (concept_move). It goes through the same
// resolver as ReadConcept/WriteConcept, so the path it returns is the file a
// read would have returned, in whichever namespace root holds it (D269): a
// caller joining DataRoot() by hand removes data/services/<x>.md while the
// real services/<x>.md survives. Both forms existing at once is refused as
// expanded_ambiguous, as on the write path. It rejects the same absolute and
// escaping IDs as ResolvePath (okf.ErrInvalidPath).
func (kb *KB) LocateConcept(id okf.ConceptID) (ConceptLocation, error) {
	rel, expanded, err := kb.resolveConceptRelPath(id, true)
	if err != nil {
		return ConceptLocation{}, err
	}
	file, err := kb.ResolvePath(rel, true)
	if err != nil {
		return ConceptLocation{}, err
	}
	dir, err := kb.ResolvePath(string(id), true)
	if err != nil {
		return ConceptLocation{}, err
	}
	return ConceptLocation{File: file, Dir: dir, Expanded: expanded}, nil
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

	expandedRel := gopath.Join(string(id), "index.md")
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

	dirLogRel := gopath.Join(relPath, "log.md")
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
	return idStr == ServicesNamespace || strings.HasPrefix(idStr, ServicesNamespace+"/")
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
	return kb.writeConcept(id, fm, body, ifMatch, false)
}

// WriteExpandedConcept writes the index of a new or existing expanded concept
// directly to "<id>/index.md". Unlike WriteConcept, it never falls back to
// the direct "<id>.md" form; callers that intentionally create an expanded
// concept can therefore write its index before any satellite exists.
func (kb *KB) WriteExpandedConcept(id okf.ConceptID, fm *okf.Frontmatter, body string, ifMatch string) (string, error) {
	return kb.writeConcept(id, fm, body, ifMatch, true)
}

func (kb *KB) writeConcept(id okf.ConceptID, fm *okf.Frontmatter, body string, ifMatch string, forceExpanded bool) (string, error) {
	plan, err := kb.prepareWriteConcept(id, fm, body, ifMatch, forceExpanded)
	if err != nil {
		return "", err
	}
	return kb.commitWriteConceptPlan(plan)
}

// writeConceptPlan is the fully-validated, disk-untouched result of
// preparing a single concept write — everything commitWriteConceptPlan
// needs to actually replace file(s). Kept private: besides writeConcept
// itself, WriteConceptBatch (batch.go, D125 WP1/WP2) is the only other
// caller, so it can validate and materialize every operation in a batch
// before any of them touches disk.
type writeConceptPlan struct {
	absPath        string
	content        []byte
	newExpandedDir string // absolute path of a new expanded-concept dir to stub, or ""
}

// prepareWriteConcept performs every WriteConcept validation and content
// materialization without touching disk beyond os.Stat/os.ReadFile: same
// invariants (reserved names, required type, depth guard, ifMatch/
// ErrStaleWrite, expanded-concept resolution) and the same error wrapping,
// split out of writeConcept so a batch of operations (WriteConceptBatch) can
// be fully validated in memory before the first byte is written to disk.
func (kb *KB) prepareWriteConcept(id okf.ConceptID, fm *okf.Frontmatter, body string, ifMatch string, forceExpanded bool) (*writeConceptPlan, error) {
	if id == "" {
		return nil, fmt.Errorf("%w: empty ConceptID", okf.ErrInvalidConcept)
	}

	inServices := isServicesID(id)
	segments := strings.Split(string(id), "/")
	var (
		relPath  string
		expanded bool
		err      error
	)
	if forceExpanded {
		if inServices || len(segments) != 2 {
			return nil, fmt.Errorf("%w: expanded concept requires a map/concept id, got %s", okf.ErrInvalidPath, id)
		}
		directAbs, resolveErr := kb.ResolvePath(okf.IDToPath(id), false)
		if resolveErr != nil {
			return nil, resolveErr
		}
		if _, statErr := os.Stat(directAbs); statErr == nil {
			return nil, fmt.Errorf("expanded_ambiguous: direct concept %s already exists", id)
		}
		relPath, expanded = gopath.Join(string(id), "index.md"), true
	} else {
		relPath, expanded, err = kb.resolveConceptRelPath(id, true)
		if err != nil {
			return nil, err
		}
	}

	// The reserved-name check applies to the direct form only: an expanded
	// concept legitimately resolves to "<id>/index.md", whose base name
	// (index.md) is otherwise reserved (D77 WP2).
	if !expanded && okf.IsReserved(filepath.Base(relPath)) {
		return nil, fmt.Errorf("%w: %s is a reserved file", okf.ErrInvalidConcept, filepath.Base(relPath))
	}

	if fm.Type() == "" {
		return nil, fmt.Errorf("%w: type field is required", okf.ErrInvalidConcept)
	}

	if !inServices && len(segments) > maxConceptDepth {
		return nil, fmt.Errorf("%w: concept depth (%d segments) exceeds the max of %d (map/concept/child): %s",
			okf.ErrInvalidPath, len(segments), maxConceptDepth, id)
	}

	absPath, err := kb.ResolvePath(relPath, true)
	if err != nil {
		return nil, err
	}

	_, statErr := os.Stat(absPath)
	fileExists := statErr == nil

	if fileExists && ifMatch != "" {
		data, err := os.ReadFile(absPath)
		if err != nil {
			return nil, fmt.Errorf("WriteConcept: read existing file: %w", err)
		}
		currentHash := okf.ContentHash(string(data))
		if currentHash != ifMatch {
			return nil, fmt.Errorf("%w", okf.ErrStaleWrite)
		}
	} else if !fileExists && ifMatch != "" {
		return nil, fmt.Errorf("%w: file not found", okf.ErrStaleWrite)
	}

	// Ensure body ends with \n.
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	serialized := fm.Serialize()
	if err := okf.VerifyRoundTrip(serialized); err != nil {
		return nil, fmt.Errorf("%w: %v", okf.ErrInvalidConcept, err)
	}
	content := "---\n" + serialized + "\n---\n" + body

	// Detect implicit expansion (map/concept, level 2) *before* MkdirAll:
	// only stub index.md when the expanded directory itself is new.
	var newExpandedDir string
	if !inServices && len(segments) == maxConceptDepth {
		dirAbs := filepath.Dir(absPath)
		if _, err := os.Stat(dirAbs); os.IsNotExist(err) {
			newExpandedDir = dirAbs
		}
	}

	return &writeConceptPlan{absPath: absPath, content: []byte(content), newExpandedDir: newExpandedDir}, nil
}

// commitWriteConceptPlan performs the actual disk I/O for an already
// validated plan: mkdir, atomic write, and expanded-index stub. Mirrors
// WriteConcept's former commit tail exactly.
func (kb *KB) commitWriteConceptPlan(plan *writeConceptPlan) (string, error) {
	if err := os.MkdirAll(filepath.Dir(plan.absPath), 0o755); err != nil {
		return "", fmt.Errorf("WriteConcept: mkdir: %w", err)
	}

	if err := writeFileAtomic(plan.absPath, plan.content); err != nil {
		return "", fmt.Errorf("WriteConcept: %w", err)
	}

	if plan.newExpandedDir != "" {
		if err := stubExpandedIndex(plan.newExpandedDir); err != nil {
			return "", fmt.Errorf("WriteConcept: stub expanded index.md: %w", err)
		}
	}

	return okf.ContentHash(string(plan.content)), nil
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
// see D77).
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

	// Rebase relative markdown links before moving the file one directory
	// deeper: a link written as "other.md" or "../m2/z.md" relative to
	// "<map>/<c>.md" must be rewritten for "<map>/<c>/index.md", otherwise
	// every outbound link silently breaks. Wiki-links are root-relative and
	// untouched. A test pins this (D295 WP1).
	raw, readErr := os.ReadFile(directAbs)
	if readErr != nil {
		return fmt.Errorf("ExpandConcept: read %s: %w", id, readErr)
	}
	oldBase := string(id) + ".md"
	newBase := string(id) + "/index.md"
	content := string(raw)
	_, body, _ := okf.SplitFrontmatter(content)
	out := content
	if rebased, n := RewriteOutboundLinks(body, oldBase, newBase, nil); n > 0 && strings.HasSuffix(content, body) {
		// Splice the body back so the frontmatter keeps its exact bytes.
		out = content[:len(content)-len(body)] + rebased
	}
	if out != content {
		if err := os.WriteFile(directAbs, []byte(out), 0o644); err != nil {
			return fmt.Errorf("ExpandConcept: rebase write %s: %w", id, err)
		}
	}

	if err := os.Rename(directAbs, filepath.Join(dirAbs, "index.md")); err != nil {
		return fmt.Errorf("ExpandConcept: move %s: %w", id, err)
	}
	return nil
}

// mapDescriptorCandidates are the filenames a Map/Journal descriptor can
// have, in precedence order (D77 WP1): "_map.md" is the current shape;
// "_archive.md" is read-compat for KBs predating the Atlas/Map/Journal
// rename (never migrated automatically — D77 WP6).
var mapDescriptorCandidates = []string{"_map.md", "_archive.md"}

// mapDescriptorRelPath returns the relative path (from the data root) of the
// existing descriptor for the given archive/map directory, preferring
// "_map.md" over the legacy "_archive.md". If neither exists, it returns the
// current form's path so the caller's own read surfaces ErrNotFound.
func (kb *KB) mapDescriptorRelPath(archive string) (string, error) {
	for _, name := range mapDescriptorCandidates {
		rel := gopath.Join(archive, name)
		abs, err := kb.ResolvePath(rel, false)
		if err != nil {
			return "", err
		}
		if _, statErr := os.Stat(abs); statErr == nil {
			return rel, nil
		}
	}
	return gopath.Join(archive, mapDescriptorCandidates[0]), nil
}

// MapContract declares the optional deterministic lint contract of a map.
// RequiredFields apply to every concept; RequiredFieldsByType are additive.
// MachinePathAllowPrefixes lists path prefixes — POSIX-absolute, Windows
// drive-absolute, or "~/"-anchored (D159) — that the machine_path
// lint (D124) must treat as this map's operational target paths rather than
// client-local paths — e.g. a container image's home directory or a remote
// node's runtime path, which are identical across every reader's machine and
// therefore not a false positive.
type MapContract struct {
	OntologyMode             string   // "strict" or "flexible" (default)
	ConceptTypes             []string // allowed types when OntologyMode is "strict"
	RequiredFields           []string
	RequiredFieldsByType     map[string][]string
	FieldValues              map[string][]string
	FieldValuesByType        map[string]map[string][]string
	ForbiddenFields          []string
	RequireIndexEntry        bool
	MachinePathAllowPrefixes []string
	// ValueSynonyms extends the built-in value families for this map
	// (value_synonyms.<canonical>: [synonym, ...], D296).
	ValueSynonyms map[string][]string
	// Lifecycle keys (D297): the map's kind ("map" or "journal"), the
	// statuses that mean "not finished", the age after which such a concept
	// is stale, whether pages must carry their template's H2 sections, and
	// the words that mark an open question.
	Kind             string
	OpenStatuses     []string
	StaleAfterDays   int
	TemplateSections bool
	OpenMarkers      []string
	// Review keys (D298): the map a journal's reusable procedures belong in,
	// the H2 prefixes that mark a procedure, and whether this map is where
	// the KB defines its terms.
	PromoteTo         string
	ProcedureHeadings []string
	Glossary          bool
	// WorkMap (D302) is the map where this map's work belongs: an open item
	// or open-phase concept here with no link into it is scattered_work.
	WorkMap string
	// Cost keys (D301): Index is "generated" when the server maintains the
	// map's concept list in a marked block of its index.md ("" = curated);
	// the integers override the review and oversize thresholds for this map
	// (0 = the built-in default).
	Index           string
	RepeatedFactMin int
	HotspotInDegree int
	HotspotBytes    int
	OversizeBytes   int
	// OversizeConcepts (D313) is the top-level concept count above which the map
	// is map_oversize; 0 keeps the built-in threshold.
	OversizeConcepts int
	Malformed        []ContractMalformed
}

// AllowedValues returns the allowed values declared for field on conceptType:
// the per-type list when present (it replaces the map-wide one, D275), else the
// map-wide list. The bool is false when the contract does not constrain it.
func (c MapContract) AllowedValues(conceptType, field string) ([]string, bool) {
	if vals, ok := c.FieldValuesByType[conceptType][field]; ok {
		return vals, true
	}
	vals, ok := c.FieldValues[field]
	return vals, ok
}

// ContractMalformed identifies a tolerated malformed contract entry.
type ContractMalformed struct {
	Descriptor string
	Key        string
}

// RequiredFor returns the map-wide fields plus fields for conceptType.
func (c MapContract) RequiredFor(conceptType string) []string {
	seen := map[string]bool{}
	var fields []string
	for _, group := range [][]string{c.RequiredFields, c.RequiredFieldsByType[conceptType]} {
		for _, field := range group {
			if !seen[field] {
				seen[field] = true
				fields = append(fields, field)
			}
		}
	}
	sort.Strings(fields)
	return fields
}

// CreateMap creates a map or journal with minimal structure: _map.md,
// index.md, log.md (D77 WP1 — replaces the former CreateArchive/
// "_archive.md" pair, which is now read-compat only, never written).
// name must be a kebab-case segment; the map must not already exist.
// kind must be "map" or "journal"; empty defaults to "map". If ontologyMode
// is empty, defaults to "flexible". It creates no optional lint contract.
func (kb *KB) CreateMap(name, title, kind string, conceptTypes []string, ontologyMode string) error {
	return kb.CreateMapWithContract(name, title, kind, conceptTypes, ontologyMode, MapContract{})
}

// CreateMapWithContract creates a map or journal with its optional lint
// contract serialized deterministically in _map.md.
func (kb *KB) CreateMapWithContract(name, title, kind string, conceptTypes []string, ontologyMode string, contract MapContract) error {
	if _, err := okf.PathToID(name + ".md"); err != nil {
		return fmt.Errorf("%w: invalid map name %q", okf.ErrInvalidPath, name)
	}
	// A map named "services" would be scaffolded under data/services/, but
	// ResolvePath routes every services/ read to the KB-root descriptor
	// namespace: the map would report success and then be unreadable (D269).
	// Checked before any filesystem mutation, for every kind.
	if name == ServicesNamespace {
		return fmt.Errorf("%w: map name %q is reserved for service descriptors (the KB-root %s/ namespace); choose another name", okf.ErrInvalidPath, name, ServicesNamespace)
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
	if len(contract.RequiredFields) > 0 {
		mapFM.WriteString("required_fields: [" + strings.Join(sortedUnique(contract.RequiredFields), ", ") + "]\n")
	}
	types := make([]string, 0, len(contract.RequiredFieldsByType))
	for typ := range contract.RequiredFieldsByType {
		types = append(types, typ)
	}
	sort.Strings(types)
	for _, typ := range types {
		fields := sortedUnique(contract.RequiredFieldsByType[typ])
		if len(fields) > 0 {
			mapFM.WriteString("required_fields." + typ + ": [" + strings.Join(fields, ", ") + "]\n")
		}
	}
	for _, key := range fieldValueKeys(contract.FieldValues, contract.FieldValuesByType) {
		mapFM.WriteString(key.name + ": [" + strings.Join(key.values, ", ") + "]\n")
	}
	if len(contract.ForbiddenFields) > 0 {
		mapFM.WriteString("forbidden_fields: [" + strings.Join(sortedUnique(contract.ForbiddenFields), ", ") + "]\n")
	}
	if contract.RequireIndexEntry {
		mapFM.WriteString("require_index_entry: true\n")
	}
	if len(contract.MachinePathAllowPrefixes) > 0 {
		mapFM.WriteString("machine_path_allow_prefixes: [" + strings.Join(sortedUnique(contract.MachinePathAllowPrefixes), ", ") + "]\n")
	}
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

type fieldValueKey struct {
	name   string
	values []string
}

// fieldValueKeys flattens the field_values contract into descriptor keys in
// sorted order: map-wide keys first, then per-type ones. Empty lists are
// skipped; values are de-duplicated and sorted.
func fieldValueKeys(wide map[string][]string, byType map[string]map[string][]string) []fieldValueKey {
	var out []fieldValueKey
	fields := make([]string, 0, len(wide))
	for f := range wide {
		fields = append(fields, f)
	}
	sort.Strings(fields)
	for _, f := range fields {
		if v := sortedUnique(wide[f]); len(v) > 0 {
			out = append(out, fieldValueKey{"field_values." + f, v})
		}
	}
	types := make([]string, 0, len(byType))
	for t := range byType {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		fs := make([]string, 0, len(byType[t]))
		for f := range byType[t] {
			fs = append(fs, f)
		}
		sort.Strings(fs)
		for _, f := range fs {
			if v := sortedUnique(byType[t][f]); len(v) > 0 {
				out = append(out, fieldValueKey{"field_values." + t + "." + f, v})
			}
		}
	}
	return out
}

// isMapName reports whether name is one segment naming an existing map or
// journal (one with a descriptor).
func (kb *KB) isMapName(name string) bool {
	if name == "" || strings.Contains(name, "/") {
		return false
	}
	if _, err := okf.PathToID(name + ".md"); err != nil {
		return false
	}
	rel, err := kb.mapDescriptorRelPath(name)
	if err != nil {
		return false
	}
	abs, err := kb.ResolvePath(rel, false)
	if err != nil {
		return false
	}
	_, err = os.Stat(abs)
	return err == nil
}

// IndexGenerated is the value of the `index` contract key that hands a map's
// concept list to the server (D301); "curated" (the default) leaves it to
// the agent.
const IndexGenerated = "generated"

// costIntKeys are the D301 positive-integer threshold overrides.
var costIntKeys = map[string]bool{"repeated_fact_min": true, "hotspot_in_degree": true, "hotspot_bytes": true, "oversize_bytes": true, "oversize_concepts": true}

// MapContractUpdate is a partial change to an existing map's lint contract:
// a nil field is left as it is. An empty list (or false) removes the key, so
// the descriptor ends up exactly as CreateMapWithContract would have written
// it for the resulting contract. A non-nil RequiredFieldsByType replaces every
// per-type key, not only the types it names. FieldValues and FieldValuesByType
// work the same way on the map-wide and per-type field_values.* keys (D275); an
// empty non-nil map removes them all.
type MapContractUpdate struct {
	RequiredFields           *[]string
	RequiredFieldsByType     map[string][]string
	FieldValues              map[string][]string
	FieldValuesByType        map[string]map[string][]string
	ForbiddenFields          *[]string
	RequireIndexEntry        *bool
	MachinePathAllowPrefixes *[]string
	// ValueSynonyms replaces every value_synonyms.* key (D296); an empty
	// non-nil map removes them.
	ValueSynonyms map[string][]string
	// D297 lifecycle keys: nil leaves the key, an empty list, 0 or false
	// removes it.
	OpenStatuses     *[]string
	StaleAfterDays   *int
	TemplateSections *bool
	OpenMarkers      *[]string
	// D298 review keys: nil leaves the key, "" / an empty list / false
	// removes it.
	PromoteTo         *string
	ProcedureHeadings *[]string
	Glossary          *bool
	// D302: nil leaves work_map, "" removes it.
	WorkMap *string
	// D301 cost keys: nil leaves the key; "" or "curated" / 0 removes it.
	Index            *string
	RepeatedFactMin  *int
	HotspotInDegree  *int
	HotspotBytes     *int
	OversizeBytes    *int
	OversizeConcepts *int
	// Title renames the map: the title key and the H1 that repeats it, in
	// _map.md and in index.md. nil leaves it; "" is refused, a map has one.
	Title *string
	// LintIgnore is the map-wide lint_ignore (D306): the checks the whole map
	// accepts. nil leaves it, an empty list removes it; lint reports names a
	// map cannot accept.
	LintIgnore *[]string
}

// UpdateMapContract rewrites the contract keys of an existing map's _map.md,
// leaving every other key, its comments and the descriptor body untouched,
// and returns the contract as read back from disk. Before it existed the
// contract could only be set at creation, so a map created without
// require_index_entry could never opt in to curated-index maintenance (#320).
// A legacy _archive.md descriptor is refused rather than rewritten: that form
// is read-compat only and is never written (D77).
func (kb *KB) UpdateMapContract(name string, upd MapContractUpdate) (MapContract, error) {
	if _, err := okf.PathToID(name + ".md"); err != nil {
		return MapContract{}, fmt.Errorf("%w: invalid map name %q", okf.ErrInvalidPath, name)
	}
	relPath, err := kb.mapDescriptorRelPath(name)
	if err != nil {
		return MapContract{}, fmt.Errorf("UpdateMapContract %s: %w", name, err)
	}
	if gopath.Base(relPath) != "_map.md" {
		return MapContract{}, fmt.Errorf("UpdateMapContract %s: legacy _archive.md descriptor — rewrite it as _map.md with a kind first (D77)", name)
	}
	content, err := kb.ReadRaw(relPath)
	if err != nil {
		return MapContract{}, fmt.Errorf("UpdateMapContract %s: %w", name, err)
	}
	fmRaw, body, ok := okf.SplitFrontmatter(content)
	if !ok {
		return MapContract{}, fmt.Errorf("UpdateMapContract %s: missing frontmatter in _map.md", name)
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return MapContract{}, fmt.Errorf("UpdateMapContract %s: %w", name, err)
	}

	setList := func(key string, values []string) {
		if v := sortedUnique(values); len(v) > 0 {
			fm.Set(key, v)
		} else {
			fm.Delete(key)
		}
	}
	if upd.RequiredFields != nil {
		setList("required_fields", *upd.RequiredFields)
	}
	if upd.RequiredFieldsByType != nil {
		for _, key := range fm.Keys() {
			if strings.HasPrefix(key, "required_fields.") {
				fm.Delete(key)
			}
		}
		types := make([]string, 0, len(upd.RequiredFieldsByType))
		for typ := range upd.RequiredFieldsByType {
			types = append(types, typ)
		}
		sort.Strings(types)
		for _, typ := range types {
			setList("required_fields."+typ, upd.RequiredFieldsByType[typ])
		}
	}
	if upd.FieldValues != nil {
		for _, key := range fm.Keys() {
			if strings.HasPrefix(key, "field_values.") && strings.Count(key, ".") == 1 {
				fm.Delete(key)
			}
		}
		for _, k := range fieldValueKeys(upd.FieldValues, nil) {
			fm.Set(k.name, k.values)
		}
	}
	if upd.FieldValuesByType != nil {
		for _, key := range fm.Keys() {
			if strings.HasPrefix(key, "field_values.") && strings.Count(key, ".") >= 2 {
				fm.Delete(key)
			}
		}
		for _, k := range fieldValueKeys(nil, upd.FieldValuesByType) {
			fm.Set(k.name, k.values)
		}
	}
	if upd.ForbiddenFields != nil {
		setList("forbidden_fields", *upd.ForbiddenFields)
	}
	if upd.OpenStatuses != nil {
		setList("open_statuses", *upd.OpenStatuses)
	}
	if upd.OpenMarkers != nil {
		setList("open_markers", *upd.OpenMarkers)
	}
	if upd.StaleAfterDays != nil {
		if *upd.StaleAfterDays > 0 {
			fm.Set("stale_after", strconv.Itoa(*upd.StaleAfterDays))
		} else {
			fm.Delete("stale_after")
		}
	}
	if upd.TemplateSections != nil {
		if *upd.TemplateSections {
			fm.Set("template_sections", "true")
		} else {
			fm.Delete("template_sections")
		}
	}
	if upd.PromoteTo != nil {
		if v := strings.TrimSpace(*upd.PromoteTo); v != "" {
			if _, err := okf.PathToID(v + ".md"); err != nil || strings.Contains(v, "/") {
				return MapContract{}, fmt.Errorf("UpdateMapContract %s: promote_to %q must be a map name", name, v)
			}
			fm.Set("promote_to", v)
		} else {
			fm.Delete("promote_to")
		}
	}
	if upd.ProcedureHeadings != nil {
		setList("procedure_headings", *upd.ProcedureHeadings)
	}
	if upd.WorkMap != nil {
		if v := strings.TrimSpace(*upd.WorkMap); v != "" {
			if !kb.isMapName(v) {
				return MapContract{}, fmt.Errorf("UpdateMapContract %s: work_map %q must name an existing map", name, v)
			}
			fm.Set("work_map", v)
		} else {
			fm.Delete("work_map")
		}
	}
	if upd.Index != nil {
		switch v := strings.TrimSpace(*upd.Index); v {
		case IndexGenerated:
			fm.Set("index", v)
		case "", "curated":
			fm.Delete("index")
		default:
			return MapContract{}, fmt.Errorf("UpdateMapContract %s: index %q must be generated or curated", name, v)
		}
	}
	for _, kv := range []struct {
		key string
		val *int
	}{{"repeated_fact_min", upd.RepeatedFactMin}, {"hotspot_in_degree", upd.HotspotInDegree}, {"hotspot_bytes", upd.HotspotBytes}, {"oversize_bytes", upd.OversizeBytes}, {"oversize_concepts", upd.OversizeConcepts}} {
		if kv.val == nil {
			continue
		}
		if *kv.val > 0 {
			fm.Set(kv.key, strconv.Itoa(*kv.val))
		} else {
			fm.Delete(kv.key)
		}
	}
	if upd.Glossary != nil {
		if *upd.Glossary {
			fm.Set("glossary", "true")
		} else {
			fm.Delete("glossary")
		}
	}
	if upd.ValueSynonyms != nil {
		for _, key := range fm.Keys() {
			if strings.HasPrefix(key, "value_synonyms.") {
				fm.Delete(key)
			}
		}
		canon := make([]string, 0, len(upd.ValueSynonyms))
		for c := range upd.ValueSynonyms {
			canon = append(canon, c)
		}
		sort.Strings(canon)
		for _, c := range canon {
			if strings.TrimSpace(c) == "" || strings.Contains(c, ".") {
				return MapContract{}, fmt.Errorf("UpdateMapContract %s: value_synonyms canonical %q must be non-empty and contain no '.'", name, c)
			}
			setList("value_synonyms."+c, upd.ValueSynonyms[c])
		}
	}
	if upd.LintIgnore != nil {
		setList("lint_ignore", *upd.LintIgnore)
	}
	var oldTitle, newTitle string
	if upd.Title != nil {
		newTitle = strings.TrimSpace(*upd.Title)
		if newTitle == "" || strings.ContainsAny(newTitle, "\n\r") {
			return MapContract{}, fmt.Errorf("UpdateMapContract %s: title must be one non-empty line", name)
		}
		if v, ok := fm.Get("title"); ok {
			oldTitle, _ = v.(string)
		}
		fm.Set("title", newTitle)
		body = retitleH1(body, oldTitle, newTitle)
	}
	if upd.RequireIndexEntry != nil {
		if *upd.RequireIndexEntry {
			fm.Set("require_index_entry", "true")
		} else {
			fm.Delete("require_index_entry")
		}
	}
	if upd.MachinePathAllowPrefixes != nil {
		setList("machine_path_allow_prefixes", *upd.MachinePathAllowPrefixes)
	}

	abs, err := kb.ResolvePath(relPath, false)
	if err != nil {
		return MapContract{}, fmt.Errorf("UpdateMapContract %s: %w", name, err)
	}
	out := "---\n" + fm.Serialize() + "\n---\n" + body
	if err := writeFileAtomic(abs, []byte(out)); err != nil {
		return MapContract{}, fmt.Errorf("UpdateMapContract %s: write _map.md: %w", name, err)
	}
	if upd.Title != nil {
		if err := kb.retitleIndex(name, oldTitle, newTitle); err != nil {
			return MapContract{}, fmt.Errorf("UpdateMapContract %s: %w", name, err)
		}
	}
	return kb.ReadMapContract(name)
}

// retitleIndex carries a map's new title into its index.md, which repeats it
// in its own frontmatter and H1: a map renamed in _map.md alone would show
// two names. A map without an index.md has nothing to carry.
func (kb *KB) retitleIndex(name, oldTitle, newTitle string) error {
	relPath := name + "/index.md"
	content, err := kb.ReadRaw(relPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read index.md: %w", err)
	}
	fmRaw, body, ok := okf.SplitFrontmatter(content)
	if !ok {
		return nil
	}
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil {
		return fmt.Errorf("index.md: %w", err)
	}
	if v, ok := fm.Get("title"); ok {
		if s, _ := v.(string); s == oldTitle || oldTitle == "" {
			fm.Set("title", newTitle)
		}
	}
	abs, err := kb.ResolvePath(relPath, false)
	if err != nil {
		return err
	}
	out := "---\n" + fm.Serialize() + "\n---\n" + retitleH1(body, oldTitle, newTitle)
	if err := writeFileAtomic(abs, []byte(out)); err != nil {
		return fmt.Errorf("write index.md: %w", err)
	}
	return nil
}

// retitleH1 replaces the body's first H1 when it repeats the old title; an H1
// someone wrote differently is theirs and stays.
func retitleH1(body, oldTitle, newTitle string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		if !strings.HasPrefix(line, "# ") {
			continue
		}
		if strings.TrimSpace(strings.TrimPrefix(line, "# ")) == oldTitle {
			lines[i] = "# " + newTitle
		}
		break
	}
	return strings.Join(lines, "\n")
}

// normalizeAllowPrefix validates and normalizes one
// machine_path_allow_prefixes entry (D124). Only an absolute path is
// accepted: POSIX ("/...") or Windows drive-absolute ("C:\..." or "C:/...").
// A relative path — including a drive-less Windows form such as
// "Users\foo" or "\Users\foo" — is rejected as malformed, same as an empty
// entry. Normalization is limited to stripping harmless trailing separators;
// everything else (case, "..", separator style) is compared literally by
// the caller.
func normalizeAllowPrefix(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}

	isPOSIX := strings.HasPrefix(trimmed, "/")
	isWindows := isWindowsDriveAbsolute(trimmed)
	// A "~/"-anchored prefix is accepted (D159): ~/.ssh/config means "your ssh
	// config" on every machine, exactly as /etc/... does, and machinePathRe
	// already matches that form — so a literal comparison works with no home
	// expansion anywhere. Expanding ~ would make the contract's meaning depend on
	// the reader's home directory, which is the opposite of what an allow-list
	// for portable paths is for. "~user/..." is deliberately rejected: the regex
	// never produces that form, so such a prefix could never match.
	isTilde := trimmed == "~" || strings.HasPrefix(trimmed, "~/")
	if !isPOSIX && !isWindows && !isTilde {
		return "", false
	}
	if trimmed == "~" {
		trimmed = "~/"
	}

	minLen := 1 // bare POSIX root "/"
	if isWindows {
		minLen = 3 // bare Windows drive root "C:\" or "C:/"
	}
	if isTilde {
		minLen = 2 // bare "~/"
	}
	normalized := trimmed
	for len(normalized) > minLen {
		last := normalized[len(normalized)-1]
		if last != '/' && last != '\\' {
			break
		}
		normalized = normalized[:len(normalized)-1]
	}
	return normalized, true
}

// isWindowsDriveAbsolute reports whether s starts with a drive letter
// followed by ":" and a path separator (e.g. "C:\" or "C:/"). A Windows path
// without a drive letter is relative or root-relative and therefore not
// accepted as an absolute allow-prefix.
func isWindowsDriveAbsolute(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	isLetter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')
	return isLetter && s[1] == ':' && (s[2] == '\\' || s[2] == '/')
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" && !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

// DeleteMap removes a map or journal directory, but only if it is empty —
// i.e. it contains nothing but the scaffold files written by CreateMap
// (_map.md, index.md, log.md). If any concept remains under it, the map is
// left untouched and the error lists the concepts so the caller can move
// them out first (concept_move) before retrying.
func (kb *KB) DeleteMap(name string) error {
	if _, err := okf.PathToID(name + ".md"); err != nil {
		return fmt.Errorf("%w: invalid map name %q", okf.ErrInvalidPath, name)
	}
	// No reserved-name check here, unlike CreateMapWithContract: DeleteMap
	// acts on data/<name>/, so an empty data/services/ scaffold left by the
	// pre-D269 map_create can still be removed by the operator.
	mapAbs := filepath.Join(kb.DataRoot(), name)
	if _, err := os.Stat(mapAbs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("DeleteMap: map %q does not exist", name)
		}
		return fmt.Errorf("DeleteMap: %w", err)
	}

	// Walk the physical tree rather than WalkConcepts: non-Markdown assets
	// and asset-only directories are deliberately invisible to the concept
	// graph, but map_delete must never RemoveAll either kind silently.
	var remaining []string
	err := filepath.WalkDir(mapAbs, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == mapAbs {
			return nil
		}
		rel, err := filepath.Rel(mapAbs, path)
		if err != nil {
			return err
		}
		if filepath.Dir(rel) == "." && !d.IsDir() && (rel == "_map.md" || rel == "_archive.md" || rel == "index.md" || rel == "log.md") {
			return nil
		}
		entry := filepath.ToSlash(rel)
		if !d.IsDir() && strings.HasSuffix(entry, ".md") {
			entry = strings.TrimSuffix(entry, ".md")
		}
		remaining = append(remaining, name+"/"+entry)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("DeleteMap: %w", err)
	}
	if len(remaining) > 0 {
		sort.Strings(remaining)
		if len(remaining) > 10 {
			remaining = append(remaining[:10], "...")
		}
		return fmt.Errorf("DeleteMap: map %q is not empty; remove or move these entries first: %s", name, strings.Join(remaining, ", "))
	}

	if err := os.RemoveAll(mapAbs); err != nil {
		return fmt.Errorf("DeleteMap: remove %q: %w", name, err)
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

// ReadMapContract reads a map's optional lint contract. Unknown descriptor
// keys remain ignored for permissive consumption; malformed recognized keys
// are returned as findings for lint to surface without blocking reads.
func (kb *KB) ReadMapContract(archive string) (MapContract, error) {
	relPath, err := kb.mapDescriptorRelPath(archive)
	if err != nil {
		return MapContract{}, fmt.Errorf("ReadMapContract %s: %w", archive, err)
	}
	meta, err := kb.ReadArchiveMeta(archive)
	if err != nil {
		return MapContract{}, err
	}
	contract := MapContract{RequiredFieldsByType: map[string][]string{}}
	// Populate ontology mode and concept types for map_misfit filtering (D295 WP3).
	if om, ok := meta.Get("ontology_mode"); ok {
		if s, ok := om.(string); ok {
			contract.OntologyMode = s
		}
	}
	if kind, ok := meta.Get("kind"); ok {
		contract.Kind, _ = kind.(string)
	}
	if contract.OntologyMode == "" {
		contract.OntologyMode = "flexible"
	}
	if ctVal, ok := meta.Get("concept_types"); ok {
		if ctList, ok := ctVal.([]string); ok {
			contract.ConceptTypes = ctList
		}
	}
	malformedKeys := map[string]bool{}
	bad := func(key string) {
		if malformedKeys[key] {
			return
		}
		malformedKeys[key] = true
		contract.Malformed = append(contract.Malformed, ContractMalformed{Descriptor: relPath, Key: key})
	}
	for _, key := range meta.Keys() {
		if key != "required_fields" && !strings.HasPrefix(key, "required_fields.") &&
			key != "forbidden_fields" && !strings.HasPrefix(key, "field_values.") &&
			key != "require_index_entry" && key != "machine_path_allow_prefixes" &&
			!strings.HasPrefix(key, "value_synonyms.") &&
			key != "open_statuses" && key != "stale_after" && key != "template_sections" && key != "open_markers" &&
			key != "promote_to" && key != "procedure_headings" && key != "glossary" &&
			key != "index" && !costIntKeys[key] && key != "work_map" {
			continue
		}
		value, _ := meta.Get(key)
		switch {
		case key == "require_index_entry":
			v, ok := value.(string)
			if !ok || (v != "true" && v != "false") {
				bad(key)
				continue
			}
			contract.RequireIndexEntry = v == "true"
		case key == "machine_path_allow_prefixes":
			raws, ok := value.([]string)
			if !ok {
				bad(key)
				continue
			}
			seen := map[string]bool{}
			valid := make([]string, 0, len(raws))
			malformed := false
			for _, raw := range raws {
				normalized, normOK := normalizeAllowPrefix(raw)
				if !normOK || seen[normalized] {
					malformed = true
					continue
				}
				seen[normalized] = true
				valid = append(valid, normalized)
			}
			if malformed {
				bad(key)
			}
			sort.Strings(valid)
			contract.MachinePathAllowPrefixes = valid
		case strings.HasPrefix(key, "field_values."):
			vals, ok := value.([]string)
			parts := strings.Split(strings.TrimPrefix(key, "field_values."), ".")
			if !ok || len(parts) > 2 {
				bad(key)
				continue
			}
			seen := map[string]bool{}
			valid := make([]string, 0, len(vals))
			for _, v := range vals {
				if v = strings.TrimSpace(v); v != "" && !seen[v] {
					seen[v] = true
					valid = append(valid, v)
				}
			}
			empty := false
			for _, p := range parts {
				empty = empty || strings.TrimSpace(p) == ""
			}
			if empty || len(valid) == 0 || len(valid) != len(vals) {
				bad(key)
				if empty || len(valid) == 0 {
					continue
				}
			}
			sort.Strings(valid)
			if len(parts) == 1 {
				if contract.FieldValues == nil {
					contract.FieldValues = map[string][]string{}
				}
				contract.FieldValues[parts[0]] = valid
				continue
			}
			if contract.FieldValuesByType == nil {
				contract.FieldValuesByType = map[string]map[string][]string{}
			}
			if contract.FieldValuesByType[parts[0]] == nil {
				contract.FieldValuesByType[parts[0]] = map[string][]string{}
			}
			contract.FieldValuesByType[parts[0]][parts[1]] = valid
		case key == "open_statuses" || key == "open_markers":
			vals, ok := value.([]string)
			if !ok || len(vals) == 0 {
				bad(key)
				continue
			}
			if key == "open_statuses" {
				contract.OpenStatuses = vals
			} else {
				contract.OpenMarkers = vals
			}
		case key == "index":
			v, _ := value.(string)
			switch strings.TrimSpace(v) {
			case IndexGenerated:
				contract.Index = IndexGenerated
			case "curated":
			default:
				bad(key)
				continue
			}
		case costIntKeys[key]:
			s, _ := value.(string)
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n <= 0 {
				bad(key)
				continue
			}
			switch key {
			case "repeated_fact_min":
				contract.RepeatedFactMin = n
			case "hotspot_in_degree":
				contract.HotspotInDegree = n
			case "hotspot_bytes":
				contract.HotspotBytes = n
			case "oversize_bytes":
				contract.OversizeBytes = n
			case "oversize_concepts":
				contract.OversizeConcepts = n
			}
		case key == "stale_after":
			s, _ := value.(string)
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil || n <= 0 {
				bad(key)
				continue
			}
			contract.StaleAfterDays = n
		case key == "template_sections" || key == "glossary":
			v, ok := value.(string)
			if !ok || (v != "true" && v != "false") {
				bad(key)
				continue
			}
			if key == "glossary" {
				contract.Glossary = v == "true"
			} else {
				contract.TemplateSections = v == "true"
			}
		case key == "promote_to":
			v, ok := value.(string)
			v = strings.TrimSpace(v)
			if _, err := okf.PathToID(v + ".md"); !ok || v == "" || err != nil || strings.Contains(v, "/") {
				bad(key)
				continue
			}
			contract.PromoteTo = v
		case key == "work_map":
			v, ok := value.(string)
			v = strings.TrimSpace(v)
			if !ok || !kb.isMapName(v) {
				bad(key)
				continue
			}
			contract.WorkMap = v
		case key == "procedure_headings":
			vals, ok := value.([]string)
			if !ok || len(vals) == 0 {
				bad(key)
				continue
			}
			contract.ProcedureHeadings = vals
		case strings.HasPrefix(key, "value_synonyms."):
			canonical := strings.TrimSpace(strings.TrimPrefix(key, "value_synonyms."))
			syns, ok := value.([]string)
			if !ok || canonical == "" || strings.Contains(canonical, ".") || len(syns) == 0 {
				bad(key)
				continue
			}
			if contract.ValueSynonyms == nil {
				contract.ValueSynonyms = map[string][]string{}
			}
			for _, s := range syns {
				if s = strings.TrimSpace(s); s != "" {
					contract.ValueSynonyms[canonical] = append(contract.ValueSynonyms[canonical], s)
				}
			}
		case key == "forbidden_fields":
			fields, ok := value.([]string)
			if !ok {
				bad(key)
				continue
			}
			seen := map[string]bool{}
			valid := make([]string, 0, len(fields))
			for _, field := range fields {
				field = strings.TrimSpace(field)
				if field == "" || seen[field] {
					bad(key)
					continue
				}
				seen[field] = true
				valid = append(valid, field)
			}
			sort.Strings(valid)
			contract.ForbiddenFields = valid
		case key == "required_fields" || strings.HasPrefix(key, "required_fields."):
			fields, ok := value.([]string)
			if !ok {
				bad(key)
				continue
			}
			seen := map[string]bool{}
			valid := make([]string, 0, len(fields))
			malformed := false
			for _, field := range fields {
				field = strings.TrimSpace(field)
				if field == "" || seen[field] {
					malformed = true
					continue
				}
				seen[field] = true
				valid = append(valid, field)
			}
			if malformed {
				bad(key)
			}
			sort.Strings(valid)
			if key == "required_fields" {
				contract.RequiredFields = valid
				continue
			}
			typ := strings.TrimPrefix(key, "required_fields.")
			if strings.TrimSpace(typ) == "" {
				bad(key)
				continue
			}
			contract.RequiredFieldsByType[typ] = valid
		}
	}
	return contract, nil
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

		// Check strict ontology for concepts inside an archive. rel is a KB
		// path, so its separator is a slash on every host — never
		// filepath.Separator, which would make this check silently do nothing
		// on Windows.
		parts := strings.SplitN(rel, "/", 2)
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
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files, err
}

// TemplateSections returns the H2 headings of templates/<type>.md, the
// sections a concept of that type promises (D297). The slug is the type
// lowercased; a missing or unreadable template returns nil. Headings inside
// fenced code are ignored.
func (kb *KB) TemplateSections(conceptType string) []string {
	slug := strings.ToLower(strings.TrimSpace(conceptType))
	if slug == "" || strings.ContainsAny(slug, "/\\.") {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(kb.Root, "templates", slug+".md"))
	if err != nil {
		return nil
	}
	_, body, _ := okf.SplitFrontmatter(string(data))
	return templateH2(body)
}

// TemplateTexts returns the bodies of the KB's templates/*.md, in name
// order: what lint compares against to tell template boilerplate from a
// repeated fact (D301). Unreadable entries and symlinks are skipped.
func (kb *KB) TemplateTexts() []string {
	dir := filepath.Join(kb.Root, "templates")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		_, body, _ := okf.SplitFrontmatter(string(data))
		out = append(out, body)
	}
	return out
}

// templateH2 is the section list a template promises: its H2 headings outside
// fenced code, or — for a template written as a fenced markdown sample, a
// common shape — the H2 headings of its first fenced block.
func templateH2(body string) []string {
	var outside, firstBlock []string
	inFence, blocks := false, 0
	for _, line := range strings.Split(body, "\n") {
		// A bare fence closes; an opener with an info string ("```markdown")
		// inside an open block is read as the author meant it — the previous
		// sample ended unclosed and a new one starts — not as CommonMark
		// would, which would merge two samples into one section list.
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "```") {
			switch {
			case !inFence:
				blocks++
				inFence = true
			case strings.Trim(trimmed, "`") == "":
				inFence = false
			default:
				blocks++
			}
			continue
		}
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		h := strings.TrimSpace(strings.TrimRight(strings.TrimPrefix(line, "## "), "#"))
		switch {
		case h == "":
		case !inFence:
			outside = append(outside, h)
		case blocks == 1:
			firstBlock = append(firstBlock, h)
		}
	}
	if len(outside) > 0 {
		return outside
	}
	return firstBlock
}

// H2Headings lists a body's level-2 headings in order, ignoring fenced code.
func H2Headings(body string) []string {
	var out []string
	for _, line := range strings.Split(MaskCodeSpans(body), "\n") {
		if strings.HasPrefix(line, "## ") {
			if h := strings.TrimSpace(strings.TrimRight(strings.TrimPrefix(line, "## "), "#")); h != "" {
				out = append(out, h)
			}
		}
	}
	return out
}
