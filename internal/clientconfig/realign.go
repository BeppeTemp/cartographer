package clientconfig

import (
	"errors"
	"fmt"
	"os"
	"strings"

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
	{Path: "token_env", Plan: 906, Replacement: "token", Note: "token: ${NAME} reads the variable NAME; the realignment rewrites it",
		Description: "Name of the environment variable that holds the bearer token."},
	{Path: "trust", Plan: 906, Note: "everything a KB ships is installed, and a KB's MCP descriptors still need `cartographer approve`; the signing_keys pins and the path checks stay",
		Description: "Treat the skills, agents, hooks and instructions a KB provides as trusted at every sync, without the one-time --auto-trust flag."},
	{Path: "write_findings_hook", Plan: 906, Note: "the hook that returns a write's findings to the agent is always installed",
		Description: "Install the hook that returns a write's findings to the agent as feedback (Claude Code, Codex). false makes the next sync remove it."},
	{Path: "search_depth", Plan: 906, Note: "it is a constant, 4; add a closer root to search_roots",
		Description: "How many directory levels the scan descends from each root. A value above 8 is clamped with a warning."},
	{Path: "update.check", Plan: 906, Replacement: "update_check",
		Description: "Look up the latest release once a day and tell when a newer one exists. false makes no lookup at all."},
	{Path: "update.policy", Plan: 906, Replacement: "update_check", Note: "a client never replaces itself: auto-patch is gone and the notice remains",
		Description: "What to do about a newer release: notify says so; auto-patch installs patch releases by itself when installed with Homebrew, install.sh or install.ps1."},
	{Path: "agents", Plan: 906, Note: "the connected clients are a record of the connect commands, kept in the client state file (.cartographer-state.json)",
		Description: "The agent clients connected to the server, for example claude or opencode. connect and disconnect maintain it."},
	{Path: "sync_timer_opt_out", Plan: 906, Note: "it is a record of `service sync-timer uninstall`, kept in the client machine file (.cartographer-machine.json)",
		Description: "Set by `service sync-timer uninstall`: connect and setup then stop installing the sync timer themselves."},
	{Path: "mcp_approvals", Note: "the local approvals are kept in the client machine file (.cartographer-machine.json), which fails closed when unreadable",
		Description: "Local approvals of third-party MCP descriptors, hooks and executable artifacts (stored as <kind>/<name>), bound to the exact content hash. Written by the approval commands; do not edit by hand.",
		Previous: []string{
			"Local approvals of third-party MCP descriptors, bound to the exact content hash. Written by the approval commands; do not edit by hand.",
		}},
}, Moved: []settingschema.Moved{
	movedTo("server_url", "server.url"),
	movedTo("server_name", "server.name"),
	movedTo("auth", "server.auth"),
	movedTo("token", "server.token"),
	movedTo("allow_insecure_http", "server.allow_insecure_http"),
	movedTo("search_roots", "placeholders.search_roots"),
	movedTo("paths", "placeholders.paths"),
	movedTo("ignored_paths", "placeholders.ignored"),
}}

// movedTo is the move of a key into a group (D1731). The
// comment the schema wrote above the old key is told from a person's by the
// wordings the new key's entry holds: the move changes the path, not the text.
func movedTo(from, to string) settingschema.Moved {
	k, _ := Schema.Lookup(to)
	return settingschema.Moved{From: from, To: to, Description: k.Description, Previous: k.Previous}
}

// layoutConflicts lists the keys that data sets at both the old and the new
// path of the layout with different values: Load and Save refuse such a file
// (the operator keeps one), and the realignment leaves it as it is.
func layoutConflicts(data []byte) []string { return Retired.MovedConflicts(data) }

// migrateLayout moves the keys of the old flat layout into their groups in
// data, comments kept, for a save over a file the realignment has not reached
// yet (a sync runs RealignFile first, which keeps the backup). Bytes without an
// old key are returned as they are; a conflict is an error.
func migrateLayout(data []byte) ([]byte, error) {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil {
		return data, nil // buildDocument reports it
	}
	root := documentMapping(&doc)
	if root == nil {
		return data, nil
	}
	pending := false
	for _, m := range Retired.Moved {
		pending = pending || pairIndex(root, m.From) >= 0
	}
	if !pending {
		return data, nil
	}
	if c := layoutConflicts(data); len(c) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(c, "; "))
	}
	out, _, err := Schema.Realign(data, settingschema.Registry{Moved: Retired.Moved})
	return out, err
}

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
	// The realignment copies the file's mode to the rewrite and to the backup:
	// with a literal token in it, close the file first so neither is open.
	if data, rerr := os.ReadFile(Path(dir)); rerr == nil {
		var y struct {
			Token  string `yaml:"token"`
			Server struct {
				Token string `yaml:"token"`
			} `yaml:"server"`
		}
		if yaml.Unmarshal(data, &y) == nil && ((&Config{Token: y.Token}).LiteralToken() != "" || (&Config{Token: y.Server.Token}).LiteralToken() != "") {
			if terr := tightenFile(Path(dir)); terr != nil {
				return settingschema.Report{}, "", terr
			}
		}
	}
	return Schema.RealignFileWith(Path(dir), Retired, version, func(data []byte) ([]byte, []settingschema.Change, error) {
		return prepareRealign(dir, data)
	})
}

