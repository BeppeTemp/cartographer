package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/embed"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
)

// shutdownPushFlushTimeout bounds how long serve waits, at shutdown, for a
// pending async push (D76/WP4) to complete on any given KB before giving up
// and exiting anyway. Generous relative to pushFlushTimeout in mcpserver
// (per-request flushes) because this only runs once, at process exit.
const shutdownPushFlushTimeout = 10 * time.Second

// shutdownHTTPTimeout bounds how long the HTTP server waits for in-flight
// requests to finish during a graceful shutdown before forcing close.
const shutdownHTTPTimeout = 10 * time.Second

// cmdServe runs the MCP server (stdio or HTTP), resolving configuration
// from --config/CARTOGRAPHER_CONFIG YAML, CARTOGRAPHER_* environment
// variables, and CLI flags (flag > env > YAML > default).
func cmdServe(args []string) int {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	kbFlag := fs.String("kb", "", "Path(s) to KB(s), comma-separated (or CARTOGRAPHER_KB)")
	dataFlag := fs.String("data", "", "Directory whose direct subdirs are each a separate KB (or CARTOGRAPHER_DATA)")
	initFlag := fs.Bool("init", false, "Initialize KB(s) if they do not exist")
	httpFlag := fs.String("http", "", "HTTP listen address, e.g. :8080 (or CARTOGRAPHER_HTTP)")
	tokensFlag := fs.String("tokens", "", "Comma-separated bearer tokens (or CARTOGRAPHER_TOKENS)")
	ollamaFlag := fs.String("ollama", "", "Ollama base URL for semantic search, e.g. http://localhost:11434 (or CARTOGRAPHER_OLLAMA)")
	gitAutoCommitFlag := fs.Bool("git-autocommit", true, "Create a git commit after each successful write operation (default true; or CARTOGRAPHER_GIT_AUTOCOMMIT=false to disable)")
	gitSyncFlag := fs.Bool("git-sync", true, "Fetch+pull before and push after each write when a remote is configured (default true; or CARTOGRAPHER_GIT_SYNC=false to disable)")
	configFlag := fs.String("config", "", "Path to a YAML config file (or CARTOGRAPHER_CONFIG)")
	toolsProfileFlag := fs.String("tools-profile", "", "Tools advertised by tools/list: 'agent' (default, core set) or 'full' (or CARTOGRAPHER_TOOLS_PROFILE)")
	fs.Parse(args)

	cfg, err := loadServeConfig(fs, config.FlagOverrides{
		HTTP:          httpFlag,
		Init:          initFlag,
		KB:            kbFlag,
		Data:          dataFlag,
		Tokens:        tokensFlag,
		Ollama:        ollamaFlag,
		GitAutocommit: gitAutoCommitFlag,
		GitSync:       gitSyncFlag,
		ToolsProfile:  toolsProfileFlag,
	}, *configFlag)
	if err != nil {
		log.Fatal(err)
	}

	runServe(cfg)
	return 0
}

// loadServeConfig resolves the effective *config.Config for `serve`:
// YAML (if a config path is given) → env overrides → explicit flag overrides.
// Flags are applied only for those actually passed on the command line
// (fs.Visit), so unset flags never clobber env/YAML values.
func loadServeConfig(fs *flag.FlagSet, overrides config.FlagOverrides, configFlagVal string) (*config.Config, error) {
	configPath := envFallback(configFlagVal, "CARTOGRAPHER_CONFIG")

	var cfg *config.Config
	if configPath != "" {
		c, err := config.Load(configPath)
		if err != nil {
			return nil, fmt.Errorf("config load %q: %w", configPath, err)
		}
		cfg = c
	} else {
		cfg = config.Default()
	}

	config.FromEnv(cfg)

	explicit := config.FlagOverrides{}
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "http":
			explicit.HTTP = overrides.HTTP
		case "init":
			explicit.Init = overrides.Init
		case "kb":
			explicit.KB = overrides.KB
		case "data":
			explicit.Data = overrides.Data
		case "tokens":
			explicit.Tokens = overrides.Tokens
		case "ollama":
			explicit.Ollama = overrides.Ollama
		case "git-autocommit":
			explicit.GitAutocommit = overrides.GitAutocommit
		case "git-sync":
			explicit.GitSync = overrides.GitSync
		case "tools-profile":
			explicit.ToolsProfile = overrides.ToolsProfile
		}
	})
	config.ApplyFlags(cfg, explicit)

	return cfg, nil
}

