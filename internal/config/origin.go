package config

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// Origin says which layer set a setting's effective value (D743). The loaders
// record it as they apply a layer: reconstructing it by diffing against the
// defaults would report a value written in the file as "default" whenever it
// equals the default.
type Origin string

const (
	OriginDefault Origin = "default"
	OriginFile    Origin = "file"
	OriginEnv     Origin = "env"
	OriginFlag    Origin = "flag"
)

// KBOriginKey is the origin-map key of a per-KB setting: keyed by the KB's
// mounted name, not its list index, so it survives reordering.
func KBOriginKey(kbName, key string) string { return "kbs[" + kbName + "]." + key }

// MountName is the name this entry mounts under when it does not set one:
// the derived name of its remote or path (the same rule as `serve`).
func (s KBSpec) MountName() string {
	if s.Name != "" {
		return s.Name
	}
	if s.Remote != "" {
		return RemoteKBName(s.Remote)
	}
	return filepath.Base(strings.TrimRight(s.Path, `/\`))
}

// RemoteKBName derives a KB name from a git remote URL or path.
func RemoteKBName(remote string) string {
	r := strings.TrimSuffix(strings.TrimRight(remote, "/\\"), ".git")
	// Backslash is a separator too: a local Windows path used as a remote
	// (file:///C:/... or C:\repos\kb.git) would otherwise yield a name that
	// still contains separators, and the caller joins it onto the data
	// directory — a name with a separator in it escapes that directory.
	if idx := strings.LastIndexAny(r, "/:\\"); idx >= 0 {
		r = r[idx+1:]
	}
	return r
}

// OriginOf returns the layer that set the setting at a schema path
// ("git.sync", or KBOriginKey for a per-KB key). A path no layer touched is
// the default.
func (c *Config) OriginOf(path string) Origin {
	if c.originFromKB(path) {
		return OriginKB
	}
	if o, ok := c.Origins[path]; ok {
		return o
	}
	return OriginDefault
}

func (c *Config) mark(o Origin, paths ...string) {
	if c.Origins == nil {
		c.Origins = map[string]Origin{}
	}
	for _, p := range paths {
		c.Origins[p] = o
	}
}

// markKB records a KB added (by env or flag) whose only key is path or remote.
func (c *Config) markKB(o Origin, spec KBSpec) {
	key := "path"
	if spec.Remote != "" {
		key = "remote"
	}
	c.mark(o, "kbs", KBOriginKey(spec.MountName(), key))
}

// markFile records every schema key present in the YAML document, whatever its
// value: a key written equal to its default is still set by the file.
// specs are the entries of kbs, in order, to name the per-KB keys.
func (c *Config) markFile(data []byte, specs []KBSpec) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || len(doc.Content) == 0 {
		return
	}
	var walk func(n *yaml.Node, prefix string)
	walk = func(n *yaml.Node, prefix string) {
		if n.Kind != yaml.MappingNode {
			return
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			path := prefix + n.Content[i].Value
			val := n.Content[i+1]
			if _, ok := Schema.Lookup(path); ok {
				c.mark(OriginFile, path)
				if path == "kbs" && val.Kind == yaml.SequenceNode {
					for j, item := range val.Content {
						if j >= len(specs) || item.Kind != yaml.MappingNode {
							continue
						}
						name := specs[j].MountName()
						for k := 0; k+1 < len(item.Content); k += 2 {
							c.mark(OriginFile, KBOriginKey(name, item.Content[k].Value))
						}
					}
				}
				continue
			}
			walk(val, path+".")
		}
	}
	walk(doc.Content[0], "")
}

// Entry is one setting as the config view shows it.
type Entry struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Origin      Origin `json:"origin"`
	Description string `json:"description"`
	Default     string `json:"default"`
	Env         string `json:"env,omitempty"`
	Flag        string `json:"flag,omitempty"`
	Restart     bool   `json:"restart,omitempty"`
	Secret      bool   `json:"secret,omitempty"`
}

// KBEntries is the settings of one mounted KB.
type KBEntries struct {
	Name    string  `json:"name"`
	Entries []Entry `json:"entries"`
}

// View is every setting with its effective value and origin: the server keys
// and one section per KB. A secret is never in it, only "(set)" or "(unset)".
type View struct {
	Server []Entry     `json:"server"`
	KBs    []KBEntries `json:"kbs"`
}

const (
	SecretSet   = "(set)"
	SecretUnset = "(unset)"
)

// Settings builds the view. kbNames are the mounted KBs to give a section to;
// a KB with no entry in kbs[] (discovered under data) shows only defaults.
func (c *Config) Settings(kbNames []string) View {
	v := View{Server: []Entry{}, KBs: []KBEntries{}}
	for _, k := range Schema.Keys {
		if strings.HasPrefix(k.Path, "kbs[].") {
			continue
		}
		val, set := c.serverValue(k.Path)
		v.Server = append(v.Server, c.entry(k, k.Path, val, set))
	}
	names := append([]string(nil), kbNames...)
	sort.Strings(names)
	for _, name := range names {
		var spec KBSpec
		for _, s := range c.KBs {
			if s.MountName() == name {
				spec = s
				break
			}
		}
		// The effective spec, with the KB's own settings.yaml laid over it
		// (D746), when the server has resolved it; a discovered KB has no
		// kbs[] entry and is only here.
		if eff, ok := c.kbSpecFor(name); ok {
			spec = eff
		}
		fields := kbFields(spec)
		section := KBEntries{Name: name, Entries: []Entry{}}
		for _, k := range Schema.Keys {
			key, ok := strings.CutPrefix(k.Path, "kbs[].")
			if !ok {
				continue
			}
			val, set := fields[key]
			if !set {
				val = k.Default
			}
			section.Entries = append(section.Entries, c.entry(k, KBOriginKey(name, key), val, set))
		}
		v.KBs = append(v.KBs, section)
	}
	return v
}

func (c *Config) entry(k settingschema.Key, originKey string, val any, set bool) Entry {
	e := Entry{Key: k.Path, Value: val, Origin: c.OriginOf(originKey), Description: k.Description,
		Default: k.Default, Env: k.Env, Flag: k.Flag, Restart: k.Restart, Secret: k.Secret}
	if k.Secret {
		e.Value = SecretUnset
		if set {
			e.Value = SecretSet
		}
	}
	return e
}

// kbFields is the KB entry as its YAML keys: only the keys it sets.
func kbFields(s KBSpec) map[string]any {
	out := map[string]any{}
	b, err := yaml.Marshal(s)
	if err != nil {
		return out
	}
	_ = yaml.Unmarshal(b, &out)
	return out
}

// serverValue is the effective value of a server-level key and whether it is
// non-empty (what "(set)" means for a secret). TestServerValueCoversSchema
// keeps it in step with Schema.
func (c *Config) serverValue(path string) (any, bool) {
	str := func(s string) (any, bool) { return s, s != "" }
	switch path {
	case "http":
		return str(c.HTTP)
	case "init":
		return c.Init, c.Init
	case "data":
		return str(c.Data)
	case "update_check":
		return c.UpdateCheck, true
	case "auth.mode":
		return str(c.Auth.Mode)
	case "auth.tokens":
		return len(c.Auth.Tokens), len(c.Auth.Tokens) > 0
	case "auth.roles":
		names := []string{}
		for _, r := range c.Auth.Roles {
			names = append(names, r.Name)
		}
		return names, len(names) > 0
	case "kbs":
		names := []string{}
		for _, s := range c.KBs {
			names = append(names, s.MountName())
		}
		return names, len(names) > 0
	case "git.autocommit":
		return c.Git.Autocommit, true
	case "git.sync":
		return c.Git.Sync, true
	case "git.ssh_key":
		return str(c.Git.SSHKey)
	case "git.known_hosts":
		return str(c.Git.KnownHosts)
	case "git.author_name":
		return str(c.Git.AuthorName)
	case "git.author_email":
		return str(c.Git.AuthorEmail)
	case "git.committer_name":
		return str(c.Git.CommitterName)
	case "git.committer_email":
		return str(c.Git.CommitterEmail)
	case "git.token_dir":
		return str(c.Git.TokenDir)
	case "git.in_window":
		return c.Git.SyncInWindow.String(), true
	case "git.out_debounce":
		return c.Git.SyncOutDebounce.String(), true
	case "git.profile":
		return str(c.Git.Profile)
	case "git.base_branch":
		return str(c.Git.BaseBranch)
	case "git.working_branch":
		return str(c.Git.WorkingBranch)
	case "git.forge":
		return str(c.Git.Forge)
	case "git.github_owner":
		return str(c.Git.GitHubOwner)
	case "git.github_repository":
		return str(c.Git.GitHubRepository)
	case "git.github_api_url":
		return str(c.Git.GitHubAPIURL)
	case "git.github_token_env":
		return str(c.Git.GitHubTokenEnv)
	case "audit.log":
		return str(c.Audit.Log)
	case "audit.key_seed":
		return str(c.Audit.KeySeed)
	case "audit.mode":
		return str(c.Audit.Mode)
	case "audit.max_segment_bytes":
		return c.Audit.MaxSegmentBytes, true
	case "audit.archive_dir":
		return str(c.Audit.ArchiveDir)
	case "audit.retention_days":
		return c.Audit.RetentionDays, true
	case "sops.age_key_file":
		return str(c.Sops.AgeKeyFile)
	case "sops.age_key_dir":
		return str(c.Sops.AgeKeyDir)
	case "tools.profile":
		return str(c.ToolsProfile)
	case "mcp.allowed_origins":
		o := c.MCP.AllowedOrigins
		if o == nil {
			o = []string{}
		}
		return o, len(o) > 0
	case "web.enabled":
		return c.Web.Enabled, true
	}
	return fmt.Sprintf("unknown key %s", path), false
}
