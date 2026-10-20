package clientconfig

import (
	"errors"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// Retired is the registry of the .cartographer.yaml keys that were removed, and
// of the values that switched a feature off and are reset to the default
// (D903): Load ignores a removed key with a warning that names what to use, and
// RealignFile drops it. Plans that remove or reset a client key add it here.
//
// The keys the cache split took out (D747) and doctor_timer_opt_out (D704)
// predate it and are dropped by Save (droppedKeys). A key that changed shape
// rather than disappeared (token_env, update.*) or home (agents,
// sync_timer_opt_out) is carried over by the Prepare step of RealignFile before
// this registry drops what is left of it.
var Retired = settingschema.Registry{Removed: []settingschema.Removed{
	{Path: "token_env", Plan: 906, Replacement: "token", Note: "token: ${NAME} reads the variable NAME; the realignment rewrites it"},
	{Path: "trust", Plan: 906, Note: "everything a KB ships is installed, and a KB's MCP descriptors still need `cartographer approve`; the signing_keys pins and the path checks stay"},
	{Path: "write_findings_hook", Plan: 906, Note: "the hook that returns a write's findings to the agent is always installed"},
	{Path: "search_depth", Plan: 906, Note: "it is a constant, 4; add a closer root to search_roots"},
	{Path: "update.check", Plan: 906, Replacement: "update_check"},
	{Path: "update.policy", Plan: 906, Replacement: "update_check", Note: "a client never replaces itself: auto-patch is gone and the notice remains"},
	{Path: "agents", Plan: 906, Note: "the connected clients are a record of the connect commands, kept in the client state file (.cartographer-state.json)"},
	{Path: "sync_timer_opt_out", Plan: 906, Note: "it is a record of `service sync-timer uninstall`, kept in the client state file (.cartographer-state.json)"},
}}

// RealignFile brings the client config in dir up to date (D903): the removed
// keys dropped, off values reset to the default, the schema's comments written,
// and the original kept beside it as .cartographer.yaml.bak-<version>. It
// reports what it changed, and does nothing, writing no backup, to a file that
// is already in that form, or that does not exist. `sync` runs it, under the
// client-state lock, before it loads the file.
//
// The values of the keys that moved are carried over first (D906): token_env
// becomes `token: ${NAME}`, update.check becomes update_check (an
// update.policy of auto-patch becomes update_check: true, and no client
// replaces itself), and agents and sync_timer_opt_out go to the state file. The
// state file is written before the YAML loses them, and a state that cannot be
// written stops the realignment: the keys stay where they are, and still work.
func RealignFile(dir, version string) (rep settingschema.Report, backup string, err error) {
	return Schema.RealignFileWith(Path(dir), Retired, version, func(data []byte) ([]byte, []settingschema.Change, error) {
		return prepareRealign(dir, data)
	})
}

func prepareRealign(dir string, data []byte) ([]byte, []settingschema.Change, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data, nil, nil // Realign reports a file it cannot read
	}
	root := doc.Content[0]
	var changes []settingschema.Change

	// agents and sync_timer_opt_out: into the state file, then the registry
	// drops them from the YAML.
	if pairIndex(root, "agents") >= 0 || pairIndex(root, "sync_timer_opt_out") >= 0 {
		cfg, err := Load(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return data, nil, fmt.Errorf("moving agents to the state file: %w", err)
		}
		if err == nil {
			wrote, err := writeStateChanged(dir, cfg)
			if err != nil {
				return data, nil, fmt.Errorf("moving agents to the state file: %w", err)
			}
			if wrote {
				changes = append(changes, settingschema.Change{Kind: settingschema.ChangeMoved, Path: "agents, sync_timer_opt_out", Detail: "their values are in " + StatePath(dir)})
			}
		}
	}

	edited := false
	// token_env: NAME becomes token: ${NAME}, in place, comments kept.
	if i := pairIndex(root, "token_env"); i >= 0 && pairIndex(root, "token") < 0 {
		v := root.Content[i+1]
		if v.Kind == yaml.ScalarNode && tokenEnvRef.MatchString(EnvRefToken(v.Value)) {
			name := v.Value
			root.Content[i].Value = "token"
			v.Value, v.Tag, v.Style = EnvRefToken(name), "!!str", 0
			changes = append(changes, settingschema.Change{Kind: settingschema.ChangeMoved, Path: "token_env", Detail: "now token: " + v.Value})
			edited = true
		}
	}
	// update: {check, policy}: update_check, in the block's place.
	if i := pairIndex(root, "update"); i >= 0 {
		if m := root.Content[i+1]; m.Kind == yaml.MappingNode {
			var check, policy string
			if j := pairIndex(m, "check"); j >= 0 {
				check = m.Content[j+1].Value
			}
			if j := pairIndex(m, "policy"); j >= 0 {
				policy = m.Content[j+1].Value
			}
			want := ""
			switch {
			case check == "true" || check == "false":
				want = check
			case policy == "auto-patch":
				want = "true"
			}
			if want != "" && pairIndex(root, "update_check") < 0 {
				root.Content[i] = scalarKey("update_check")
				root.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: want}
				changes = append(changes, settingschema.Change{Kind: settingschema.ChangeMoved, Path: "update", Detail: "now update_check: " + want + "; a client never replaces itself"})
			} else {
				removePair(&doc, root, i)
				changes = append(changes, settingschema.Change{Kind: settingschema.ChangeRemoved, Path: "update", Detail: "a client never replaces itself; update_check is true unless set"})
			}
			edited = true
		}
	}
	if !edited {
		return data, changes, nil
	}
	out, err := settingschema.Encode(&doc)
	if err != nil {
		return data, nil, err
	}
	return out, changes, nil
}
