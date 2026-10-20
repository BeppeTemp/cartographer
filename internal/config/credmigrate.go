package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LegacyCredential is one secret that a setting removed by D905 still holds: a
// file to copy into the credentials directory, or an inline value to write
// there.
type LegacyCredential struct {
	// Setting is where the secret is: the removed key ("kbs[1].ssh_key") or the
	// environment variable. A report names it, never the value.
	Setting string
	// Target is the file name in the credentials directory.
	Target string
	// From is the file to copy; "" when Value holds the secret.
	From  string
	Value string
}

// legacySecrets is the part of a server file that D905 moved to the
// credentials directory.
type legacySecrets struct {
	Git struct {
		SSHKey     string `yaml:"ssh_key"`
		KnownHosts string `yaml:"known_hosts"`
		TokenDir   string `yaml:"token_dir"`
	} `yaml:"git"`
	Sops struct {
		AgeKeyFile string `yaml:"age_key_file"`
		AgeKeyDir  string `yaml:"age_key_dir"`
	} `yaml:"sops"`
	Audit struct {
		KeySeed string `yaml:"key_seed"`
	} `yaml:"audit"`
	KBs []struct {
		Remote              string `yaml:"remote"`
		Name                string `yaml:"name"`
		Path                string `yaml:"path"`
		SSHKey              string `yaml:"ssh_key"`
		KnownHosts          string `yaml:"known_hosts"`
		SopsAgeKeyFile      string `yaml:"sops_age_key_file"`
		ArtifactSigningSeed string `yaml:"artifact_signing_seed"`
	} `yaml:"kbs"`
}

// LegacyCredentials lists the secrets that the server file data and the
// environment still hold in settings D905 removed, each with the file of the
// credentials directory it becomes. They come in the order the removed lookups
// preferred them, so the first one for a target is the one that was in effect;
// every known_hosts is listed, the one file holds them all. An environment
// variable wins over the key it fed, as it did. A value that cannot be resolved
// (an unset ${NAME}) or a directory that cannot be read is a problem, and the
// rest is still listed.
func LegacyCredentials(data []byte) (creds []LegacyCredential, problems []string) {
	var raw legacySecrets
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &raw); err != nil {
			return nil, []string{fmt.Sprintf("the config cannot be read for the credentials it holds: %v", err)}
		}
	}
	file := func(setting, target, from string) {
		if from != "" {
			creds = append(creds, LegacyCredential{Setting: setting, Target: target, From: from})
		}
	}
	inline := func(setting, target, value string) {
		if value == "" {
			return
		}
		v, err := expandEnvRef(value)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v; write %s by hand", setting, err, target))
			return
		}
		creds = append(creds, LegacyCredential{Setting: setting, Target: target, Value: strings.TrimSpace(v)})
	}
	// dir copies every <name>.<kind> of a removed per-KB directory, whose files
	// already had the names of the credentials directory.
	dir := func(setting, path, kind string) {
		if path == "" {
			return
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v; copy its *.%s files by hand", setting, err, kind))
			return
		}
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, "."+kind) || !safeCredentialName(strings.TrimSuffix(n, "."+kind)) {
				continue
			}
			if e.Type()&os.ModeSymlink != 0 {
				// A Kubernetes Secret volume, sops-nix and agenix present each
				// file as a link, which the removed lookup read through.
				fi, err := os.Stat(filepath.Join(path, n))
				if err != nil {
					problems = append(problems, fmt.Sprintf("%s: %v; copy %s by hand", setting, err, n))
					continue
				}
				if !fi.Mode().IsRegular() {
					continue
				}
			} else if !e.Type().IsRegular() {
				continue
			}
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			file(setting, n, filepath.Join(path, n))
		}
	}
	envOr := func(env, key, value string) (string, string) {
		if v := os.Getenv(env); v != "" {
			return env, v
		}
		return key, value
	}

	for i, kb := range raw.KBs {
		name := kb.Name
		if name == "" {
			name = RemoteKBName(kb.Remote)
		}
		if name == "" && kb.Path != "" {
			// A path KB is named after its folder, as resolveKBName does.
			name = filepath.Base(strings.TrimRight(kb.Path, string(os.PathSeparator)))
		}
		at := fmt.Sprintf("kbs[%d].", i)
		if !safeCredentialName(name) {
			if kb.SSHKey != "" || kb.SopsAgeKeyFile != "" || kb.ArtifactSigningSeed != "" {
				problems = append(problems, fmt.Sprintf("kbs[%d]: no usable KB name for its credential files; place them by hand", i))
			}
		} else {
			file(at+"ssh_key", name+"."+CredentialSSH, kb.SSHKey)
			file(at+"sops_age_key_file", name+"."+CredentialAge, kb.SopsAgeKeyFile)
			inline(at+"artifact_signing_seed", name+"."+CredentialSigning, kb.ArtifactSigningSeed)
		}
		file(at+"known_hosts", KnownHostsFile, kb.KnownHosts)
	}
	file("git.ssh_key", DefaultCredentialName+"."+CredentialSSH, raw.Git.SSHKey)
	file("git.known_hosts", KnownHostsFile, raw.Git.KnownHosts)
	setting, path := envOr("CARTOGRAPHER_GIT_TOKEN_DIR", "git.token_dir", raw.Git.TokenDir)
	dir(setting, path, CredentialToken)
	setting, path = envOr("CARTOGRAPHER_SOPS_AGE_KEY_DIR", "sops.age_key_dir", raw.Sops.AgeKeyDir)
	dir(setting, path, CredentialAge)
	setting, path = envOr("CARTOGRAPHER_SOPS_AGE_KEY_FILE", "sops.age_key_file", raw.Sops.AgeKeyFile)
	file(setting, DefaultCredentialName+"."+CredentialAge, path)
	if v := os.Getenv("CARTOGRAPHER_AUDIT_KEY"); v != "" {
		creds = append(creds, LegacyCredential{Setting: "CARTOGRAPHER_AUDIT_KEY", Target: AuditKeyFile, Value: strings.TrimSpace(v)})
	} else {
		inline("audit.key_seed", AuditKeyFile, raw.Audit.KeySeed)
	}
	return creds, problems
}
