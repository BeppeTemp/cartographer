package sops

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// SecretFile represents a decrypted SOPS file with key-value pairs.
type SecretFile struct {
	Path   string
	Values map[string]string
}

// Available checks whether the sops CLI binary is available in PATH.
func Available() bool {
	_, err := exec.LookPath("sops")
	return err == nil
}

// Version returns the sops version string, or error if not available.
func Version() (string, error) {
	out, err := exec.Command("sops", "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("sops --version: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// Decrypt decrypts a SOPS-encrypted YAML file using the sops CLI binary.
// The optional env vars (e.g. from AgeKeyEnv) are layered onto the
// subprocess environment, taking precedence over the process environment on
// key collision (same pattern as gitx.runGitEnv, D46) — a caller-provided
// value always wins.
func Decrypt(filePath string, env ...string) (*SecretFile, error) {
	if !Available() {
		return nil, fmt.Errorf("sops binary not found in PATH")
	}
	cmd := exec.Command("sops", "decrypt", "--output-type", "yaml", filePath)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("sops decrypt %s: %w: %s", filePath, err, string(out))
	}
	values, err := parseYAMLFlat(out)
	if err != nil {
		return nil, fmt.Errorf("parse decrypted %s: %w", filePath, err)
	}
	return &SecretFile{Path: filePath, Values: values}, nil
}

// DecryptAll decrypts all *.sops.yaml files under the given directory (non-recursive).
func DecryptAll(dir string, env ...string) ([]SecretFile, []error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.sops.yaml"))
	if err != nil {
		return nil, []error{err}
	}
	var files []SecretFile
	var errs []error
	for _, m := range matches {
		sf, err := Decrypt(m, env...)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		files = append(files, *sf)
	}
	return files, errs
}

// AgeKeyEnv returns the "SOPS_AGE_KEY_FILE=<path>" env entry for path, or
// nil if path is empty. Meant to be passed straight into Decrypt/DecryptAll/
// ResolveRefs to scope the age key used for decryption to a specific KB.
func AgeKeyEnv(path string) []string {
	if path == "" {
		return nil
	}
	return []string{"SOPS_AGE_KEY_FILE=" + path}
}

// SecretRef maps a logical secret name to a SOPS file path and key.
type SecretRef struct {
	Name     string // env var name
	SOPSFile string // path to *.sops.yaml
	SOPSKey  string // key within the SOPS file
}

// ResolveRefs resolves SecretRefs by decrypting needed SOPS files and extracting values.
func ResolveRefs(kbRoot string, refs []SecretRef, env ...string) (map[string]string, error) {
	cache := map[string]*SecretFile{}
	result := make(map[string]string)
	for _, ref := range refs {
		sopsPath := filepath.Join(kbRoot, ref.SOPSFile)
		sf, ok := cache[sopsPath]
		if !ok {
			var err error
			sf, err = Decrypt(sopsPath, env...)
			if err != nil {
				return nil, fmt.Errorf("resolve %s: %w", ref.Name, err)
			}
			cache[sopsPath] = sf
		}
		val, ok := sf.Values[ref.SOPSKey]
		if !ok {
			return nil, fmt.Errorf("key %q not found in %s", ref.SOPSKey, ref.SOPSFile)
		}
		result[ref.Name] = val
	}
	return result, nil
}

// EnvForSkill converts resolved refs into "NAME=value" strings for os/exec.Cmd.Env.
func EnvForSkill(resolved map[string]string) []string {
	env := make([]string, 0, len(resolved))
	for k, v := range resolved {
		env = append(env, k+"="+v)
	}
	return env
}

func parseYAMLFlat(data []byte) (map[string]string, error) {
	result := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		idx := strings.Index(line, ": ")
		if idx < 0 {
			if strings.HasSuffix(line, ":") {
				continue
			}
			continue
		}
		key := strings.TrimSpace(line[:idx])
		val := strings.TrimSpace(line[idx+2:])
		val = strings.Trim(val, `"'`)
		result[key] = val
	}
	return result, nil
}
