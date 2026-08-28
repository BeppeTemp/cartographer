package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
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

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
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
	httpFlag := fs.String("http", "", "HTTP listen address, e.g. :39273 (or CARTOGRAPHER_HTTP)")
	tokensFlag := fs.String("tokens", "", "Comma-separated bearer tokens (or CARTOGRAPHER_TOKENS)")
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
	// Discovered marks a KB found by scanning Data rather than declared in a
	// kbs[] entry: it has no KBSpec, so every per-KB setting is at its zero
	// value and nothing but adding the entry can change that (D151). Carried
	// explicitly rather than inferred from an empty Spec, because an operator
	// who writes kbs: [{path: ...}] with no options also has a zero Spec, and
	// that KB is configured — it chose the defaults.
	Discovered bool
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

func completeIdentity(name, email, scope string) error {
	if (name == "") != (email == "") {
		return fmt.Errorf("%s git author_name and author_email must be configured together", scope)
	}
	return nil
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
		opts := auditOptions(cfg.Audit)
		if cfg.Audit.KeySeed != "" {
			kp, err := audit.KeyPairFromSeed(cfg.Audit.KeySeed)
			if err != nil {
				log.Fatalf("audit key seed invalid: %v", err)
			}
			al, err := audit.OpenWithKeyAndOptions(cfg.Audit.Log, kp, opts)
			if err != nil {
				log.Fatalf("audit log open: %v", err)
			}
			auditLog = al
			log.Printf("audit log: %s (mode %s, signing enabled)", cfg.Audit.Log, auditLog.ModeName())
		} else {
			al, err := audit.OpenWithOptions(cfg.Audit.Log, opts)
			if err != nil {
				log.Fatalf("audit log open: %v", err)
			}
			auditLog = al
			log.Printf("audit log: %s (mode %s)", cfg.Audit.Log, auditLog.ModeName())
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
			mounts = append(mounts, kbMount{Path: p, Name: filepath.Base(p), Discovered: true})
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
	seenPrefixes := map[string]string{}  // resolved tool prefix -> KB name (D152)
	var kbs []*kb.KB
	var kbNames []string                                   // index-aligned with kbs
	var kbToolPrefixes []string                            // index-aligned with kbs (D102, "" = unprefixed)
	var kbArtifactSigners []ed25519.PrivateKey             // index-aligned with kbs
	var kbMCPAllowlists [][]provisioning.MCPAllowlistEntry // index-aligned with kbs
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
		k.GitEnv = gitEnvForKB(m.Spec, cfg.Git, m.Name)
		if err := completeIdentity(m.Spec.AuthorName, m.Spec.AuthorEmail, "kbs[]"); err != nil {
			log.Fatal(err)
		}
		if err := completeIdentity(m.Spec.CommitterName, m.Spec.CommitterEmail, "kbs[] committer"); err != nil {
			log.Fatal(err)
		}
		if err := completeIdentity(cfg.Git.AuthorName, cfg.Git.AuthorEmail, "git"); err != nil {
			log.Fatal(err)
		}
		if err := completeIdentity(cfg.Git.CommitterName, cfg.Git.CommitterEmail, "git committer"); err != nil {
			log.Fatal(err)
		}
		k.GitAuthorName, k.GitAuthorEmail = firstNonEmpty(m.Spec.AuthorName, cfg.Git.AuthorName), firstNonEmpty(m.Spec.AuthorEmail, cfg.Git.AuthorEmail)
		k.GitAuthorExplicit = k.GitAuthorName != ""
		if !k.GitAuthorExplicit {
			if name, email, identErr := gitx.AuthorIdent(k.Root, k.GitEnv...); identErr == nil {
				k.GitAuthorName, k.GitAuthorEmail = name, email
			} else {
				k.GitAuthorName, k.GitAuthorEmail, k.GitAuthorExplicit = "cartographer", "cartographer@localhost", true
			}
		}
		k.SopsAgeKeyFile = resolveSopsAgeKeyFile(m.Spec, cfg.Sops, m.Name)
		k.AllowArtifactWrite = m.Spec.AllowArtifactWrite
		k.Discovered = m.Discovered
		name := m.Name
		if prev, ok := seenNames[name]; ok {
			log.Printf("warning: KB name collision %q (first: %s, duplicate: %s) — skipping duplicate", name, prev, m.Path)
			continue
		}
		// With one KB mounted a prefix is pure noise, so derivation is reserved
		// for the second mount onward (D153). An explicit kbs[].tool_prefix still
		// applies: only derivation is suppressed, which ResolveToolPrefix's own
		// precedence gives for free once the mode is the thing we override.
		prefixMode := cfg.MCP.ToolPrefixMode
		if len(mounts) == 1 {
			prefixMode = "off"
		}
		toolPrefix, err := config.ResolveToolPrefix(m.Spec, prefixMode, name)
		if err != nil {
			log.Fatal(err)
		}
		// Fail fast on a duplicate, before anything is appended: two KBs whose
		// prefixes collide advertise identical tool names, and on a flat-namespace
		// client one silently answers for the other (D152). Same shape as the KB
		// name collision above, but fatal: a name clash has a safe fallback
		// (skip the duplicate), an ambiguous prefix does not.
		rawPrefix := m.Spec.ToolPrefix
		if rawPrefix == "" {
			rawPrefix = name
		}
		if err := config.ValidateToolPrefixUniqueness(seenPrefixes, name, toolPrefix, rawPrefix); err != nil {
			log.Fatal(err)
		}
		if toolPrefix != "" {
			seenPrefixes[toolPrefix] = name
		}
		k.ToolPrefix = toolPrefix
		seenNames[name] = m.Path
		var artifactSigner ed25519.PrivateKey
		if m.Spec.ArtifactSigningSeed != "" {
			artifactSigner, err = artifactsig.ParseSeed(m.Spec.ArtifactSigningSeed)
			if err != nil {
				log.Fatalf("KB %q artifact signing seed invalid: %v", name, err)
			}
			log.Printf("KB %q provisioning artifact signing enabled (key ID %s)", name, artifactsig.KeyID(artifactSigner.Public().(ed25519.PublicKey)))
		}
		if _, scanErr := provisioning.BuildManifest(nil, map[string]string{name: k.Root}, provisioning.BuildOptions{
			MCPAllowlists: map[string][]provisioning.MCPAllowlistEntry{name: m.Spec.MCPAllowlist},
			MCPDiagnostic: func(message string) { log.Printf("warning: %s", message) },
		}); scanErr != nil {
			log.Printf("warning: KB %q MCP descriptor scan: %v", name, scanErr)
		}
		kbs = append(kbs, k)
		kbNames = append(kbNames, name)
		kbToolPrefixes = append(kbToolPrefixes, toolPrefix)
		kbArtifactSigners = append(kbArtifactSigners, artifactSigner)
		kbMCPAllowlists = append(kbMCPAllowlists, m.Spec.MCPAllowlist)
		if toolPrefix != "" && m.Spec.ToolPrefix == "" {
			// Derived, not written down by the operator: adding a second KB renames
			// the first one's tools, and that must be loud rather than silent (D153).
			log.Printf("KB %q mounts its tools as %s__<tool> (derived from the KB name; set mcp.tool_prefix_mode: off to keep bare names)", name, toolPrefix)
		}
		if m.Discovered {
			// A discovered KB works — it serves tools, answers reads, commits
			// writes — and looks identical to a configured one from every client
			// surface, which is how one deployment ran for a whole migration with
			// artifact writes and tool prefixes silently off (D151).
			log.Printf("warning: KB %q was discovered under data:, so no kbs[] entry governs it — tool_prefix, allow_artifact_write, sops_age_key_file and machine_path allow-prefixes are all at their defaults", name)
		}
		if _, ok := k.HasRemote(); kb.ShouldWarnGitIdentity(k.GitSync, ok, k.GitAuthorEmail) {
			log.Printf("WARNING: KB %q commits will be authored as cartographer@localhost; forges with author push rules will reject the push", name)
		}
	}

	if len(kbs) == 0 && (cfg.Data == "" || cfg.HTTP == "") {
		log.Fatal("no KBs specified")
	}

	log.Printf("cartographer %s — %d KB(s) mounted (git-autocommit=%v git-sync=%v)", version, len(kbs), cfg.Git.Autocommit, cfg.Git.Sync)

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

		// Best-effort reconciliation with out-of-band changes.
		if stats, err := mcpserver.EnsureSQLIndexFresh(k, ix); err != nil {
			log.Printf("sqlindex: reconcile %s: %v", sqlPath, err)
		} else if stats.Indexed > 0 || stats.Updated > 0 || stats.Removed > 0 {
			log.Printf("sqlindex: reconciled at startup: indexed=%d updated=%d removed=%d", stats.Indexed, stats.Updated, stats.Removed)
		}
	}

	if cfg.HTTP != "" {
		serveHTTP(cfg.HTTP, kbs, kbNames, kbToolPrefixes, kbArtifactSigners, kbMCPAllowlists, cfg.Auth, cfg.MCP.AllowedOrigins, cfg.ToolsProfile, sqlIdxs, auditLog)
	} else {
		serveStdio(kbs[0], kbArtifactSigners[0], kbMCPAllowlists[0], cfg.ToolsProfile, sqlIdxs, auditLog)
	}
}

