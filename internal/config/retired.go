package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// noteRetired records, as warnings, the removed keys the file still carries
// (D903): each is ignored, and the line names what to use instead. A removed
// key that cannot be ignored without changing where writes go (the server git
// profile; a kbs[].git_branch, which is renamed kbs[].branch) is a refusal as
// well. An entry of kbs[] with no
// remote, which names nothing to clone, is only a warning.
func (c *Config) noteRetired(data []byte, specs []KBSpec) {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return
	}
	for _, h := range Retired.Find(&doc) {
		c.Warnings = append(c.Warnings, h.Message())
		if msg, ok := h.Refused(); ok {
			c.Refusals = append(c.Refusals, msg)
		}
	}
	for i, spec := range specs {
		if spec.Remote == "" {
			c.Warnings = append(c.Warnings, fmt.Sprintf("kbs[%d]: no remote, so there is nothing to clone and the entry is ignored; a KB that is a folder under data is discovered without one", i))
		}
	}
}

// noteRetiredEnv does the same for the environment variables and, in
// Retired, the removed settings they used to feed.
func (c *Config) noteRetiredEnv() {
	for _, e := range Retired.Removed {
		if e.Env == "" {
			continue
		}
		v := os.Getenv(e.Env)
		if v == "" {
			continue
		}
		c.Warnings = append(c.Warnings, fmt.Sprintf("%s: removed setting, ignored (%s)", e.Env, e.Advice()))
		if e.Refuses(v) {
			c.Refusals = append(c.Refusals, fmt.Sprintf("%s=%s: %s", e.Env, v, e.Note))
		}
	}
}

// RefusalError is the error serve stops with when a retired setting that cannot
// be honoured is still configured; nil when there is none.
func (c *Config) RefusalError() error {
	if len(c.Refusals) == 0 {
		return nil
	}
	return fmt.Errorf("config: refusing to start, a removed setting cannot be ignored without changing where writes go:\n  - %s", strings.Join(c.Refusals, "\n  - "))
}

// RemovedFlags are the command-line flags of removed settings, with the advice
// that goes with each (D903): serve drops them from its arguments and warns, so
// a service definition written for an older version still starts.
func RemovedFlags() map[string]string {
	out := map[string]string{}
	for _, e := range Retired.Removed {
		if e.Flag != "" {
			out[strings.TrimLeft(e.Flag, "-")] = e.Advice()
		}
	}
	return out
}

// RemovedValueFlags are the removed flags that take their value as the next
// argument (--tools-profile full): serve drops that argument with the flag.
func RemovedValueFlags() map[string]bool {
	out := map[string]bool{}
	for _, e := range Retired.Removed {
		if e.Flag != "" && e.FlagValue {
			out[strings.TrimLeft(e.Flag, "-")] = true
		}
	}
	return out
}

// RealignServerFile brings the server config.yaml at path up to date (D903):
// the removed keys are dropped, off values reset to the default, the schema's
// comments written (only when something else needed to change), and the original kept beside it as <file>.bak-<version>.
// It reports what it changed, and does nothing, writing no backup, to a file
// that is already in that form.
func RealignServerFile(path, version string) (rep settingschema.Report, backup string, err error) {
	return RealignServerFileKeeping(path, version)
}

// RealignServerFileKeeping is RealignServerFile except that the removed keys
// named in keep (by path) stay in the file: a key whose value could not be
// moved elsewhere yet is not dropped with the others.
func RealignServerFileKeeping(path, version string, keep ...string) (rep settingschema.Report, backup string, err error) {
	reg := Retired
	if len(keep) > 0 {
		reg.Removed = nil
		for _, e := range Retired.Removed {
			kept := false
			for _, k := range keep {
				kept = kept || e.Path == k
			}
			if !kept {
				reg.Removed = append(reg.Removed, e)
			}
		}
	}
	// A file that only lacks the schema's comments is the operator's own, fresh
	// or hand-written, and is left untouched: no rewrite, no backup. Only
	// content that needs migrating (a removed or reset key) is rewritten.
	if data, rerr := os.ReadFile(path); rerr == nil {
		if _, r, perr := Schema.Realign(data, reg); perr == nil && !r.Substantive() {
			return settingschema.Report{}, "", nil
		}
	}
	return Schema.RealignFile(path, reg, version)
}

// RealignKBSettings is the realignment of a KB's settings.yaml, over its bytes:
// the server commits the result itself (kb.KB.RealignSettings), so the commit is
// the backup.
func RealignKBSettings(data []byte) ([]byte, settingschema.Report, error) {
	return KBSettingsSchema().Realign(data, kb.SettingsRetired)
}
