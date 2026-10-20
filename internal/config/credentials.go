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

// FolderSession says whether every KB comes from --kb or CARTOGRAPHER_KB and no
// data directory is configured: a development or test session on folders (D905).
// It has no data directory to keep a credentials directory or an audit log in,
// and does not borrow DefaultDataDir, which belongs to the native service.
func (c *Config) FolderSession() bool {
	if c.Data != "" || len(c.KBs) == 0 {
		return false
	}
	for _, kb := range c.KBs {
		if kb.Path == "" || kb.Remote != "" {
			return false
		}
	}
	return true
}

// dataBase is where the server keeps its own directories: data, else the default
// data directory; "" in a folder session, which keeps none.
func (c *Config) dataBase() string {
	if c.Data != "" {
		return c.Data
	}
	if c.FolderSession() {
		return ""
	}
	return DefaultDataDir()
}

// CredentialsPath is the credentials directory in effect: credentials_dir, or
// <data>/credentials, with the default data directory when there is none. ""
// for a folder session without credentials_dir: it has no credentials.
func (c *Config) CredentialsPath() string {
	if c.CredentialsDir != "" {
		return c.CredentialsDir
	}
	base := c.dataBase()
	if base == "" {
		return ""
	}
	return filepath.Join(base, credentialsDirName)
}

// AuditLogPath is the audit log in effect: audit.log, or <data>/audit/audit.log.
// The log is always on (D905); the setting only relocates it. "" for a folder
// session without audit.log: it runs without an audit log.
func (c *Config) AuditLogPath() string {
	if c.Audit.Log != "" {
		return c.Audit.Log
	}
	base := c.dataBase()
	if base == "" {
		return ""
	}
	return filepath.Join(base, auditDirName, "audit.log")
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

// LoosePermissions describes why a credential file is too open. A file other
// users can read always is. A file its group can read is too open only when the
// server owns it, as it owns one it generated or a copy made for it: a file of
// another user shared with the server's group is access the platform granted,
// and the only way a server that does not run as root reads a Kubernetes
// Secret, whose files the kubelet makes 0440 with the pod's fsGroup whatever
// defaultMode says. OpenSSH draws the same line for a private key. It is a
// warning and never a refusal, because a mounted Kubernetes Secret is 0644
// unless defaultMode says otherwise. Windows has no such mode, so it never
// reports one.
func LoosePermissions(path string) (string, bool) {
	if runtime.GOOS == "windows" {
		return "", false
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	perm := fi.Mode().Perm()
	switch {
	case perm&0o004 != 0:
		return fmt.Sprintf("credential file %s is readable by other users (mode %04o): chmod 600, or on a Kubernetes Secret volume set defaultMode: 0440 with the pod's fsGroup", path, perm), true
	case perm&0o040 != 0 && ownedByProcess(fi):
		return fmt.Sprintf("credential file %s is the server's own and readable by its group (mode %04o): chmod 600", path, perm), true
	}
	return "", false
}

// processUID is the user the ownership of a credential file is compared with;
// a variable so a test can play a file of another user without being root.
var processUID = os.Getuid

// WriteCredential writes a new file of the credentials directory with mode
// 0600, creating the directory (0700) when it is missing. It never replaces:
// created is false, with no error, when the file is already there. The data is
// written to a temporary file and linked into place, so a process that reads
// the file at the same time sees all of it or none; a filesystem without hard
// links gets an exclusive create instead.
func WriteCredential(dir, name string, data []byte) (created bool, err error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return false, err
	}
	path := filepath.Join(dir, name)
	created, err = linkCredential(dir, path, data)
	if err == nil || errors.Is(err, fs.ErrExist) {
		return created, nil
	}
	if _, serr := os.Lstat(path); serr == nil {
		return false, nil
	}
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

// linkCredential publishes data at path through a temporary file of dir. An
// error other than fs.ErrExist from the link means the filesystem cannot link,
// and the caller writes the file another way.
func linkCredential(dir, path string, data []byte) (bool, error) {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return false, err
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		return false, err
	}
	return true, nil
}
