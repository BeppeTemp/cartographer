package sops

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrNoConfig is returned by Create and RuleRecipients when the KB has no root
// .sops.yaml: the operator has not set up encryption, so no file is created.
var ErrNoConfig = errors.New("the KB has no .sops.yaml")

// maxConfigSize bounds what is read of a .sops.yaml.
const maxConfigSize = 1 << 20

// otherKeys are the key types of a creation rule besides age. Cartographer checks
// that the server holds an age recipient; a data key also sent to a KMS, PGP or
// Vault recipient would go to one it never inspected, so such a rule is refused.
type otherKeys struct {
	KMS               any `yaml:"kms"`
	PGP               any `yaml:"pgp"`
	GCPKMS            any `yaml:"gcp_kms"`
	AzureKV           any `yaml:"azure_kv"`
	HCVault           any `yaml:"hc_vault"`
	HCVaultURI        any `yaml:"hc_vault_uri"`
	HCVaultTransitURI any `yaml:"hc_vault_transit_uri"`
}

func (o otherKeys) any() bool {
	for _, v := range []any{o.KMS, o.PGP, o.GCPKMS, o.AzureKV, o.HCVault, o.HCVaultURI, o.HCVaultTransitURI} {
		switch x := v.(type) {
		case nil:
		case string:
			if strings.TrimSpace(x) != "" {
				return true
			}
		case []any:
			if len(x) > 0 {
				return true
			}
		default:
			return true
		}
	}
	return false
}

type creationRule struct {
	otherKeys `yaml:",inline"`
	PathRegex string `yaml:"path_regex"`
	Age       string `yaml:"age"`
	KeyGroups []struct {
		otherKeys `yaml:",inline"`
		Age       []string `yaml:"age"`
	} `yaml:"key_groups"`
}

