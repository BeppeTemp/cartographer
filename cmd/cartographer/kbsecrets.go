package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/sops"
)

// maxAgeKeyFile bounds the key file the command reads: an age identity is
// under 200 bytes, a larger file is not one.
const maxAgeKeyFile = 64 << 10

const kbSecretsUsage = "Usage: cartographer kb secrets init <kb> --recipient <age-pub> [--key-file <path>] [--force] [--data <dir>] [--config <path>] [--local]"

// cmdKBSecrets dispatches `cartographer kb secrets <subcommand>`.
func cmdKBSecrets(args []string) int {
	if len(args) > 0 && isHelpArg(args[0]) {
		fmt.Fprintln(os.Stdout, kbSecretsUsage)
		return 0
	}
	if len(args) == 0 || args[0] != "init" {
		fmt.Fprintln(os.Stderr, kbSecretsUsage)
		return 2
	}
	return cmdKBSecretsInit(args[1:])
}

// cmdKBSecretsInit implements `cartographer kb secrets init <kb> --recipient
// <age-pub> [--key-file <path>] [--force]`: it enables secrets on a KB with one
// command. The creation rule of .sops.yaml goes through the running server
// (the admin-only secrets_init tool, committed in the normal write flow); the
// key is installed here, on the server machine, because the key file is a path
// of the operator's machine and no MCP call may write a credential (D2039).
// Like kb reauthor it is local (D177). Exit codes: 0 done or unchanged, 1
// refusal or failure, 2 usage.
func cmdKBSecretsInit(args []string) int {
	var name string
	fs := flag.NewFlagSet("kb secrets init", flag.ExitOnError)
	recipient := fs.String("recipient", "", "age public key (age1...) of the operator who may decrypt")
	keyFile := fs.String("key-file", "", "age identity file to install as <kb>.age in the credentials directory")
	force := fs.Bool("force", false, "Replace a different existing rule or key")
	dataFlag := fs.String("data", "", "KB data directory (default: the server config's data:, or "+defaultDataDir()+")")
	configFlag := fs.String("config", "", "Server config YAML (default: the standard path)")
	localFlag := fs.Bool("local", false, "Act on the local data dir even though the client points at a remote server")
	if code := bindPositionals("kb secrets init", parseFlagsAnywhere(fs, args), &name); code != 0 {
		return code
	}
	if name == "" || *recipient == "" {
		fmt.Fprintln(os.Stderr, kbSecretsUsage)
		return 2
	}
	if code := checkLocalTarget("kb secrets init", *dataFlag, *localFlag); code != 0 {
		return code
	}
	dataDir := *dataFlag
	if dataDir == "" {
		dataDir = resolveServerDataDir(*configFlag)
	}
	if err := validateKBName(name); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	scfg, err := loadReauthorConfig(*configFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}
	credDir := kbCredentialDir(dataDir, scfg)

	cfg, err := loadClientConfigOrDefault()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb secrets init:", err)
		return 1
	}
	// Like every kb command it talks to the local service its config describes
	// (the config's http:, else the default address), never to the client's
	// server_url (docs/deployment.md).
	localCfg := *cfg
	localCfg.ServerURL = serverBaseURL(*configFlag) + "/mcp"
	cfg = &localCfg
	token := resolveToken(cfg)
	health, err := client.New(cfg.ServerURL, token).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).Health(probeTimeout)
	if err != nil && errors.Is(err, client.ErrUnauthorized) {
		fmt.Fprintln(os.Stderr, "kb secrets init:", secretsUnauthorized(token != ""))
		return 1
	}
	if err != nil {
		// Only a refused connection proves no service runs (an answer, a 401
		// included, or a timeout does not): then the rule is written in the
		// clone directly, as kb reauthor and kb attach do (#2278).
		if isConnRefused(err) {
			code := runKBSecretsInit(&directRuleCaller{name: name, dataDir: dataDir, cfg: scfg, errOut: os.Stderr},
				name, strings.TrimSpace(*recipient), *keyFile, credDir, *force, os.Stdout, os.Stderr)
			if code == 0 {
				fmt.Fprintln(os.Stderr, "service not running: wrote the rule in the clone directly")
			}
			return code
		}
		fmt.Fprintln(os.Stderr, "kb secrets init:", err)
		return 1
	}
	targets, err := resolveTypedKBTargets(health, []string{name}, cfg.ServerURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb secrets init:", err)
		return 1
	}
	dir, _ := os.Getwd()
	c := &unauthorizedHintCaller{toolCaller: repairCaller(cfg, token, targets[0], dir, os.Stderr), sent: token != ""}
	return runKBSecretsInit(c, name, strings.TrimSpace(*recipient), *keyFile, credDir, *force, os.Stdout, os.Stderr)
}

