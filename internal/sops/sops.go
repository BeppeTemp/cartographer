// Package sops decrypts sops-encrypted KB secrets and resolves the references a
// concept declares, turning them into the environment a skill runs with. It
// shells out to the sops binary: no key material is ever handled in process.
//
// Every sops invocation runs in a hermetic environment (D260): the child gets
// only a short allowlist of the server's variables, a fresh empty HOME and
// XDG_CONFIG_HOME, and the per-KB SOPS_AGE_KEY_FILE the caller passes. sops
// tries every identity it can find, so an ambient SOPS_AGE_KEY, a default
// keys.txt, an SSH key under ~/.ssh or cloud KMS credentials would otherwise
// decrypt any KB whatever its configured key file says.
package sops

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

// ErrDecrypt marks a failed decryption: the key is missing, invalid, or not a
// recipient of the file. A caller maps it to its own words (which KB).
var ErrDecrypt = errors.New("sops decrypt failed")

// Error is an error whose text is safe to hand to a remote caller: it names the
// step and the KB-relative path, never a server path nor sops diagnostics. The
// detail (the wrapped error, sops stderr) is for the server log; read it with
// Detail.
type Error struct {
	msg    string
	kind   error
	detail error
}

func (e *Error) Error() string   { return e.msg }
func (e *Error) Unwrap() error   { return e.detail }
func (e *Error) Is(t error) bool { return e.kind != nil && t == e.kind }

func newError(kind error, detail error, format string, a ...any) *Error {
	return &Error{msg: fmt.Sprintf(format, a...), kind: kind, detail: detail}
}

// Detail returns the full text of err for the server log: for an *Error the
// public text followed by the private detail.
func Detail(err error) string {
	var e *Error
	if errors.As(err, &e) && e.detail != nil {
		return e.msg + ": " + e.detail.Error()
	}
	return err.Error()
}

func Available() bool { _, err := exec.LookPath("sops"); return err == nil }

// Decrypt decrypts relativePath from kbRoot. SOPS always runs from kbRoot so
// repository-relative creation rules have deterministic semantics.
func Decrypt(kbRoot, relativePath string, env ...string) (*SecretFile, error) {
	if !Available() {
		return nil, fmt.Errorf("sops binary not found in PATH")
	}
	if err := validatePath(kbRoot, relativePath, false); err != nil {
		return nil, err
	}
	childEnv, cleanup, err := hermeticEnv(env)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd := exec.Command("sops", "decrypt", "--output-type", "yaml", relativePath)
	cmd.Dir = kbRoot
	cmd.Env = childEnv
	// Only stdout is the document: a warning sops prints on stderr during a
	// successful decrypt must never enter the YAML parse.
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, newError(ErrDecrypt, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String())), "could not decrypt %s", relativePath)
	}
	values, err := parseYAMLFlat(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("parse decrypted %s: %w", relativePath, err)
	}
	return &SecretFile{Path: relativePath, Values: values}, nil
}

// hermeticEnvAllowlist names the only server variables a sops child inherits,
// and only when they are set: what a process needs to find binaries, write
// temporary files and pick a locale — nothing that can carry an identity.
// Allowlist, not denylist: a denylist would miss the next identity source
// sops learns to read.
var hermeticEnvAllowlist = []string{"PATH", "TMPDIR", "TMP", "TEMP", "SYSTEMROOT", "LANG", "LC_ALL"}

// hermeticEnv builds the environment of a sops child from scratch (D260): the
// allowlisted variables present in the parent, HOME and XDG_CONFIG_HOME (and on
// Windows USERPROFILE and APPDATA) pointing at a fresh empty directory so no
// default keys.txt or SSH key is found, then the caller's entries, which win.
// cleanup removes the directory; it must run after the child has exited.
func hermeticEnv(extra []string) (env []string, cleanup func(), err error) {
	home, err := os.MkdirTemp("", "cartographer-sops-home-")
	if err != nil {
		// No fallback to the real home: that would reopen every ambient key.
		return nil, nil, newError(nil, err, "sops: cannot create an isolated home")
	}
	for _, name := range hermeticEnvAllowlist {
		if v, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+v)
		}
	}
	env = append(env, "HOME="+home, "XDG_CONFIG_HOME="+home)
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+home, "APPDATA="+home)
	}
	// os/exec keeps the last value of a duplicated key, so the caller's
	// entries (SOPS_AGE_KEY_FILE) override the allowlist.
	env = append(env, extra...)
	return env, func() { os.RemoveAll(home) }, nil
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
	return ResolveNamed(kbRoot, refs, nil, env...)
}

