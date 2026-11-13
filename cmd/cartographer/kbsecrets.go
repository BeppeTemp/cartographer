package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/sops"
)

// maxAgeKeyFile bounds the key file the command reads: an age identity is
// under 200 bytes, a larger file is not one.
const maxAgeKeyFile = 64 << 10

const kbSecretsUsage = "Usage: cartographer kb secrets init <kb> --recipient <age-pub> [--key-file <path>] [--force] [--data <dir>] [--config <path>] [--local]"

// cmdKBSecrets dispatches `cartographer kb secrets <subcommand>`.
func cmdKBSecrets(args []string) int {
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
	token := resolveToken(cfg)
	health, err := client.New(cfg.ServerURL, token).WithInsecureHTTP(cfg.InsecureHTTPAllowed()).Health(probeTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb secrets init:", err)
		return 1
	}
	targets, err := resolveKBTargets(health, []string{name})
	if err != nil {
		fmt.Fprintln(os.Stderr, "kb secrets init:", err)
		return 1
	}
	dir, _ := os.Getwd()
	c := repairCaller(cfg, token, targets[0], dir, os.Stderr)
	return runKBSecretsInit(c, name, strings.TrimSpace(*recipient), *keyFile, credDir, *force, os.Stdout, os.Stderr)
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