// kbMount pairs a resolved filesystem path and resolved name with the
// config.KBSpec it came from, so each KB can carry its own git identity/SSH
// override through the open/init step. Name is resolved once via
// resolveKBName (D53) — before the git-token/SOPS-age-key conventions are
// applied — so it is used verbatim afterwards instead of being re-derived.
// KBs auto-discovered from --data have a zero-value Spec, which falls back
// entirely to the global cfg.Git settings; their Name is just the directory
// basename (no config.KBSpec to carry an override).
type kbMount struct {
	Path string
	Name string
	Spec config.KBSpec
}

// firstNonEmpty returns the first non-empty string among vs, or "" if all are empty.
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolveSopsAgeKeyFile resolves the SOPS age key file for a KB (D53).
// Resolution order: spec.SopsAgeKeyFile (explicit per-KB override) wins;
// otherwise <sops.AgeKeyDir>/<name>.age if that file exists; otherwise the
// global sops.AgeKeyFile.
func resolveSopsAgeKeyFile(spec config.KBSpec, sops config.SopsConfig, name string) string {
	if spec.SopsAgeKeyFile != "" {
		return spec.SopsAgeKeyFile
	}
	if sops.AgeKeyDir != "" && name != "" {
		p := filepath.Join(sops.AgeKeyDir, name+".age")
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return sops.AgeKeyFile
}

// runServe opens/bootstraps all configured KBs and starts the server.
func runServe(cfg *config.Config) {
	var auditLog *audit.Log
	if cfg.Audit.Log != "" {
		if cfg.Audit.KeySeed != "" {
			kp, err := audit.KeyPairFromSeed(cfg.Audit.KeySeed)
			if err != nil {
				log.Fatalf("audit key seed invalid: %v", err)
			}
			al, err := audit.OpenWithKey(cfg.Audit.Log, kp)
			if err != nil {
				log.Fatalf("audit log open: %v", err)
			}
			auditLog = al
			log.Printf("audit log: %s (signing enabled)", cfg.Audit.Log)
		} else {
			al, err := audit.Open(cfg.Audit.Log)
			if err != nil {
				log.Fatalf("audit log open: %v", err)
			}
			auditLog = al
			log.Printf("audit log: %s", cfg.Audit.Log)
		}
	}

	if err := setupGitSSH(cfg.Git); err != nil {
		log.Fatalf("git SSH setup: %v", err)
	}

	// Collect all KB mounts: explicit KBSpec paths first (local, then
	// remote clones — each carrying its own spec for per-KB git identity),
	// then auto-discovered from Data (zero-value spec = fallback to the
	// global cfg.Git identity).
	var mounts []kbMount
	for _, spec := range cfg.KBs {
		if spec.Remote != "" {
			name := resolveKBName(spec, "")
			dest, err := ensureClonedKB(spec.Remote, name, cfg.Data, gitEnvForKB(spec, cfg.Git, name)...)
			if err != nil {
				log.Fatalf("KB remote %q: %v", spec.Remote, err)
			}
			mounts = append(mounts, kbMount{Path: dest, Name: name, Spec: spec})
			continue
		}
		if spec.Path != "" {
			mounts = append(mounts, kbMount{Path: spec.Path, Name: resolveKBName(spec, spec.Path), Spec: spec})
		}
	}
	if cfg.Data != "" {
		discovered, err := discoverKBPaths(cfg.Data)
		if err != nil {
			log.Fatalf("--data discovery failed: %v", err)
		}
		// Skip paths already mounted explicitly: a remote KB cloned under
		// Data (ensureClonedKB) would otherwise be re-discovered here and only
		// discarded downstream by the name dedup, with a spurious collision warning.
		mounted := make(map[string]bool, len(mounts))
		for _, m := range mounts {
			mounted[filepath.Clean(m.Path)] = true
		}
		for _, p := range discovered {
			if mounted[filepath.Clean(p)] {
				continue
			}
			mounts = append(mounts, kbMount{Path: p, Name: filepath.Base(p)})
		}
	}

	if len(mounts) == 0 {
		// A configured-but-empty data dir is a legitimate fresh state for the
		// native local service (`cartographer service install` on a new
		// machine): in HTTP mode start with zero KBs — /health stays up, KBs
		// appear at the next restart. Stdio needs exactly one KB, and with no
		// KB source configured at all the fail-fast still applies.
		if cfg.Data == "" || cfg.HTTP == "" {
			fmt.Fprintln(os.Stderr, "Error: no KBs configured (--kb/CARTOGRAPHER_KB, --data/CARTOGRAPHER_DATA, or kbs:/data: in the YAML config)")
			os.Exit(1)
		}
		log.Printf("warning: data dir %s has no KBs yet — serving 0 KBs; create a subdirectory (or add kbs: entries) and restart", cfg.Data)
	}

	seenNames := make(map[string]string) // name → first path seen
	var kbs []*kb.KB
	var kbNames []string // index-aligned with kbs
	for _, m := range mounts {
		var k *kb.KB
		var err error
		if cfg.Init {
			k, err = kb.Init(m.Path)
			if err != nil {
				log.Fatalf("KB init %q failed: %v", m.Path, err)
			}
			log.Printf("KB initialized at %s", k.Root)
		} else {
			k, err = kb.Open(m.Path)
			if err != nil {
				log.Fatalf("KB open %q failed: %v\n(use --init to create a new KB)", m.Path, err)
			}
		}
		k.AutoCommit = cfg.Git.Autocommit
		k.GitSync = cfg.Git.Sync
		k.SyncInWindow = cfg.Git.SyncInWindow
		k.SyncOutDebounce = cfg.Git.SyncOutDebounce
		k.GitAuthorName = firstNonEmpty(m.Spec.AuthorName, cfg.Git.AuthorName)
		k.GitAuthorEmail = firstNonEmpty(m.Spec.AuthorEmail, cfg.Git.AuthorEmail)
		k.GitEnv = gitEnvForKB(m.Spec, cfg.Git, m.Name)
		k.SopsAgeKeyFile = resolveSopsAgeKeyFile(m.Spec, cfg.Sops, m.Name)
		k.AllowArtifactWrite = m.Spec.AllowArtifactWrite
		name := m.Name
		if prev, ok := seenNames[name]; ok {
			log.Printf("warning: KB name collision %q (first: %s, duplicate: %s) — skipping duplicate", name, prev, m.Path)
			continue
		}
		seenNames[name] = m.Path
		kbs = append(kbs, k)
		kbNames = append(kbNames, name)
	}

	if len(kbs) == 0 && (cfg.Data == "" || cfg.HTTP == "") {
		log.Fatal("no KBs specified")
	}

	log.Printf("cartographer %s — %d KB(s) mounted (git-autocommit=%v git-sync=%v)", version, len(kbs), cfg.Git.Autocommit, cfg.Git.Sync)

	var emb embed.Embedder
	var vecStore *embed.Store
	if cfg.Search.OllamaURL != "" {
		emb = embed.NewOllama(cfg.Search.OllamaURL, cfg.Search.OllamaModel)
		vecStore = embed.NewStore()
		log.Printf("semantic search enabled: Ollama %s model=%s", cfg.Search.OllamaURL, cfg.Search.OllamaModel)
	}

	// Open per-KB SQLite search index (best-effort; falls back to in-memory).
	sqlIdxs := make(map[string]*sqlindex.Index, len(kbs))
	for _, k := range kbs {
		sqlPath := filepath.Join(k.Root, ".cartographer", "index.db")
		ix, err := sqlindex.Open(sqlPath)
		if err != nil {
			log.Printf("sqlindex: open %s: %v (falling back to in-memory)", sqlPath, err)
			continue
		}
		sqlIdxs[filepath.Clean(k.Root)] = ix
		log.Printf("sqlindex: opened %s", sqlPath)

		// Cold start recovery: index.db is gitignored, so a fresh clone (e.g.
		// after a pod restart) starts with an empty SQLite index even though
		// the concepts are all on disk. Best-effort, keyword-only (no
		// embedding — Ollama may not be reachable yet at boot).
		if n, err := mcpserver.EnsureSQLIndexFresh(k, ix); err != nil {
			log.Printf("sqlindex: ensure fresh %s: %v", sqlPath, err)
		} else if n > 0 {
			log.Printf("sqlindex: rebuilt %d concepts at startup (index was empty)", n)
		}
	}

	if cfg.HTTP != "" {
		serveHTTP(cfg.HTTP, kbs, kbNames, cfg.Auth, cfg.ToolsProfile, emb, vecStore, sqlIdxs, auditLog)
	} else {
		serveStdio(kbs[0], cfg.ToolsProfile, emb, vecStore, sqlIdxs, auditLog)
	}
}

func serveStdio(k *kb.KB, toolsProfile string, emb embed.Embedder, store *embed.Store, sqlIdxs map[string]*sqlindex.Index, auditLog *audit.Log) {
	if auditLog != nil {
		log.Printf("audit log active")
	}
	sqlIdx := sqlIdxs[filepath.Clean(k.Root)]
	s := mcpserver.New(version)
	mcpserver.RegisterKBTools(s, k, mcpserver.Deps{Embedder: emb, VecStore: store, SQLIndex: sqlIdx, BundleFS: skillbundle.FS})
	s.SetToolsProfile(toolsProfile)
	log.Printf("stdio transport, KB: %s (tools profile: %s)", k.Root, toolsProfile)
	// s.Run blocks on the stdio read loop and returns when the client closes
	// stdin (or on a transport error) — that return is stdio's natural
	// shutdown point. Flush any pending async push (D76/WP4) before exiting
	// so a debounced push is not lost when the process ends.
	runErr := s.Run(os.Stdin, os.Stdout)
	if fErr := k.FlushPush(shutdownPushFlushTimeout); fErr != nil {
		log.Printf("flush pending push at shutdown: %v", fErr)
	}
	if runErr != nil {
		log.Fatalf("server error: %v", runErr)
	}
}

func serveHTTP(addr string, kbs []*kb.KB, names []string, authCfg config.AuthConfig, toolsProfile string, emb embed.Embedder, vecStore *embed.Store, sqlIdxs map[string]*sqlindex.Index, auditLog *audit.Log) {
	if auditLog != nil {
		log.Printf("audit log active")
	}

	authOn, err := resolveAuth(authCfg)
	if err != nil {
		log.Fatal(err)
	}

	var store *auth.TokenStore
	if authOn {
		store = auth.NewScopedTokenStore(scopedTokens(authCfg.Tokens))
		log.Printf("HTTP auth enabled (%d token(s))", len(authCfg.Tokens))
	} else {
		store = auth.NewTokenStore(nil)
		log.Print("HTTP auth disabled")
	}

	multi := mcpserver.NewMultiKBServer(version)
	for i, k := range kbs {
		k := k
		name := names[i]
		multi.MountKB(name, func(s *mcpserver.Server) {
			sqlIdx := sqlIdxs[filepath.Clean(k.Root)]
			mcpserver.RegisterKBTools(s, k, mcpserver.Deps{Embedder: emb, VecStore: vecStore, SQLIndex: sqlIdx, BundleFS: skillbundle.FS})
			s.SetToolsProfile(toolsProfile)
		})
		log.Printf("mounted KB %q at %s (tools profile: %s)", name, k.Root, toolsProfile)
	}

	handler := store.Middleware(multi.Handler())
	httpSrv := &http.Server{Addr: addr, Handler: handler}

	// Graceful shutdown (D76/WP4): on SIGINT/SIGTERM, stop accepting new
	// connections, let in-flight requests finish, then flush any pending
	// async push on every mounted KB before the process exits — otherwise a
	// debounced push could be lost on a pod restart. No such hook existed
	// before D76/WP4 (the server previously just blocked in ListenAndServe
	// with no signal handling); this is the minimal addition needed.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	serveErrCh := make(chan error, 1)
	go func() {
		log.Printf("HTTP server listening on %s", addr)
		serveErrCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-serveErrCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("received %s, shutting down gracefully", sig)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownHTTPTimeout)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Printf("HTTP server shutdown: %v", err)
		}
	}

	for _, k := range kbs {
		if err := k.FlushPush(shutdownPushFlushTimeout); err != nil {
			log.Printf("flush pending push for KB %s: %v", k.Root, err)
		}
	}
}