// secretsUnauthorized words a 401 of kb secrets init with the source the token
// was read from: this command reads auth/token of the client config, never
// $CARTOGRAPHER_TOKENS (the server's own variable), and a 401 that names only
// "the env var" sends the operator to the wrong place (#2284).
func secretsUnauthorized(sent bool) string {
	src := "the client config"
	if dir, derr := clientconfig.TargetDir(); derr == nil {
		src = clientconfig.Path(dir)
	}
	state := "no bearer token was sent"
	if sent {
		state = "the bearer token was rejected"
	}
	return fmt.Sprintf("%s: the token is read from auth/token of %s, not from $CARTOGRAPHER_TOKENS", state, src)
}

// unauthorizedHintCaller rewrites a 401 of the tool call like the health
// probe's (secretsUnauthorized).
type unauthorizedHintCaller struct {
	toolCaller
	sent bool
}

func (u *unauthorizedHintCaller) Invoke(tool string, args any) (json.RawMessage, error) {
	raw, err := u.toolCaller.Invoke(tool, args)
	if err != nil && errors.Is(err, client.ErrUnauthorized) {
		return nil, errors.New(secretsUnauthorized(u.sent))
	}
	return raw, err
}

// secretsRuleSubject is the commit subject of the creation rule, the one the
// secrets_init tool commits with.
const secretsRuleSubject = "chore(secrets): enable secrets, creation rule for 1 recipient"

// directRuleCaller answers the secrets_init call of runKBSecretsInit by
// writing the rule in the KB's clone itself, for when no service runs: the
// same family as kb reauthor and kb attach (open the clone, take the process
// lock D155, commit the path, push best-effort). The text it returns is the
// tool's, so the caller's one code path and output stay the same.
type directRuleCaller struct {
	name, dataDir string
	cfg           *config.Config
	errOut        io.Writer
}

func (d *directRuleCaller) Invoke(tool string, args any) (json.RawMessage, error) {
	if tool != "secrets_init" {
		return nil, fmt.Errorf("%s is not available without a running service", tool)
	}
	m, _ := args.(map[string]any)
	recipient, _ := m["recipient"].(string)
	force, _ := m["force"].(bool)

	root := filepath.Join(d.dataDir, d.name)
	if !isLocalKB(d.dataDir, d.name) {
		// Like the server path: an unknown name says so and lists the KBs of
		// the data dir (#2284).
		return nil, noLocalKBError(d.name, d.dataDir)
	}
	k, err := kb.Open(root)
	if err != nil {
		return nil, fmt.Errorf("%s is not an OKF KB: %v", root, err)
	}
	if !gitx.IsRepo(root) {
		return nil, fmt.Errorf("%s is not a git repository", root)
	}
	k.GitEnv = gitEnvForKB(d.name, d.cfg.CredentialsPath())
	idName, idEmail, hasID := d.cfg.IdentityFor(d.name)
	if hasID {
		k.SetDefaultIdentity(idName, idEmail)
	}
	release, err := k.AcquireProcessLock(reauthorLockTimeout, "cartographer kb secrets init")
	if err != nil {
		return nil, err
	}
	defer release()

	state, previous, err := sops.InitRule(k.Root, recipient, force)
	if err != nil {
		var exists *sops.RuleExistsError
		if errors.As(err, &exists) {
			return nil, fmt.Errorf("the KB already has a different .sops.yaml (recipients: %s); pass force to replace it", strings.Join(previous, ", "))
		}
		return nil, err
	}
	msg := "secrets_init: rule " + state
	if state == sops.RuleReplaced {
		msg += "; files already under secrets/ keep their old recipients: run sops updatekeys on each"
	}
	// Commit even when the rule is unchanged: a rerun after a failed commit
	// finds the file already written, and CommitPaths is a no-op when there is
	// nothing to commit.
	before, _ := gitx.HeadSHA(k.Root)
	if err := k.CommitPaths([]string{".sops.yaml"}, secretsRuleSubject, idName, idEmail); err != nil {
		return nil, fmt.Errorf("the rule was written but not committed: %v (run the command again)", err)
	}
	if after, _ := gitx.HeadSHA(k.Root); after != before || state != sops.RuleUnchanged {
		// A failed push is not fatal: the rule is committed and the server
		// pushes on its next write or sync.
		if err := k.SyncOut(); err != nil {
			fmt.Fprintf(d.errOut, "kb secrets init: the rule is committed but not pushed yet: %v\n", err)
		}
	}
	return json.RawMessage(msg), nil
}