// tokenLabel names a token value for a message: a ${NAME} reference as it is,
// anything else as "a literal token", never its text.
func tokenLabel(v string) string {
	if strings.TrimSpace(v) == "" {
		return "empty"
	}
	if IsEnvRef(v) {
		return strings.TrimSpace(v)
	}
	return "a literal token"
}

func prepareRealign(dir string, data []byte) ([]byte, []settingschema.Change, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return data, nil, nil // Realign reports a file it cannot read
	}
	root := doc.Content[0]
	var changes []settingschema.Change

	// agents into the state file, and the approvals and the opt-outs into the
	// machine file (D1728); then the registry drops them from the
	// YAML. The machine file is written first, with its backups.
	if pairIndex(root, "agents") >= 0 || pairIndex(root, "sync_timer_opt_out") >= 0 || pairIndex(root, "mcp_approvals") >= 0 {
		cfg, err := Load(dir)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return data, nil, fmt.Errorf("moving agents to the state file: %w", err)
		}
		if err == nil {
			if cfg.machineBroken != nil {
				// Never drop the old keys while the file that should hold their
				// values cannot be read: they are all that is left of them.
				return data, nil, fmt.Errorf("the machine file is unreadable (%v): not moving the approvals, fix or remove it first", cfg.machineBroken)
			}
			movedMachine, err := writeMachineChanged(dir, cfg)
			if err != nil {
				return data, nil, fmt.Errorf("moving the approvals to the machine file: %w", err)
			}
			if movedMachine {
				changes = append(changes, settingschema.Change{Kind: settingschema.ChangeMoved, Path: "mcp_approvals, sync_timer_opt_out", Detail: "their values are in " + MachinePath(dir)})
			}
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
	// token_env: NAME becomes token: ${NAME}, in place, comments kept. It is
	// never dropped without a word: when token already holds a value, or NAME
	// cannot be an environment variable, the key is removed here with a change
	// that names what was lost (the registry would drop it silently).
	if i := pairIndex(root, "token_env"); i >= 0 {
		v := root.Content[i+1]
		// The token sits at the root or, in the grouped layout, under server.
		tokParent, j := root, pairIndex(root, "token")
		if g := lookup(&doc, "server"); j < 0 && g != nil && g.Kind == yaml.MappingNode {
			if k := pairIndex(g, "token"); k >= 0 {
				tokParent, j = g, k
			}
		}
		valid := v.Kind == yaml.ScalarNode && tokenEnvRef.MatchString(EnvRefToken(v.Value))
		tokenEmpty := j < 0 || (tokParent.Content[j+1].Kind == yaml.ScalarNode && strings.TrimSpace(tokParent.Content[j+1].Value) == "")
		switch {
		case valid && j < 0:
			name := v.Value
			root.Content[i].Value = "token"
			v.Value, v.Tag, v.Style = EnvRefToken(name), "!!str", 0
			changes = append(changes, settingschema.Change{Kind: settingschema.ChangeMoved, Path: "token_env", Detail: "now token: ${…}, the variable token_env named"})
			edited = true
		case valid && tokenEmpty:
			tv := tokParent.Content[j+1]
			ref := EnvRefToken(v.Value)
			tv.Kind, tv.Value, tv.Tag, tv.Style = yaml.ScalarNode, ref, "!!str", 0
			removePair(&doc, root, i)
			changes = append(changes, settingschema.Change{Kind: settingschema.ChangeMoved, Path: "token_env", Detail: "now token: ${…}, the variable token_env named (token was empty)"})
			edited = true
		case valid && tokParent.Content[j+1].Value == EnvRefToken(v.Value):
			removePair(&doc, root, i)
			changes = append(changes, settingschema.Change{Kind: settingschema.ChangeRemoved, Path: "token_env", Detail: "token already is " + EnvRefToken(v.Value)})
			edited = true
		default:
			// The value is never echoed: someone may have pasted the token itself
			// into token_env, and an alphanumeric token passes as a name.
			var detail string
			if valid {
				detail = fmt.Sprintf("token_env was dropped because token is already set (%s); to read that variable instead, set token: ${NAME}", tokenLabel(tokParent.Content[j+1].Value))
			} else {
				detail = "token_env is not an environment variable name and cannot become token: ${NAME}; set token to the reference you want"
				if !tokenEmpty {
					detail += fmt.Sprintf(" (token stays %s)", tokenLabel(tokParent.Content[j+1].Value))
				}
			}
			removePair(&doc, root, i)
			changes = append(changes, settingschema.Change{Kind: settingschema.ChangeRemoved, Path: "token_env", Detail: detail})
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