func serveStdio(k *kb.KB, artifactSigner ed25519.PrivateKey, allowlist []provisioning.MCPAllowlistEntry, toolsProfile string, sqlIdxs map[string]*sqlindex.Index, auditLog *audit.Log) {
	if auditLog != nil {
		log.Printf("audit log active")
	}
	sqlIdx := sqlIdxs[filepath.Clean(k.Root)]
	s := mcpserver.New(version)
	mcpserver.RegisterKBTools(s, k, mcpserver.Deps{SQLIndex: sqlIdx, BundleFS: skillbundle.FS, ArtifactSigner: artifactSigner, MCPAllowlist: allowlist})
	s.SetToolsProfile(toolsProfile)
	s.SetAuditLog(auditLog)
	s.SetKBName(k.AuthName)
	s.SetTransport("stdio")
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

func serveHTTP(addr string, kbs []*kb.KB, names []string, toolPrefixes []string, artifactSigners []ed25519.PrivateKey, allowlists [][]provisioning.MCPAllowlistEntry, authCfg config.AuthConfig, allowedOrigins []string, toolsProfile string, sqlIdxs map[string]*sqlindex.Index, auditLog *audit.Log) {
	if auditLog != nil {
		log.Printf("audit log active")
	}

	authOn, err := resolveAuth(authCfg)
	if err != nil {
		log.Fatal(err)
	}

	var store *auth.TokenStore
	if authOn {
		store = auth.NewScopedTokenStore(scopedTokensWithRoles(authCfg.Tokens, authCfg.Roles))
		log.Printf("HTTP auth enabled (%d token(s))", len(authCfg.Tokens))
	} else {
		store = auth.NewTokenStore(nil)
		log.Print("HTTP auth disabled")
	}

	multi := mcpserver.NewMultiKBServer(version)
	// serverInfo.name (D102) identifies the mounted KB only when more than
	// one is mounted — a single-KB HTTP server keeps the historical bare
	// "cartographer" (asserted verbatim in server_test.go).
	multiKB := len(kbs) > 1
	for i, k := range kbs {
		k := k
		name := names[i]
		prefix := toolPrefixes[i]
		err := multi.MountKBWithPrefix(name, prefix, func(s *mcpserver.Server) {
			if multiKB {
				s.SetDisplayName("cartographer:" + name)
			}
			sqlIdx := sqlIdxs[filepath.Clean(k.Root)]
			mcpserver.RegisterKBTools(s, k, mcpserver.Deps{SQLIndex: sqlIdx, BundleFS: skillbundle.FS, ArtifactSigner: artifactSigners[i], MCPAllowlist: allowlists[i]})
			s.SetToolsProfile(toolsProfile)
			s.SetAuditLog(auditLog)
			s.SetKBName(name)
			s.SetTransport("http")
		})
		if err != nil {
			log.Fatal(err)
		}
		multi.SetKBCapabilities(name, mcpserver.KBCapabilitiesFor(k))
		// The unprefixed line stays exactly as it was pre-D102: the prefix is
		// only mentioned when there is one.
		prefixLog := ""
		if prefix != "" {
			prefixLog = fmt.Sprintf(", tool prefix: %s__", prefix)
		}
		log.Printf("mounted KB %q at %s (tools profile: %s%s)", name, k.Root, toolsProfile, prefixLog)
	}

	if w := flatNamespaceMountWarning(names, toolPrefixes); w != "" {
		log.Printf("warning: %s", w)
	}

	// The origin check sits outside authentication (D128): a page that is not
	// allowed to talk to this server should be turned away before its token is
	// looked at.
	handler := mcpserver.OriginGuard(allowedOrigins, store.Middleware(multi.Handler()))
	if len(allowedOrigins) > 0 {
		log.Printf("MCP origin allow-list: %s", strings.Join(allowedOrigins, ", "))
	}
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
	return scopedTokensWithRoles(specs, nil)
}

// scopedTokensWithRoles additionally compiles the named roles of D118 into an
// immutable per-token policy. Roles and legacy scopes are unioned, so a
// deployment can migrate one token at a time.
func scopedTokensWithRoles(specs []config.TokenSpec, roles []config.RoleSpec) []auth.ScopedToken {
	byName := make(map[string]config.RoleSpec, len(roles))
	for _, r := range roles {
		byName[r.Name] = r
	}
	out := make([]auth.ScopedToken, len(specs))
	for i, spec := range specs {
		var scopes []auth.KBScope
		for _, s := range spec.Scopes {
			scopes = append(scopes, auth.ParseScopes(s)...)
		}
		// Fail loud on operator typos: a token that declared scopes but whose
		// entries all failed to parse would otherwise silently degrade to nil
		// scopes = full admin access. Warn so the misconfiguration is visible.
		// A token carrying roles is already bounded, so it is not a typo case.
		if len(spec.Scopes) > 0 && len(scopes) == 0 && len(spec.Roles) == 0 {
			log.Printf("WARNING: token %s declares scopes %v but none parsed as kb:<name>:r|rw — this token has FULL ADMIN access; fix the scope syntax", principalID(spec), spec.Scopes)
		}
		var policy auth.Policy
		for _, name := range spec.Roles {
			for _, rule := range byName[name].Rules {
				policy.Permissions = append(policy.Permissions, auth.Permission{
					KB:       rule.KB,
					Write:    rule.Access == "rw",
					Maps:     rule.Maps,
					Journals: rule.Journals,
					Types:    rule.Types,
				})
			}
		}
		out[i] = auth.ScopedToken{
			Token:     spec.Token,
			Scopes:    scopes,
			Principal: principalID(spec),
			Policy:    policy,
		}
	}
	return out
}

// principalID returns a stable, non-secret identifier for logs and audit
// records: the operator-chosen ID when present, otherwise a short digest of
// the token. A plaintext token prefix is never used — it would leak key
// material into logs an operator reasonably treats as non-sensitive.
func principalID(spec config.TokenSpec) string {
	if spec.ID != "" {
		return spec.ID
	}
	sum := sha256.Sum256([]byte(spec.Token))
	return "tok-" + hex.EncodeToString(sum[:4])
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

// discoverKBPaths scans dataDir and returns the paths of its direct
// subdirectories (dotfiles excluded). A missing dataDir is tolerated: it is
// created and treated as empty (0 KB, matching D73's "empty data dir" case)
// rather than failing the whole server startup.
func discoverKBPaths(dataDir string) ([]string, error) {
	entries, err := os.ReadDir(dataDir)
	if os.IsNotExist(err) {
		if mkErr := os.MkdirAll(dataDir, 0o755); mkErr != nil {
			return nil, fmt.Errorf("create missing data dir %q: %w", dataDir, mkErr)
		}
		fmt.Fprintf(os.Stderr, "data dir %q did not exist, created\n", dataDir)
		return nil, nil
	}
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

// auditOptions maps the operator-facing audit configuration onto the package
// options. An empty AuditConfig yields the zero Options, i.e. the pre-D119
// best-effort behaviour.
func auditOptions(c config.AuditConfig) audit.Options {
	return audit.Options{
		Mode:          c.Mode,
		MaxBytes:      c.MaxSegmentBytes,
		RetentionDays: c.RetentionDays,
		ArchiveDir:    c.ArchiveDir,
	}
}
