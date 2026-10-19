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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/skillbundle"
	"github.com/BeppeTemp/cartographer/internal/sqlindex"
	"github.com/BeppeTemp/cartographer/internal/webui"
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
	kbFlag := fs.String("kb", "", "Folder(s) of KB(s) to mount, comma-separated (or CARTOGRAPHER_KB); such a folder syncs only if it has a git remote")
	dataFlag := fs.String("data", "", "Directory whose direct subdirs are each a separate KB (or CARTOGRAPHER_DATA)")
	initFlag := fs.Bool("init", false, "Initialize KB(s) if they do not exist")
	httpFlag := fs.String("http", "", "HTTP listen address, e.g. :39273 (or CARTOGRAPHER_HTTP)")
	tokensFlag := fs.String("tokens", "", "Comma-separated bearer tokens (or CARTOGRAPHER_TOKENS)")
	configFlag := fs.String("config", "", "Path to a YAML config file (or CARTOGRAPHER_CONFIG)")
	toolsProfileFlag := fs.String("tools-profile", "", "Tools advertised by tools/list: 'agent' (default, core set) or 'full' (or CARTOGRAPHER_TOOLS_PROFILE)")
	webEnabledFlag := fs.Bool("web-enabled", true, "Serve the embedded read-only Atlas UI at /ui/ and its API at /api/ui/v1 in HTTP mode (or CARTOGRAPHER_WEB_ENABLED)")
	updateCheckFlag := fs.Bool("update-check", true, "Look up the latest release once a day and report it in /health and kb_status when newer, and let the Atlas read the release history on demand; off, neither call is made (or CARTOGRAPHER_UPDATE_CHECK)")
	logFileFlag := fs.String("log-file", "", "Append the server log to this file instead of stderr (created if absent; never rotated)")
	fs.Parse(dropRemovedFlags(args))

	// Before anything else can log: a service definition that names a log file
	// expects everything in it, the startup lines included.
	if *logFileFlag != "" {
		f, err := redirectServerLog(*logFileFlag)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
	}

	cfg, err := loadServeConfig(fs, config.FlagOverrides{
		HTTP:         httpFlag,
		Init:         initFlag,
		KB:           kbFlag,
		Data:         dataFlag,
		Tokens:       tokensFlag,
		ToolsProfile: toolsProfileFlag,
		WebEnabled:   webEnabledFlag,
		UpdateCheck:  updateCheckFlag,
	}, *configFlag)
	if err != nil {
		log.Fatal(err)
	}
	announceConfig(cfg, envFallback(*configFlag, "CARTOGRAPHER_CONFIG"))

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
		case "tools-profile":
			explicit.ToolsProfile = overrides.ToolsProfile
		case "web-enabled":
			explicit.WebEnabled = overrides.WebEnabled
		case "update-check":
			explicit.UpdateCheck = overrides.UpdateCheck
		}
	})
	config.ApplyFlags(cfg, explicit)

	// Validate the configuration that will actually be served, not just the
	// YAML: environment- and flag-supplied tokens reach the listener too, and
	// an invalid one used to widen access rather than stop startup (D179).
	if err := config.ValidateAuth(cfg.Auth); err != nil {
		return nil, fmt.Errorf("config: auth: %w", err)
	}
	// A configuration that asks for the server git profile, or still names a
	// kbs[].git_branch (both removed, D903), stops here, before anything is
	// realigned or mounted: ignoring either would send writes to another
	// branch, the one the operator had protected or the remote's default.
	if err := cfg.RefusalError(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// kbMount pairs a resolved filesystem path and resolved name with the
// config.KBSpec it came from, so each KB carries its own policy through the
// open/init step. Name is resolved once via resolveKBName (D53) — before the
// files named after the KB in the credentials directory are looked up (D905) —
// so it is used verbatim afterwards instead of being re-derived. KBs
// auto-discovered from --data have a zero-value Spec; their Name is just the
// directory basename (no config.KBSpec to carry an override).
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

// kbBootstrap is what bootstrapKBs hands to the transport: the mounted KBs and
// their per-KB wiring, index-aligned (D348).
type kbBootstrap struct {
	kbs             []*kb.KB
	names           []string
	artifactSigners []ed25519.PrivateKey
	allowlists      [][]provisioning.MCPAllowlistEntry
	sqlIdxs         map[string]*sqlindex.Index
}

// bootstrapKBs clones remote KBs, opens every mount, wires it and reconciles
// its SQL index. It is the slow part of startup: in HTTP mode it runs after
// the listener is up (D348), in stdio before the transport. phase reports
// "cloning", "mounting" and "indexing" for /health. Failures are fatal, as
// they always were.
func bootstrapKBs(cfg *config.Config, phase func(string)) kbBootstrap {
	// Collect all KB mounts: explicit KBSpec paths first (local, then
	// remote clones — each carrying its own spec), then auto-discovered from
	// Data (zero-value spec). A KB's credentials are files named after it in
	// the credentials directory, whatever its origin (D905).
	phase("cloning")
	credDir := cfg.CredentialsPath()
	var mounts []kbMount
	for _, spec := range cfg.KBs {
		if spec.Remote != "" {
			name := resolveKBName(spec, "")
			dest, err := ensureClonedKB(spec.Remote, name, cfg.Data, gitEnvForKB(name, credDir)...)
			if err != nil {
				if hint := credentialHint(spec.Remote, name, credDir); hint != "" {
					err = fmt.Errorf("%w\n%s", err, hint)
				}
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
		discovered, err := discoverKBPaths(cfg.Data, cfg.CredentialsPath())
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

	phase("mounting")
	seenNames := make(map[string]string) // name → first path seen
	var kbs []*kb.KB
	var kbNames []string                                   // index-aligned with kbs
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
		k.SyncInWindow = kb.DefaultSyncInWindow
		k.SyncOutDebounce = kb.SyncOutDebounceFor(os.Getenv)
		k.GitEnv = gitEnvForKB(m.Name, credDir)
		k.SopsAgeKeyFile = resolveSopsAgeKeyFile(credDir, m.Name)
		// The KB's settings.yaml is brought up to date first (D903): a retired
		// key is dropped, in one commit of its own, and a KB that still asks for
		// the server git profile does not mount at all.
		if refusal := realignKBSettings(k, m.Name); refusal != "" {
			log.Printf("ERROR: KB %q (%s) refuses to mount: %s", m.Name, m.Path, refusal)
			continue
		}
		// The KB's own settings.yaml wins over kbs[] (D746): resolve it first,
		// then every policy read below sees the effective spec. A later pull
		// or edit re-resolves through k.SettingsReload.
		serverSpec, mountName := m.Spec, m.Name
		m.Spec = applyKBPolicy(k, cfg, serverSpec, mountName, false)
		k.SettingsReload = func(k *kb.KB) { applyKBPolicy(k, cfg, serverSpec, mountName, true) }
		k.Discovered = m.Discovered
		name := m.Name
		if name == strings.TrimPrefix(mcpserver.RoutedMountPath, "/mcp/") {
			// The routed endpoint's own path (D288): a KB with this name would
			// be unreachable through /mcp/<name> and ambiguous next to it.
			log.Printf("warning: KB %q (%s) collides with the routed endpoint %s — skipping it; rename the KB", name, m.Path, mcpserver.RoutedMountPath)
			continue
		}
		if prev, ok := seenNames[name]; ok {
			log.Printf("warning: KB name collision %q (first: %s, duplicate: %s) — skipping duplicate", name, prev, m.Path)
			continue
		}
		seenNames[name] = m.Path
		artifactSigner, signed, err := loadArtifactSigner(credDir, name)
		if err != nil {
			log.Fatalf("KB %q artifact signing seed invalid: %v", name, err)
		}
		if signed {
			log.Printf("KB %q provisioning artifact signing enabled (key ID %s)", name, artifactsig.KeyID(artifactSigner.Public().(ed25519.PublicKey)))
		}
		if _, scanErr := provisioning.BuildManifest(nil, map[string]string{name: k.Root}, provisioning.BuildOptions{
			MCPAllowlists:   map[string][]provisioning.MCPAllowlistEntry{name: m.Spec.MCPAllowlist},
			MCPDiagnostic:   func(message string) { log.Printf("warning: %s", message) },
			SkillDiagnostic: func(message string) { log.Printf("warning: %s", message) },
		}); scanErr != nil {
			log.Printf("warning: KB %q MCP descriptor scan: %v", name, scanErr)
		}
		kbs = append(kbs, k)
		kbNames = append(kbNames, name)
		kbArtifactSigners = append(kbArtifactSigners, artifactSigner)
		kbMCPAllowlists = append(kbMCPAllowlists, m.Spec.MCPAllowlist)
		if m.Discovered {
			// A discovered KB works — it serves tools, answers reads, commits
			// writes — and looks identical to a configured one from every client
			// surface, which is how one deployment ran for a whole migration with
			// artifact writes and tool prefixes silently off (D151).
			// Its policy comes from its own settings.yaml (D746), when it has one.
			log.Printf("warning: KB %q was discovered under data:, so no kbs[] entry governs it — its policy comes from its settings.yaml if it has one, and machine_path allow-prefixes are at their defaults", name)
		}
	}

	if len(kbs) == 0 && (cfg.Data == "" || cfg.HTTP == "") {
		log.Fatal("no KBs specified")
	}

	log.Printf("cartographer %s — %d KB(s) mounted", version, len(kbs))

	phase("indexing")
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
	return kbBootstrap{kbs: kbs, names: kbNames, artifactSigners: kbArtifactSigners, allowlists: kbMCPAllowlists, sqlIdxs: sqlIdxs}
}

// applyKBPolicy lays the KB's own settings.yaml over its server spec (D746) and
// copies the effective policy onto the mounted KB. It returns the effective
// spec. refresh is true for a re-resolution after a pull: it logs only what
// changed. Never fatal: a bad KB file costs its entries, not the server.
func applyKBPolicy(k *kb.KB, cfg *config.Config, server config.KBSpec, name string, refresh bool) config.KBSpec {
	k.MarkSettingsSeen()
	st, err := k.ReadSettings()
	if err != nil {
		log.Printf("warning: KB %q: %v — settings.yaml ignored", name, err)
		st = kb.SettingsState{}
	}
	eff, info := config.ResolveKBSettings(server, cfg.ServerSetFunc(name), st)
	prev := k.SettingsInfo()
	k.SetSettingsInfo(info)
	cfg.RecordKBSettings(name, eff, info.FromKB)

	// Validated by config.Load and by ResolveKBSettings; a zero spec yields the default.
	var pol kb.Policy
	pol.AllowArtifactWrite = eff.AllowArtifactWrite
	pol.AllowTemplateWrite = eff.TemplateWriteEnabled()
	pol.AutoRepair, pol.AutoRepairDefault = eff.AutoRepairChecks()
	pol.RepairOnWrite = eff.RepairOnWriteEnabled()
	pol.WriteGate = eff.WriteGateMode()
	pol.DoctorIntervalDays, _ = eff.DoctorIntervalDays()
	pol.DoctorAutoIntervalDays, _ = eff.DoctorAutoIntervalDays()
	pol.DoctorBudget = eff.DoctorBudgetItems()
	pol.UsageStaleDays = eff.UsageStale()
	k.SetPolicy(pol)

	if refresh && fmt.Sprint(prev) == fmt.Sprint(info) {
		return eff
	}
	for _, f := range info.Findings {
		log.Printf("warning: KB %q: %s: %s", name, kb.SettingsFile, f)
	}
	for _, g := range info.Ignored {
		log.Printf("warning: KB %q: %s sets %s to %s; the server's value %s is ignored — the KB is authoritative", name, kb.SettingsFile, g.Key, g.KBValue, g.ServerValue)
	}
	if info.SecretsDeclared && k.SopsAgeKeyFile == "" {
		log.Printf("warning: KB %q declares secrets: true but no age key is configured for it (create %s.age or default.age in credentials_dir)", name, name)
	}
	if refresh {
		log.Printf("KB %q: settings.yaml changed, effective settings re-resolved (mcp_allowlist and git workflow keys apply at the next start)", name)
	}
	return eff
}

// runServe opens/bootstraps all configured KBs and starts the server.
func runServe(cfg *config.Config) {
	if err := checkCredentialsDir(cfg); err != nil {
		log.Fatal(err)
	}
	auditLog, err := openAuditLog(cfg)
	if err != nil {
		log.Fatal(err)
	}

	if err := setupGitSSH(cfg.CredentialsPath()); err != nil {
		log.Fatalf("git SSH setup: %v", err)
	}

	latestVersion := startServerUpdateCheck(cfg, version)
	if cfg.HTTP != "" {
		serveHTTP(cfg.HTTP, func(phase func(string)) kbBootstrap { return bootstrapKBs(cfg, phase) }, cfg.Auth, cfg.MCP.AllowedOrigins, cfg.ToolsProfile, cfg.Web.Enabled, auditLog, latestVersion, cfg)
	} else {
		bs := bootstrapKBs(cfg, func(string) {})
		serveStdio(bs.kbs[0], bs.artifactSigners[0], bs.allowlists[0], cfg.ToolsProfile, bs.sqlIdxs, auditLog, latestVersion)
	}
}

func serveStdio(k *kb.KB, artifactSigner ed25519.PrivateKey, allowlist []provisioning.MCPAllowlistEntry, toolsProfile string, sqlIdxs map[string]*sqlindex.Index, auditLog *audit.Log, latestVersion func() string) {
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
	s.SetLatestVersionSource(latestVersion)
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

// serveHTTP binds the port first and bootstraps the KBs in the background
// (D348): until boot returns and the real handler is built, a BootGate answers
// /health (live, bootstrapping) and /ready (503) and refuses everything else
// with a retryable 503. A bad auth configuration still fails before the
// listener starts; a failure inside boot stays fatal.
func serveHTTP(addr string, boot func(phase func(string)) kbBootstrap, authCfg config.AuthConfig, allowedOrigins []string, toolsProfile string, webEnabled bool, auditLog *audit.Log, latestVersion func() string, settingsCfg *config.Config) {
	if auditLog != nil {
		log.Printf("audit log active")
	}

	authOn, err := resolveAuth(authCfg)
	if err != nil {
		log.Fatal(err)
	}

	var store *auth.TokenStore
	if authOn {
		// RequireAuth carries the decision explicitly (D179). Without it the
		// middleware re-derived enforcement from the token count, which is not
		// the count resolveAuth decided on: the store drops unusable entries,
		// so a configuration that asked for authentication could produce an
		// empty store and serve every request as a local admin.
		store = auth.NewScopedTokenStore(scopedTokensWithRoles(authCfg.Tokens, authCfg.Roles)).RequireAuth()
		if store.TokenCount() == 0 {
			log.Fatal("auth is enabled but no usable token was configured (see auth.tokens, CARTOGRAPHER_TOKENS or --tokens)")
		}
		log.Printf("HTTP auth enabled (%d token(s))", store.TokenCount())
	} else {
		store = auth.NewTokenStore(nil)
		if len(authCfg.Tokens) > 0 {
			// auth.mode "off" with tokens configured: the case of a local
			// service whose generated config pins "off" while a
			// CARTOGRAPHER_TOKENS meant for another server reached its
			// environment (D268). Said out loud so an operator who did mean
			// the tokens sees why they are not enforced.
			log.Printf("HTTP auth disabled: auth.mode is \"off\", ignoring %d configured token(s) (CARTOGRAPHER_TOKENS/--tokens/auth.tokens)", len(authCfg.Tokens))
		} else {
			log.Print("HTTP auth disabled")
		}
	}

	gate := mcpserver.NewBootGate(version)
	// The background repair (D323) of every mounted KB stops with the server:
	// cancelled before the drain, so no run starts while it shuts down.
	repairCtx, stopRepair := context.WithCancel(context.Background())
	defer stopRepair()

	// Timeouts are set explicitly: http.Server's zero value has none, so a
	// client that opens a connection and never finishes its request headers
	// holds a goroutine and a file descriptor indefinitely (the classic
	// Slowloris exhaustion). ReadHeaderTimeout bounds that handshake;
	// ReadTimeout and IdleTimeout bound the slow-body and keep-alive variants.
	// There is deliberately no WriteTimeout: it would also cap the duration of
	// a legitimately slow tool call (a large reindex, a git sync), and those
	// are already bounded per-operation.
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           gate.Handler(),
		ReadHeaderTimeout: 15 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Graceful shutdown (D76/WP4): on SIGINT/SIGTERM, stop accepting new
	// connections, let in-flight requests finish, then flush any pending
	// async push on every mounted KB before the process exits — otherwise a
	// debounced push could be lost on a pod restart. No such hook existed
	// before D76/WP4 (the server previously just blocked in ListenAndServe
	// with no signal handling); this is the minimal addition needed.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	// On Windows there is no CLI-deliverable SIGTERM, so the same shutdown is
	// also reachable through a named event (D217). Off Windows the channel is
	// nil and its select arm can never fire.
	eventCh := watchShutdownEvent()
	serveErrCh := make(chan error, 1)
	go func() {
		log.Printf("HTTP server listening on %s", addr)
		serveErrCh <- httpSrv.ListenAndServe()
	}()

	// published for the shutdown flush; nil until the bootstrap has finished.
	var mounted atomic.Pointer[[]*kb.KB]
	go func() {
		bs := boot(gate.SetPhase)
		kbs, names, artifactSigners, allowlists, sqlIdxs := bs.kbs, bs.names, bs.artifactSigners, bs.allowlists, bs.sqlIdxs
		multi := mcpserver.NewMultiKBServer(version)
		multi.SetLatestVersionSource(latestVersion)
		multi.SetSettings(settingsCfg, string(updateChannelFn()))
		multi.SetReleasesSource(serverReleasesSource(settingsCfg, version))
		// serverInfo.name (D102) identifies the mounted KB only when more than
		// one is mounted — a single-KB HTTP server keeps the historical bare
		// "cartographer" (asserted verbatim in server_test.go).
		multiKB := len(kbs) > 1
		// Each KB learns its siblings' roots for lint's cross_kb_path (D316).
		if multiKB {
			for i, k := range kbs {
				siblings := make(map[string]string, len(kbs)-1)
				for j, other := range kbs {
					if j != i {
						siblings[names[j]] = other.Root
					}
				}
				k.SiblingRoots = siblings
			}
		}
		byName := make(map[string]*kb.KB, len(kbs))
		for i, k := range kbs {
			byName[names[i]] = k
		}
		kbByName := func(n string) (*kb.KB, bool) { k, ok := byName[n]; return k, ok } // concept_transfer (D760)
		for i, k := range kbs {
			k := k
			name := names[i]
			multi.MountKB(name, func(s *mcpserver.Server) {
				if multiKB {
					s.SetDisplayName("cartographer:" + name)
				}
				sqlIdx := sqlIdxs[filepath.Clean(k.Root)]
				mcpserver.RegisterKBTools(s, k, mcpserver.Deps{SQLIndex: sqlIdx, BundleFS: skillbundle.FS, ArtifactSigner: artifactSigners[i], MCPAllowlist: allowlists[i], RoutedMount: true, KBByName: kbByName})
				s.SetToolsProfile(toolsProfile)
				s.SetAuditLog(auditLog)
				s.SetKBName(name)
				s.SetTransport("http")
				s.SetLatestVersionSource(latestVersion)
				s.StartAutoRepair(repairCtx)
			})
			multi.SetKBCapabilities(name, mcpserver.KBCapabilitiesFor(k))
			log.Printf("mounted KB %q at %s (tools profile: %s)", name, k.Root, toolsProfile)
		}

		// D187/D288: the routed mount is the one agent-facing topology, always
		// served. The per-KB endpoints above stay as plumbing (the routed mount
		// dispatches into them) and for clients not yet re-synced. It is enabled
		// after every KB is mounted because it registers the union of what they
		// registered. A server with no KB yet has nothing to route: the next
		// restart, with KBs, serves it.
		if len(names) > 0 {
			if settingsCfg != nil {
				multi.SetKBAliases(settingsCfg.KBAliases)
			}
			err := multi.EnableRoutedMount(version, func(s *mcpserver.Server) {
				s.SetToolsProfile(toolsProfile)
				s.SetTransport("http")
			})
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("routed mount enabled at %s (%d KB(s), one copy of the tools, kb as a tool argument)",
				mcpserver.RoutedMountPath, len(names))
		}

		// The read-only Atlas UI (D227). Mounted only in HTTP mode and only when
		// enabled: with web.enabled off, /ui/ and /api/ui/v1 are not routed at all
		// and the HTTP surface is byte-identical to what it was before the UI.
		webMounted := false
		if webEnabled {
			static, err := webui.Handler()
			if err != nil {
				// The bundle is embedded at build time, so a failure here means a
				// broken binary rather than a misconfiguration: serving the rest
				// without the UI is better than refusing to start.
				log.Printf("warning: the embedded web UI is unusable, serving without it: %v", err)
			} else {
				multi.EnableWeb(static)
				webMounted = true
				log.Printf("Atlas UI on http://%s%s", displayAddr(addr), webui.MountPath)
			}
		}

		// The origin check sits outside authentication (D128): a page that is not
		// allowed to talk to this server should be turned away before its token is
		// looked at.
		handler := mcpserver.OriginGuard(allowedOrigins, store.Middleware(multi.Handler()))
		if webMounted {
			// Outside the auth chain: the browser that types the bare address has
			// no token yet (webui.RedirectRoot).
			handler = mcpserver.OriginGuard(allowedOrigins, webui.RedirectRoot(store.Middleware(multi.Handler())))
		}
		if len(allowedOrigins) > 0 {
			log.Printf("MCP origin allow-list: %s", strings.Join(allowedOrigins, ", "))
		}

		// The handler is complete before the gate opens, so no request can
		// reach a half-mounted server.
		mounted.Store(&kbs)
		gate.Open(handler)
		log.Printf("bootstrap complete: %d KB(s) ready", len(kbs))
	}()

	// One shutdown path, whatever asked for it: a second copy is how the drain
	// and the push flush come to differ between a signal and an event.
	shutdown := func() {
		stopRepair()
		ctx, cancel := context.WithTimeout(context.Background(), shutdownHTTPTimeout)
		defer cancel()
		if err := httpSrv.Shutdown(ctx); err != nil {
			log.Printf("HTTP server shutdown: %v", err)
		}
	}

	select {
	case err := <-serveErrCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	case sig := <-sigCh:
		log.Printf("received %s, shutting down gracefully", sig)
		shutdown()
	case <-eventCh:
		log.Printf("received shutdown event, shutting down gracefully")
		shutdown()
	}

	// Nil while bootstrapping: nothing was mounted, so nothing to flush.
	if kbs := mounted.Load(); kbs != nil {
		for _, k := range *kbs {
			if err := k.FlushPush(shutdownPushFlushTimeout); err != nil {
				log.Printf("flush pending push for KB %s: %v", k.Root, err)
			}
		}
	}
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
		// The warning this replaces (D118) named the right hazard — a token
		// whose scopes all failed to parse ended up with full admin access —
		// but a warning cannot stop a listener from starting. config.ValidateAuth
		// now refuses the configuration outright (D179), so reaching this point
		// with unparsed scopes is impossible.
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
		// The pre-scopes admin token is DECLARED here, not inferred downstream
		// (D179): only this layer can tell "the operator wrote no restriction"
		// apart from "the restrictions the operator wrote produced nothing".
		if len(spec.Scopes) == 0 && len(spec.Roles) == 0 {
			policy.Admin = true
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
// subdirectories (dotfiles excluded), except the server's own and credDir, the
// credentials directory ("" for none). A missing dataDir is tolerated: it is
// created and treated as empty (0 KB, matching D73's "empty data dir" case)
// rather than failing the whole server startup.
func discoverKBPaths(dataDir, credDir string) ([]string, error) {
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
		path := filepath.Join(dataDir, e.Name())
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || serverOwnDataDir(path) ||
			(credDir != "" && absPath(path) == absPath(credDir)) {
			continue
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// serverOwnDataDir says whether a subdirectory of data is one the server keeps
// for itself (credentials) and not a KB: discovery leaves it alone. One that
// holds a KB is a KB, and the server then refuses to put its own files in it.
func serverOwnDataDir(path string) bool {
	if !config.IsReservedDataName(filepath.Base(path)) {
		return false
	}
	_, err := os.Stat(filepath.Join(path, "data", "index.md"))
	return err != nil
}

// auditOptions maps the operator-facing audit configuration onto the package
// options. The server is always the single writer of its log: a second process
// on the same file would fork the hash chain.
func auditOptions(c config.AuditConfig) audit.Options {
	return audit.Options{
		Mode:          c.Mode,
		MaxBytes:      c.MaxSegmentBytes,
		RetentionDays: c.RetentionDays,
		ArchiveDir:    c.ArchiveDir,
		SingleWriter:  true,
	}
}

// openAuditLog opens the audit log, which is always on (D905): audit.log, or
// <data>/audit/audit.log, signed with audit.key from the credentials directory,
// generated at the first start. What cannot be had is one loud warning in
// best_effort mode, and the server runs on: unsigned when the key cannot be
// read or written, without a log when the log cannot be opened, which is also
// the case of a second process on a log another one writes. In required mode
// each of them refuses the start, the error naming the file.
func openAuditLog(cfg *config.Config) (*audit.Log, error) {
	path := cfg.AuditLogPath()
	opts := auditOptions(cfg.Audit)
	required := audit.IsRequired(opts.Mode)

	kp, generated, keyErr := loadAuditKey(cfg.CredentialsPath())
	if keyErr != nil {
		if required {
			return nil, fmt.Errorf("audit log not started: audit.mode is required and the signing key is unusable: %w", keyErr)
		}
		log.Printf("WARNING: the audit log is NOT signed: %v. Create %s with a 64-character hexadecimal seed, or make the directory writable so the server generates it.",
			keyErr, filepath.Join(cfg.CredentialsPath(), config.AuditKeyFile))
	}

	var (
		l   *audit.Log
		err error
	)
	if err = os.MkdirAll(filepath.Dir(path), 0o750); err == nil {
		if keyErr == nil {
			l, err = audit.OpenWithKeyAndOptions(path, kp, opts)
		} else {
			l, err = audit.OpenWithOptions(path, opts)
		}
	}
	if err != nil {
		if required {
			return nil, fmt.Errorf("audit log not started: audit.mode is required: %w", err)
		}
		log.Printf("WARNING: this process runs WITHOUT an audit log: %v", err)
		return nil, nil
	}

	if generated {
		log.Printf("audit key generated: %s", filepath.Join(cfg.CredentialsPath(), config.AuditKeyFile))
	}
	if keyErr == nil {
		log.Printf("audit log: %s (mode %s, signed; verify with --public-key %s)", path, l.ModeName(), hex.EncodeToString(kp.Public))
	} else {
		log.Printf("audit log: %s (mode %s, unsigned)", path, l.ModeName())
	}
	return l, nil
}

// displayAddr turns a listen address into something a person can paste into a
// browser: a bare ":39273" means "every interface", which is not a hostname.
// It never prints a token, because the log is the one place a credential must
// not end up.
func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "localhost" + addr
	}
	if strings.HasPrefix(addr, "0.0.0.0:") {
		return "localhost" + strings.TrimPrefix(addr, "0.0.0.0")
	}
	return addr
}
