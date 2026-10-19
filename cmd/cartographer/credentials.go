package main

import (
	"crypto/ed25519"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/BeppeTemp/cartographer/internal/artifactsig"
	"github.com/BeppeTemp/cartographer/internal/audit"
	"github.com/BeppeTemp/cartographer/internal/config"
)

// loosePermissionWarned remembers the credential files already warned about, so
// the warning is one line per file for the life of the process, not one per
// lookup (D905).
var loosePermissionWarned sync.Map

// credentialFile is the one lookup of a KB's secret file (D905): <kb>.<kind> in
// the credentials directory, then default.<kind>, with no default for the
// signing seed. ok is false when there is no such file, which behaves as the
// credential being unset. A file that is too open (config.LoosePermissions) is
// warned about, never refused.
func credentialFile(credDir, kbName, kind string) (string, bool) {
	return warnLoose(config.CredentialFile(credDir, kbName, kind))
}

// sharedCredentialFile is a file of the credentials directory that is not per
// KB: known_hosts, audit.key.
func sharedCredentialFile(credDir, name string) (string, bool) {
	return warnLoose(config.SharedCredentialFile(credDir, name))
}

// ownCredentialFile is the KB's own file, <kb>.<kind>, with no default: the
// lookup of a setting that overrides a global one.
func ownCredentialFile(credDir, kbName, kind string) (string, bool) {
	return warnLoose(config.KBCredentialFile(credDir, kbName, kind))
}

func warnLoose(path string, ok bool) (string, bool) {
	if !ok {
		return "", false
	}
	if _, seen := loosePermissionWarned.LoadOrStore(path, true); !seen {
		if msg, loose := config.LoosePermissions(path); loose {
			log.Printf("warning: %s", msg)
		}
	}
	return path, true
}

// resolveSopsAgeKeyFile is the age key of a KB: <name>.age, then default.age;
// "" when there is none, the state in which secrets report a missing key.
func resolveSopsAgeKeyFile(credDir, name string) string {
	p, _ := credentialFile(credDir, name, config.CredentialAge)
	return p
}

// loadArtifactSigner reads the KB's artifact signing seed, <name>.signing: the
// hexadecimal seed, whitespace around it ignored. There is no default, a
// signature names its KB; no file means the KB's artifacts are unsigned. The
// seed is never in an error message.
func loadArtifactSigner(credDir, name string) (ed25519.PrivateKey, bool, error) {
	p, ok := credentialFile(credDir, name, config.CredentialSigning)
	if !ok {
		return nil, false, nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, false, fmt.Errorf("read %s: %w", p, err)
	}
	key, err := artifactsig.ParseSeed(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", p, err)
	}
	return key, true, nil
}

// loadAuditKey is the key that signs the audit log: audit.key in the
// credentials directory, the hexadecimal seed with whitespace around it
// ignored. A missing one is generated, so the log is signed from the first
// start (D905); generated reports it. The seed is never in an error message.
func loadAuditKey(credDir string) (kp audit.KeyPair, generated bool, err error) {
	if p, ok := sharedCredentialFile(credDir, config.AuditKeyFile); ok {
		kp, err := readAuditKey(p)
		return kp, false, err
	}
	p := filepath.Join(credDir, config.AuditKeyFile)
	kp, err = audit.GenerateKeyPair()
	if err != nil {
		return audit.KeyPair{}, false, err
	}
	created, err := config.WriteCredential(credDir, config.AuditKeyFile, []byte(kp.SeedToHex()+"\n"))
	if err != nil {
		return audit.KeyPair{}, false, fmt.Errorf("generate %s: %w", p, err)
	}
	if !created {
		// Another process generated it first: both must sign with that one.
		kp, err := readAuditKey(p)
		return kp, false, err
	}
	return kp, true, nil
}

func readAuditKey(p string) (audit.KeyPair, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return audit.KeyPair{}, fmt.Errorf("read %s: %w", p, err)
	}
	kp, err := audit.KeyPairFromSeed(strings.TrimSpace(string(raw)))
	if err != nil {
		// The decode error can quote a character of the seed.
		return audit.KeyPair{}, fmt.Errorf("%s is not a 64-character hexadecimal seed", p)
	}
	return kp, nil
}

// checkCredentialsDir refuses a credentials directory that is itself a KB: the
// KB commits what is in its working tree, and the secrets would go with it.
func checkCredentialsDir(credDir string) error {
	if _, err := os.Stat(filepath.Join(credDir, "data", "index.md")); err == nil {
		return fmt.Errorf("credentials_dir %s is a KB, and its files would be committed with it: set credentials_dir to another directory, or rename the KB", credDir)
	}
	return nil
}

// credentialHint says which file a KB's remote would have been reached with,
// for the error of a clone that failed: an https remote reads <kb>.token or
// default.token, anything else <kb>.ssh or default.ssh. "" when the KB has the
// file, so the failure is something else.
func credentialHint(remote, name, credDir string) string {
	kind := config.CredentialSSH
	if strings.HasPrefix(remote, "http://") || strings.HasPrefix(remote, "https://") {
		kind = config.CredentialToken
	}
	if _, ok := config.CredentialFile(credDir, name, kind); ok {
		return ""
	}
	return fmt.Sprintf("no %s credential found for KB %q: if the remote needs one, create %s or %s",
		kind, name, filepath.Join(credDir, name+"."+kind), filepath.Join(credDir, config.DefaultCredentialName+"."+kind))
}