// runKBSecretsInit is cmdKBSecretsInit once a caller for the KB exists. The
// order leaves nothing half-done: every check that needs no write comes first,
// then the rule, then the key; the command is idempotent, so a key install that
// fails after the rule is committed is fixed by running it again.
func runKBSecretsInit(c toolCaller, name, recipient, keyFile, credDir string, force bool, out, errOut io.Writer) int {
	fail := func(format string, a ...any) int {
		fmt.Fprintf(errOut, "kb secrets init: "+format+"\n", a...)
		return 1
	}
	if !sops.ValidRecipient(recipient) {
		return fail("--recipient is not an age public key (age1...)")
	}
	target := filepath.Join(credDir, name+"."+config.CredentialAge)

	var keyData []byte
	if keyFile != "" {
		data, err := readAgeKeyFile(keyFile)
		if err != nil {
			return fail("%v", err)
		}
		keyData = data
		if !sops.HoldsRecipient(keyFile, []string{recipient}) {
			return fail("%s does not hold the recipient %s: nothing was written", filepath.Base(keyFile), recipient)
		}
	}

	keyState := "not given"
	haveTarget := false
	if fi, err := os.Lstat(target); err == nil {
		if !fi.Mode().IsRegular() {
			return fail("%s is not a regular file: nothing was written", filepath.Base(target))
		}
		haveTarget = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail("%v", err)
	}
	if keyFile != "" && haveTarget {
		if slices.Equal(sortedCopy(sops.PublicKeys(target)), sortedCopy(sops.PublicKeys(keyFile))) {
			keyState = "unchanged"
		} else if !force {
			return fail("%s already holds a different key: pass --force to replace it (nothing was written)", filepath.Base(target))
		} else {
			keyState = "replaced"
		}
	} else if keyFile != "" {
		keyState = "installed"
	}

	var res string
	args := map[string]any{"recipient": recipient}
	if force {
		args["force"] = true
	}
	raw, err := c.Invoke("secrets_init", args)
	if err != nil {
		return fail("%v", err)
	}
	res = string(raw)
	state := ""
	for _, s := range []string{sops.RuleCreated, sops.RuleUnchanged, sops.RuleReplaced} {
		if strings.Contains(res, "rule "+s) {
			state = s
		}
	}
	if state == "" {
		return fail("unexpected answer from the server: %s", strings.TrimSpace(res))
	}
	fmt.Fprintf(out, "rule: %s\n", state)

	switch keyState {
	case "installed":
		created, err := config.WriteCredential(credDir, filepath.Base(target), keyData)
		if err != nil {
			return fail("the rule is committed but the key was not installed: %v (run the command again)", err)
		}
		if !created {
			return fail("the rule is committed but %s appeared meanwhile: run the command again", filepath.Base(target))
		}
	case "replaced":
		if err := config.ReplaceCredential(credDir, filepath.Base(target), keyData); err != nil {
			return fail("the rule is committed but the key was not replaced: %v (run the command again)", err)
		}
	}
	fmt.Fprintf(out, "key: %s\n", keyState)
	if state == sops.RuleReplaced {
		fmt.Fprintln(out, "files already under secrets/ keep their old recipients: run sops updatekeys on each")
	}

	enabled := keyState != "not given"
	if !enabled && haveTarget {
		enabled = sops.HoldsRecipient(target, []string{recipient})
	}
	if enabled {
		fmt.Fprintln(out, "secrets are enabled: no restart needed")
	} else {
		fmt.Fprintf(out, "secrets stay disabled until a key holding %s is at %s in the credentials directory (it loads on the next call, no restart)\n", recipient, filepath.Base(target))
	}
	return 0
}

// readAgeKeyFile reads an age identity file, capped, and checks it looks like
// one. Its content is never in an error or an output.
func readAgeKeyFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("--key-file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAgeKeyFile+1))
	if err != nil {
		return nil, fmt.Errorf("--key-file: %w", err)
	}
	if len(data) > maxAgeKeyFile {
		return nil, fmt.Errorf("--key-file is larger than %d bytes: not an age identity", maxAgeKeyFile)
	}
	if !strings.Contains(string(data), "AGE-SECRET-KEY-") {
		return nil, errors.New("--key-file is not an age identity (no AGE-SECRET-KEY- line)")
	}
	return data, nil
}

func sortedCopy(s []string) []string {
	c := slices.Clone(s)
	slices.Sort(c)
	return c
}