// ResolveNamed resolves refs; a non-empty only restricts resolution to the refs
// of those names, so a broken reference nobody asked for is never touched. A
// name that is not declared is an error.
func ResolveNamed(kbRoot string, refs []SecretRef, only []string, env ...string) (map[string]string, error) {
	cache := map[string]*SecretFile{}
	result := make(map[string]string)
	names := map[string]bool{}
	if len(only) > 0 {
		declared := map[string]SecretRef{}
		for _, ref := range refs {
			declared[ref.Name] = ref
		}
		picked := make([]SecretRef, 0, len(only))
		asked := map[string]bool{}
		for _, n := range only {
			ref, ok := declared[n]
			if !ok {
				return nil, fmt.Errorf("unknown name %q", n)
			}
			if asked[n] { // a name asked twice is resolved once
				continue
			}
			asked[n] = true
			picked = append(picked, ref)
		}
		refs = picked
	}
	for _, ref := range refs {
		if ref.Name == "" || names[ref.Name] {
			return nil, fmt.Errorf("duplicate or empty secret ref name %q", ref.Name)
		}
		names[ref.Name] = true
		if err := validatePath(kbRoot, ref.SOPSFile, false); err != nil {
			return nil, fmt.Errorf("reference %s: %w", ref.Name, err)
		}
		sf := cache[ref.SOPSFile]
		if sf == nil {
			var err error
			sf, err = Decrypt(kbRoot, ref.SOPSFile, env...)
			if err != nil {
				return nil, fmt.Errorf("reference %s: %w", ref.Name, err)
			}
			cache[ref.SOPSFile] = sf
		}
		val, ok := sf.Values[ref.SOPSKey]
		// Flat legacy YAML keeps top-level keys unprefixed for compatibility,
		// while secret_refs always use JSON Pointer syntax.
		if !ok && strings.HasPrefix(ref.SOPSKey, "/") && !strings.Contains(strings.TrimPrefix(ref.SOPSKey, "/"), "/") {
			val, ok = sf.Values[strings.TrimPrefix(ref.SOPSKey, "/")]
		}
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
		return newError(nil, err, "cannot read %s", relativePath)
	}
	originalNode, err := yamlDocument(raw)
	if err != nil {
		return fmt.Errorf("parse encrypted %s: %w", relativePath, err)
	}
	if !hasSOPSMetadata(originalNode) {
		return fmt.Errorf("%s is not SOPS-encrypted (missing sops metadata)", relativePath)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(original); err == nil {
		mode = info.Mode()
	}
	dir := filepath.Dir(original)
	// The copy keeps the target's extension: sops picks its store (YAML, JSON)
	// from it, and a copy without one is read as JSON (#1274).
	tmp, err := os.CreateTemp(dir, ".sops-set-*"+filepath.Ext(relativePath))
	if err != nil {
		return newError(nil, err, "cannot write a temporary copy of %s", relativePath)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err = tmp.Write(raw); err != nil {
		tmp.Close()
		return newError(nil, err, "cannot write a temporary copy of %s", relativePath)
	}
	if err = tmp.Close(); err != nil {
		return newError(nil, err, "cannot write a temporary copy of %s", relativePath)
	}
	if err = os.Chmod(tmpName, mode); err != nil {
		return newError(nil, err, "cannot write a temporary copy of %s", relativePath)
	}
	selector, err := selectorForPointer(originalNode, pointer)
	if err != nil {
		return err
	}
	encoded, _ := json.Marshal(value)
	tmpRel, err := filepath.Rel(kbRoot, tmpName)
	if err != nil || !filepath.IsLocal(tmpRel) {
		return fmt.Errorf("temporary secret path must stay inside the KB")
	}
	// SOPS 3.13 expects file before the selector. Keep the root cwd so its
	// repository-relative creation_rules/path_regex matching is unchanged.
	childEnv, cleanup, err := hermeticEnv(env)
	if err != nil {
		return err
	}
	defer cleanup()
	cmd := exec.Command("sops", "set", "--value-stdin", tmpRel, selector)
	cmd.Dir = kbRoot
	cmd.Stdin = bytes.NewReader(encoded)
	cmd.Env = childEnv
	_, err = cmd.CombinedOutput()
	if err != nil {
		// SOPS may include decrypted material in diagnostics. Never surface it.
		return newError(nil, err, "sops set failed on %s", relativePath)
	}
	updated, err := os.ReadFile(tmpName)
	if err != nil {
		return newError(nil, err, "cannot read the result of sops set on %s", relativePath)
	}
	if err := verifyEncryptedPointer(updated, pointer); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return newError(nil, err, "cannot replace %s", relativePath)
	}
	if err := os.Rename(tmpName, original); err != nil {
		return newError(nil, err, "cannot replace %s", relativePath)
	}
	return nil
}

// RefProblem says, without decrypting anything, why a secret_refs entry cannot
// resolve: its file is missing, or (sops keeps key names in clear) the pointer
// names no key of it. It returns "" when the reference looks fine or when the
// file cannot be judged (not YAML, no sops block). The text names only the
// KB-relative file and the pointer: it is shown to callers and lint output.
func RefProblem(kbRoot, file, pointer string) string {
	if err := validatePath(kbRoot, file, false); err != nil {
		return err.Error()
	}
	f, err := os.Open(filepath.Join(kbRoot, file))
	if err != nil {
		return file + " is not readable"
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDeclaredFileSize+1))
	if err != nil || len(data) > maxDeclaredFileSize {
		return ""
	}
	root, err := yamlDocument(data)
	if err != nil || !hasSOPSMetadata(root) {
		return ""
	}
	// Key names stay in clear in a sops file, so the pointer is walked as
	// written (a top-level key needs no legacy unprefixed form here).
	n, err := nodeAtPointer(root, pointer)
	if err != nil {
		return fmt.Sprintf("key %s not found in %s", pointer, file)
	}
	if n.Kind != yaml.ScalarNode {
		// ResolveNamed only knows scalar leaves: a mapping or a list is not a value.
		return fmt.Sprintf("key %s in %s is not a single value", pointer, file)
	}
	return ""
}

