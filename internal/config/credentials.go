package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The files of the credentials directory (D905). A per-KB file is named
// <kb>.<kind>; the kinds that have a default also have default.<kind>.
const (
	CredentialSSH     = "ssh"     // SSH private key for the KB's remote
	CredentialToken   = "token"   // HTTPS git token
	CredentialAge     = "age"     // sops age key
	CredentialSigning = "signing" // artifact signing seed, hex; never has a default

	// DefaultCredentialName is the KB part of the file used by every KB that
	// has none of its own.
	DefaultCredentialName = "default"

	// KnownHostsFile is the one known_hosts file for every remote.
	KnownHostsFile = "known_hosts"

	// AuditKeyFile is the seed that signs the audit log, hex; generated at the
	// first start when it is missing.
	AuditKeyFile = "audit.key"
)

// credentialsDirName is the directory kept under data when credentials_dir is
// not set; it is never discovered as a KB.
const credentialsDirName = "credentials"

// auditDirName is the directory under data that holds the audit log when
// audit.log does not relocate it; it is never discovered as a KB.
const auditDirName = "audit"

// reservedDataNames are the direct subdirectories of data that the server keeps
// for itself and does not discover as KBs, unless one of them holds a KB.
var reservedDataNames = []string{credentialsDirName, auditDirName}

// DefaultDataDir is the data directory a server uses when none is configured
// and one is needed anyway: where `cartographer service install` puts it.
func DefaultDataDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "cartographer-data"
	}
	return filepath.Join(home, "cartographer-data")
}

func (c *Config) dataBase() string {
	if c.Data != "" {
		return c.Data
	}
	return DefaultDataDir()
}

// CredentialsPath is the credentials directory in effect: credentials_dir, or
// <data>/credentials, with the default data directory when there is none.
func (c *Config) CredentialsPath() string {
	if c.CredentialsDir != "" {
		return c.CredentialsDir
	}
	return filepath.Join(c.dataBase(), credentialsDirName)
}

// AuditLogPath is the audit log in effect: audit.log, or <data>/audit/audit.log.
// The log is always on (D905); the setting only relocates it.
func (c *Config) AuditLogPath() string {
	if c.Audit.Log != "" {
		return c.Audit.Log
	}
	return filepath.Join(c.dataBase(), auditDirName, "audit.log")
}

// IsReservedDataName says whether a subdirectory of data is one the server
// keeps for itself, so discovery leaves it alone.
func IsReservedDataName(name string) bool {
	for _, r := range reservedDataNames {
		if name == r {
			return true
		}
	}
	return false
}

// CredentialFile resolves the file of one kind for a KB: <kb>.<kind> first,
// then default.<kind>, except for the signing seed, which a KB either has or
// does not (a signature names its KB). It reports false when neither is a file,
// which means the credential is not configured, exactly as an unset key did.
func CredentialFile(dir, kb, kind string) (string, bool) {
	if p, ok := KBCredentialFile(dir, kb, kind); ok {
		return p, true
	}
	if kind == CredentialSigning {
		return "", false
	}
	return SharedCredentialFile(dir, DefaultCredentialName+"."+kind)
}

// KBCredentialFile is the KB's own file of one kind, <kb>.<kind>, without the
// default.
func KBCredentialFile(dir, kb, kind string) (string, bool) {
	if !safeCredentialName(kb) {
		return "", false
	}
	return SharedCredentialFile(dir, kb+"."+kind)
}

// SharedCredentialFile is a file of the credentials directory by its name: the
// ones no KB has its own copy of (known_hosts, audit.key) and the building block
// of the per-KB lookups. ok is false unless it is a regular file.
func SharedCredentialFile(dir, name string) (string, bool) {
	if dir == "" {
		return "", false
	}
	p := filepath.Join(dir, name)
	if !isFile(p) {
		return "", false
	}
	return p, true
}

// safeCredentialName keeps a KB name from naming a file outside the directory.
func safeCredentialName(kb string) bool {
	return kb != "" && kb != "." && kb != ".." && !strings.ContainsAny(kb, `/\`)
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.Mode().IsRegular()
}

// LoosePermissions describes why a credential file is too open: readable by
// group or others. It is a warning and never a refusal, because a mounted
// Kubernetes Secret is 0644 unless defaultMode says otherwise. Windows has no
// such mode, so it never reports one.
func LoosePermissions(path string) (string, bool) {
	if runtime.GOOS == "windows" {
		return "", false
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm()&0o044 == 0 {
		return "", false
	}
	return fmt.Sprintf("credential file %s is readable by group or others (mode %04o): restrict it to the server's user, chmod 600 (on a Kubernetes Secret volume, defaultMode: 0400)", path, fi.Mode().Perm()), true
}

// WriteCredential writes a new file of the credentials directory with mode
// 0600, creating the directory (0700) when it is missing. It never replaces:
// created is false, with no error, when the file is already there.
func WriteCredential(dir, name string, data []byte) (created bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return false, werr
	}
	return true, nil
}