// RuleRecipients returns the age recipients of the first creation rule of the
// KB's root .sops.yaml that matches relativePath, the rule sops itself would
// pick (an empty path_regex matches every path). matched is false when no rule
// matches. The file is never written by Cartographer (D1191): the recipients are
// the operator's, not the caller's.
func RuleRecipients(kbRoot, relativePath string) (recipients []string, matched bool, err error) {
	path := filepath.Join(kbRoot, ".sops.yaml")
	fi, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, ErrNoConfig
		}
		return nil, false, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > maxConfigSize {
		return nil, false, fmt.Errorf(".sops.yaml must be a plain file of at most %d bytes", maxConfigSize)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	var cfg struct {
		CreationRules []creationRule `yaml:"creation_rules"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, false, fmt.Errorf("parse .sops.yaml: %w", err)
	}
	for _, r := range cfg.CreationRules {
		if r.PathRegex != "" {
			re, err := regexp.Compile(r.PathRegex)
			if err != nil {
				return nil, false, fmt.Errorf(".sops.yaml path_regex %q: %w", r.PathRegex, err)
			}
			if !re.MatchString(filepath.ToSlash(relativePath)) {
				continue
			}
		}
		nonAge := r.otherKeys.any()
		for _, g := range r.KeyGroups {
			nonAge = nonAge || g.otherKeys.any()
		}
		if nonAge {
			return nil, true, fmt.Errorf(".sops.yaml rule %q names a recipient that is not an age key (kms, pgp, vault...); Cartographer creates files only under age-only rules", r.PathRegex)
		}
		for _, a := range strings.Split(r.Age, ",") {
			if a = strings.TrimSpace(a); a != "" {
				recipients = append(recipients, a)
			}
		}
		for _, g := range r.KeyGroups {
			for _, a := range g.Age {
				if a = strings.TrimSpace(a); a != "" {
					recipients = append(recipients, a)
				}
			}
		}
		return recipients, true, nil
	}
	return nil, false, nil
}

// PublicKeys returns the age public keys of an identity file: the
// "# public key:" comments age-keygen writes, else what `age-keygen -y` derives.
// The private key is never returned.
func PublicKeys(keyFile string) []string {
	f, err := os.Open(keyFile)
	if err != nil {
		return nil
	}
	defer f.Close()
	var keys []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if v, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "# public key:"); ok {
			if v = strings.TrimSpace(v); v != "" {
				keys = append(keys, v)
			}
		}
	}
	if len(keys) > 0 {
		return keys
	}
	if bin, err := exec.LookPath("age-keygen"); err == nil {
		if out, err := exec.Command(bin, "-y", keyFile).Output(); err == nil {
			for _, l := range strings.Fields(string(out)) {
				keys = append(keys, l)
			}
		}
	}
	return keys
}

// HoldsRecipient reports whether the identity file is one of recipients.
func HoldsRecipient(keyFile string, recipients []string) bool {
	for _, have := range PublicKeys(keyFile) {
		for _, want := range recipients {
			if have == want {
				return true
			}
		}
	}
	return false
}

// ExpectedRecipients returns the age recipients the KB's encrypted files name:
// those of the creation rule matching a secrets/ file when there is one, else
// those recorded in the sops metadata of the first encrypted file under secrets/.
func ExpectedRecipients(kbRoot string) []string {
	if r, ok, err := RuleRecipients(kbRoot, "secrets/x.sops.yaml"); err == nil && ok && len(r) > 0 {
		return r
	}
	items, _ := os.ReadDir(filepath.Join(kbRoot, "secrets"))
	for _, it := range items {
		if !it.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(kbRoot, "secrets", it.Name()))
		if err != nil || len(data) > maxDeclaredFileSize {
			continue
		}
		var doc struct {
			Sops struct {
				Age []struct {
					Recipient string `yaml:"recipient"`
				} `yaml:"age"`
			} `yaml:"sops"`
		}
		if yaml.Unmarshal(data, &doc) != nil {
			continue
		}
		var out []string
		for _, a := range doc.Sops.Age {
			if a.Recipient != "" {
				out = append(out, a.Recipient)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

// Create writes a new encrypted file holding one pointer (D1191). The
// recipients are the ones of the root .sops.yaml creation rule that matches the
// path, which sops applies itself; the caller chooses nothing. It refuses when
// there is no .sops.yaml or no rule matches, when the key file is not one of the
// rule's recipients (the server could not read back what it wrote), when the
// file exists, and when the pointer would start a list. The value travels on
// stdin and the result is checked to be encrypted before it is written.
func Create(kbRoot, relativePath, pointer, value, keyFile string, env ...string) error {
	if !Available() {
		return fmt.Errorf("sops binary not found in PATH")
	}
	if err := validatePath(kbRoot, filepath.Dir(relativePath), true); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(kbRoot, relativePath)); err == nil {
		return fmt.Errorf("%s already exists", relativePath)
	}
	recipients, matched, err := RuleRecipients(kbRoot, relativePath)
	if err != nil {
		return err
	}
	if !matched {
		return fmt.Errorf("no .sops.yaml creation rule matches %s", relativePath)
	}
	if !HoldsRecipient(keyFile, recipients) {
		return fmt.Errorf("the server's age key is not a recipient of the .sops.yaml rule for %s", relativePath)
	}
	plain, err := plaintextForPointer(pointer, value)
	if err != nil {
		return err
	}
	childEnv, cleanup, err := hermeticEnv(env)
	if err != nil {
		return err
	}
	defer cleanup()
	cmd := exec.Command("sops", "--config", filepath.Join(kbRoot, ".sops.yaml"), "encrypt", "--filename-override", filepath.ToSlash(relativePath), "--input-type", "yaml", "--output-type", "yaml")
	cmd.Dir = kbRoot
	cmd.Stdin = bytes.NewReader(plain)
	cmd.Env = childEnv
	var stdout bytes.Buffer
	cmd.Stdout = &stdout // stderr is dropped: sops diagnostics may echo the value
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("sops encrypt %s: %w", relativePath, err)
	}
	root, err := yamlDocument(stdout.Bytes())
	if err != nil || !hasSOPSMetadata(root) {
		return fmt.Errorf("sops encrypt %s: result is not a SOPS file", relativePath)
	}
	if err := verifyEncryptedPointer(stdout.Bytes(), pointer); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(kbRoot, filepath.Dir(relativePath)), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(kbRoot, relativePath), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(stdout.Bytes()); err != nil {
		f.Close()
		os.Remove(f.Name())
		return err
	}
	return f.Close()
}

// plaintextForPointer renders the one-pointer document sops encrypts. A numeric
// segment is a mapping key here, never a list index: a new file starts as
// mappings, and a list is built by the operator.
func plaintextForPointer(pointer, value string) ([]byte, error) {
	if !strings.HasPrefix(pointer, "/") || pointer == "/" {
		return nil, fmt.Errorf("key must be a JSON Pointer starting with /")
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	var node any = value
	for i := len(parts) - 1; i >= 0; i-- {
		part, err := unescape(parts[i])
		if err != nil {
			return nil, err
		}
		if part == "" {
			return nil, fmt.Errorf("key %q has an empty segment", pointer)
		}
		node = map[string]any{part: node}
	}
	return yaml.Marshal(node)
}
