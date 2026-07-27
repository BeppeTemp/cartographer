package sops

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SecretFile represents a decrypted SOPS file. Values are keyed by RFC 6901
// JSON Pointers (flat mappings retain their legacy top-level keys).
type SecretFile struct {
	Path   string
	Values map[string]string
}

func Available() bool { _, err := exec.LookPath("sops"); return err == nil }

func Version() (string, error) {
	out, err := exec.Command("sops", "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sops --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Decrypt decrypts relativePath from kbRoot. SOPS always runs from kbRoot so
// repository-relative creation rules have deterministic semantics.
func Decrypt(kbRoot, relativePath string, env ...string) (*SecretFile, error) {
	if !Available() {
		return nil, fmt.Errorf("sops binary not found in PATH")
	}
	if err := validatePath(kbRoot, relativePath, false); err != nil {
		return nil, err
	}
	cmd := exec.Command("sops", "decrypt", "--output-type", "yaml", relativePath)
	cmd.Dir = kbRoot
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sops decrypt %s: %w: %s", relativePath, err, string(out))
	}
	values, err := parseYAMLFlat(out)
	if err != nil {
		return nil, fmt.Errorf("parse decrypted %s: %w", relativePath, err)
	}
	return &SecretFile{Path: relativePath, Values: values}, nil
}

// DecryptAll decrypts all *.sops.yaml files in a relative directory.
func DecryptAll(kbRoot, dir string, env ...string) ([]SecretFile, []error) {
	if err := validatePath(kbRoot, dir, false); err != nil {
		return nil, []error{err}
	}
	matches, err := filepath.Glob(filepath.Join(kbRoot, dir, "*.sops.yaml"))
	if err != nil {
		return nil, []error{err}
	}
	var files []SecretFile
	var errs []error
	for _, m := range matches {
		rel, _ := filepath.Rel(kbRoot, m)
		sf, err := Decrypt(kbRoot, rel, env...)
		if err != nil {
			errs = append(errs, err)
		} else {
			files = append(files, *sf)
		}
	}
	return files, errs
}

func AgeKeyEnv(path string) []string {
	if path == "" {
		return nil
	}
	return []string{"SOPS_AGE_KEY_FILE=" + path}
}

type SecretRef struct {
	Name     string
	SOPSFile string
	SOPSKey  string
}

func ResolveRefs(kbRoot string, refs []SecretRef, env ...string) (map[string]string, error) {
	cache := map[string]*SecretFile{}
	result := make(map[string]string)
	names := map[string]bool{}
	for _, ref := range refs {
		if ref.Name == "" || names[ref.Name] {
			return nil, fmt.Errorf("duplicate or empty secret ref name %q", ref.Name)
		}
		names[ref.Name] = true
		if err := validatePath(kbRoot, ref.SOPSFile, false); err != nil {
			return nil, fmt.Errorf("resolve %s: %w", ref.Name, err)
		}
		sf := cache[ref.SOPSFile]
		if sf == nil {
			var err error
			sf, err = Decrypt(kbRoot, ref.SOPSFile, env...)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", ref.Name, err)
			}
			cache[ref.SOPSFile] = sf
		}
		val, ok := sf.Values[ref.SOPSKey]
		if !ok {
			keys := make([]string, 0, len(sf.Values))
			for k := range sf.Values {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if len(keys) > 10 {
				keys = keys[:10]
			}
			return nil, fmt.Errorf("key %q not found in %s (available: %s)", ref.SOPSKey, ref.SOPSFile, strings.Join(keys, ", "))
		}
		result[ref.Name] = val
	}
	return result, nil
}

// Set changes one pointer on a pre-existing encrypted file without ever
// exposing value in argv or the result. It returns only the selected path/key.
func Set(kbRoot, relativePath, pointer, value string, env ...string) error {
	if !Available() {
		return fmt.Errorf("sops binary not found in PATH")
	}
	if err := validatePath(kbRoot, relativePath, false); err != nil {
		return err
	}
	if !strings.HasPrefix(pointer, "/") {
		return fmt.Errorf("key must be a JSON Pointer starting with /")
	}
	original := filepath.Join(kbRoot, relativePath)
	raw, err := os.ReadFile(original)
	if err != nil {
		return err
	}
	if !bytes.Contains(raw, []byte("sops:")) {
		return fmt.Errorf("%s is not SOPS-encrypted (missing sops metadata)", relativePath)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(original); err == nil {
		mode = info.Mode()
	}
	dir := filepath.Dir(original)
	tmp, err := os.CreateTemp(dir, ".sops-set-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return err
	}
	selector, err := selectorForPointer(pointer)
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(value)
	cmd := exec.Command("sops", "set", "--value-stdin", selector, filepath.Base(tmpName))
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(encoded)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("sops set %s: %w: %s", relativePath, err, redact(string(out), value))
	}
	updated, err := os.ReadFile(tmpName)
	if err != nil {
		return err
	}
	if err := verifyEncryptedPointer(updated, pointer); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, original)
}

