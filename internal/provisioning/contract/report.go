package contract

import (
	"io/fs"
	"path"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/configurator"
)

// UnverifiedFor is the capabilities one artifact depends on while their status
// is `unverified`: the agent pair for a native agent, the event of a hook. It
// judges neither authorization nor destination; the callers do.
func UnverifiedFor(a Artifact, provider configurator.Provider, scope Scope, bundleFS fs.FS, kbRoots map[string]string) []Capability {
	var out []Capability
	switch a.Kind {
	case "agent":
		if StrictAgentSkipped(a, provider) {
			return nil
		}
		d, i := CapabilityStatus(provider, scope, CapAgentDelegate), CapabilityStatus(provider, scope, CapAgentInvoke)
		if d == CapUnverified && i != CapVerified {
			out = append(out, CapAgentDelegate)
		}
		if i == CapUnverified && d != CapVerified {
			out = append(out, CapAgentInvoke)
		}
	case "hook":
		if IsReservedHook(a.Kind, a.Name) {
			return nil
		}
		spec, ok := HookSpecOf(a, bundleFS, kbRoots)
		if !ok || !HookEventKnown(spec.Event) {
			return nil
		}
		id := Capability(HookCapPrefix + spec.Event)
		if CapabilityStatus(provider, scope, id) == CapUnverified {
			out = append(out, id)
		}
	}
	return out
}

// HookSpecOf reads the hook.json of a hook artifact, from memory or from its
// source; ok is false when it is absent or unreadable.
func HookSpecOf(a Artifact, bundleFS fs.FS, kbRoots map[string]string) (HookSpec, bool) {
	files := a.Files
	if len(files) == 0 {
		var err error
		if files, err = ReadArtifactFiles(a, bundleFS, kbRoots); err != nil {
			return HookSpec{}, false
		}
	}
	for _, f := range files {
		if path.Clean(filepath.ToSlash(f.Path)) != "hook.json" {
			continue
		}
		spec, err := ParseHookSpec(f.Content)
		return spec, err == nil
	}
	return HookSpec{}, false
}