// scopedTokens converts []config.TokenSpec into []auth.ScopedToken, parsing
// each TokenSpec's Scopes (each entry already an atomic "kb:<name>:r|rw"
// string, or possibly a combined space/";"-separated group) via
// auth.ParseScopes. An empty Scopes list yields nil KBScopes, i.e. full
// (admin) access — the same semantics as before scoped tokens existed.
func scopedTokens(specs []config.TokenSpec) []auth.ScopedToken {
	out := make([]auth.ScopedToken, len(specs))
	for i, spec := range specs {
		var scopes []auth.KBScope
		for _, s := range spec.Scopes {
			scopes = append(scopes, auth.ParseScopes(s)...)
		}
		// Fail loud on operator typos: a token that declared scopes but whose
		// entries all failed to parse would otherwise silently degrade to nil
		// scopes = full admin access. Warn so the misconfiguration is visible.
		if len(spec.Scopes) > 0 && len(scopes) == 0 {
			id := spec.Token
			if len(id) > 8 {
				id = id[:8]
			}
			log.Printf("WARNING: token %s… declares scopes %v but none parsed as kb:<name>:r|rw — this token has FULL ADMIN access; fix the scope syntax", id, spec.Scopes)
		}
		out[i] = auth.ScopedToken{Token: spec.Token, Scopes: scopes}
	}
	return out
}

// resolveAuth determines whether auth should be enforced.
// Mode "on"  → enforce (fatal if no tokens configured)
// Mode "off" → disable regardless of tokens
// Mode "auto" (or empty) → enabled if tokens are present
func resolveAuth(authCfg config.AuthConfig) (bool, error) {
	switch authCfg.Mode {
	case "off":
		return false, nil
	case "on":
		if len(authCfg.Tokens) == 0 {
			return false, fmt.Errorf("auth mode is \"on\" but no tokens configured (--tokens, CARTOGRAPHER_TOKENS, or auth.tokens)")
		}
		return true, nil
	default:
		return len(authCfg.Tokens) > 0, nil
	}
}

// discoverKBPaths scans dataDir and returns the paths of its direct subdirectories (dotfiles excluded).
func discoverKBPaths(dataDir string) ([]string, error) {
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return nil, fmt.Errorf("read dir %q: %w", dataDir, err)
	}
	var paths []string
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		paths = append(paths, filepath.Join(dataDir, e.Name()))
	}
	return paths, nil
}