func EnvForSkill(resolved map[string]string) []string {
	env := make([]string, 0, len(resolved))
	for k, v := range resolved {
		env = append(env, k+"="+v)
	}
	return env
}

func validatePath(root, rel string, allowMissing bool) error {
	if !filepath.IsLocal(rel) || rel == "." {
		return fmt.Errorf("path %q must be relative and inside the KB", rel)
	}
	parts := strings.Split(filepath.Clean(rel), string(filepath.Separator))
	current := root
	for i, p := range parts {
		current = filepath.Join(current, p)
		info, err := os.Lstat(current)
		if err != nil {
			if allowMissing && os.IsNotExist(err) && i == len(parts)-1 {
				return nil
			}
			return fmt.Errorf("path %q: %w", rel, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("path %q must not contain symlinks", rel)
		}
	}
	return nil
}

func parseYAMLFlat(data []byte) (map[string]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("root must be a mapping")
	}
	result := map[string]string{}
	if err := flatten(doc.Content[0], "", result); err != nil {
		return nil, err
	}
	return result, nil
}
func flatten(n *yaml.Node, ptr string, out map[string]string) error {
	if n.Kind == yaml.AliasNode {
		return fmt.Errorf("aliases are not supported")
	}
	switch n.Kind {
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i < len(n.Content); i += 2 {
			k, v := n.Content[i], n.Content[i+1]
			if k.Kind != yaml.ScalarNode {
				return fmt.Errorf("map keys must be strings")
			}
			if seen[k.Value] {
				return fmt.Errorf("duplicate key %q", k.Value)
			}
			seen[k.Value] = true
			if err := flatten(v, ptr+"/"+escape(k.Value), out); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for i, v := range n.Content {
			if err := flatten(v, ptr+"/"+strconv.Itoa(i), out); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		key := ptr
		if strings.Count(ptr, "/") == 1 {
			key = strings.TrimPrefix(ptr, "/")
		}
		if n.Tag == "!!null" {
			out[key] = ""
		} else {
			out[key] = n.Value
		}
	default:
		return fmt.Errorf("unsupported YAML node")
	}
	return nil
}
func escape(s string) string { return strings.ReplaceAll(strings.ReplaceAll(s, "~", "~0"), "/", "~1") }
func unescape(s string) (string, error) {
	for i := 0; i < len(s); i++ {
		if s[i] == '~' {
			if i+1 >= len(s) || (s[i+1] != '0' && s[i+1] != '1') {
				return "", fmt.Errorf("invalid JSON Pointer escape")
			}
			i++
		}
	}
	return strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~"), nil
}
func selectorForPointer(pointer string) (string, error) {
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	var b strings.Builder
	for _, p := range parts {
		p, err := unescape(p)
		if err != nil {
			return "", err
		}
		if n, err := strconv.Atoi(p); err == nil {
			b.WriteString("[")
			b.WriteString(strconv.Itoa(n))
			b.WriteString("]")
		} else {
			q, _ := json.Marshal(p)
			b.WriteString("[")
			b.Write(q)
			b.WriteString("]")
		}
	}
	return b.String(), nil
}
func verifyEncryptedPointer(data []byte, pointer string) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("parse encrypted result: %w", err)
	}
	if !bytes.Contains(data, []byte("ENC[")) {
		return fmt.Errorf("sops result left target cleartext")
	}
	return nil
}
func redact(s, value string) string { return strings.ReplaceAll(s, value, "[REDACTED]") }

var _ io.Reader