// validatePath refuses a path that is not relative and inside root, and any
// component of it that is not a plain regular file or a plain directory.
//
// The refusal is deliberately wider than "symlink", in line with
// internal/provisioning's isUnsafeDestination (D148 and D216): this function has
// exactly the same shape and exactly the same exposure — a component swapped for
// a link or, on Windows, for a reparse point Go may only describe as
// os.ModeIrregular, and a secret is read from or written to somewhere else. There
// is no reason for the two guards to disagree, and a divergence would be found
// the hard way.
//
// The check runs before sops opens the path, so a writer on the KB filesystem
// could swap a component between the two (a time-of-check/time-of-use window).
// Accepted (D261): that writer can already rewrite the KB, secrets/ included,
// so the window grants nothing it does not already have.
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
			if os.IsNotExist(err) {
				return newError(nil, err, "%s not found", rel)
			}
			return newError(nil, err, "%s is not readable", rel)
		}
		if mode := info.Mode(); !mode.IsRegular() && !mode.IsDir() {
			return fmt.Errorf("path %q must not contain symlinks or other non-plain files", rel)
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
			if k.Kind != yaml.ScalarNode || k.Tag != "!!str" {
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
func selectorForPointer(root *yaml.Node, pointer string) (string, error) {
	if !strings.HasPrefix(pointer, "/") {
		return "", fmt.Errorf("key must be a JSON Pointer starting with /")
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	n := root
	var b strings.Builder
	for i, raw := range parts {
		part, err := unescape(raw)
		if err != nil {
			return "", err
		}
		switch n.Kind {
		case yaml.MappingNode:
			q, _ := json.Marshal(part)
			b.WriteByte('[')
			b.Write(q)
			b.WriteByte(']')
			child, ok := mappingValue(n, part)
			if !ok {
				if i == len(parts)-1 {
					return b.String(), nil
				}
				return "", fmt.Errorf("JSON Pointer %q: missing mapping key %q", pointer, part)
			}
			n = child
		case yaml.SequenceNode:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(n.Content) {
				return "", fmt.Errorf("JSON Pointer %q: invalid sequence index %q", pointer, part)
			}
			b.WriteString("[")
			b.WriteString(strconv.Itoa(idx))
			b.WriteByte(']')
			n = n.Content[idx]
		default:
			return "", fmt.Errorf("JSON Pointer %q: cannot descend through scalar", pointer)
		}
	}
	return b.String(), nil
}
func verifyEncryptedPointer(data []byte, pointer string) error {
	n, err := yamlDocument(data)
	if err != nil {
		return fmt.Errorf("parse encrypted result: %w", err)
	}
	target, err := nodeAtPointer(n, pointer)
	if err != nil {
		return err
	}
	if target.Kind != yaml.ScalarNode {
		return fmt.Errorf("sops result target %q is not a scalar", pointer)
	}
	if !strings.HasPrefix(target.Value, "ENC[") {
		return fmt.Errorf("sops result left target %q cleartext", pointer)
	}
	return nil
}

func yamlDocument(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("root must be a mapping")
	}
	return doc.Content[0], nil
}
func mappingValue(n *yaml.Node, key string) (*yaml.Node, bool) {
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Kind == yaml.ScalarNode && n.Content[i].Value == key {
			return n.Content[i+1], true
		}
	}
	return nil, false
}
func hasSOPSMetadata(root *yaml.Node) bool {
	n, ok := mappingValue(root, "sops")
	return ok && n.Kind == yaml.MappingNode
}
func nodeAtPointer(root *yaml.Node, pointer string) (*yaml.Node, error) {
	if !strings.HasPrefix(pointer, "/") {
		return nil, fmt.Errorf("key must be a JSON Pointer starting with /")
	}
	n := root
	for _, raw := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		part, err := unescape(raw)
		if err != nil {
			return nil, err
		}
		switch n.Kind {
		case yaml.MappingNode:
			var ok bool
			n, ok = mappingValue(n, part)
			if !ok {
				return nil, fmt.Errorf("sops result target %q is missing", pointer)
			}
		case yaml.SequenceNode:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(n.Content) {
				return nil, fmt.Errorf("sops result target %q has invalid index %q", pointer, part)
			}
			n = n.Content[idx]
		default:
			return nil, fmt.Errorf("sops result target %q cannot descend through scalar", pointer)
		}
	}
	return n, nil
}

// maxDeclaredFileSize bounds what Declared reads of one file: a sops file of a
// KB is a few kilobytes.
const maxDeclaredFileSize = 1 << 20

// Declared reports whether the KB at kbRoot declares secrets (D906): at least
// one plain YAML file directly under secrets/ carries sops metadata. Nothing is
// decrypted, so it needs neither the sops binary nor a key. A secrets/ that is
// a link, a link or directory inside it, and a file that is not YAML or has no
// sops block all count for nothing.
func Declared(kbRoot string) bool {
	dir := filepath.Join(kbRoot, "secrets")
	if fi, err := os.Lstat(dir); err != nil || !fi.IsDir() {
		return false
	}
	items, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, it := range items {
		name := it.Name()
		if !it.Type().IsRegular() || !(strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".yml")) {
			continue
		}
		p := filepath.Join(dir, name)
		if fi, err := os.Lstat(p); err != nil || fi.Size() > maxDeclaredFileSize {
			continue
		}
		data, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		if root, err := yamlDocument(data); err == nil && hasSOPSMetadata(root) {
			return true
		}
	}
	return false
}
