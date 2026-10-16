package config

import (
	"strings"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// Redacted is how a secret that is set is written in a settings YAML view.
const Redacted = "<redacted>"

// ServerYAML is the server's effective configuration as the YAML file it
// amounts to (D818): every key whose value does not come from its default, at
// its path, and the kbs[] entries of the KBs given, each documented by the
// schema, with the keys left at their default listed under the
// available-settings block (D747). A secret is written redacted; a value that
// comes from the environment or a flag says so in a line comment, since no
// file holds it. kbs is the per-KB half of the same View, already filtered to
// what the caller sees.
func (v View) ServerYAML() ([]byte, error) {
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	for _, e := range v.Server {
		if e.Key == "kbs" || e.Key == "auth.roles" {
			continue
		}
		if n := entryNode(e, false); n != nil {
			settingschema.Set(doc, e.Key, n)
		}
	}
	for _, e := range v.Server {
		if e.Key == "auth.roles" && e.Origin != OriginDefault {
			settingschema.Set(doc, e.Key, rolesNode(e.Value))
		}
	}
	if kbs := v.serverKBsNode(); kbs != nil {
		settingschema.Set(doc, "kbs", kbs)
	}
	Schema.Render(doc)
	return settingschema.Encode(doc)
}

// KBYAML is one KB's effective settings as the settings.yaml it amounts to
// (D746): every key that does not come from its default, documented by the
// KB schema, the rest listed as available. A line comment names where a value
// comes from when it is not the KB's own file: the server's kbs[] entry, the
// environment or a flag. ok is false when the View has no such KB.
func (v View) KBYAML(name string) (out []byte, ok bool, err error) {
	for _, section := range v.KBs {
		if section.Name != name {
			continue
		}
		doc := &yaml.Node{Kind: yaml.DocumentNode}
		for _, e := range section.Entries {
			if n := entryNode(e, true); n != nil {
				settingschema.Set(doc, strings.TrimPrefix(e.Key, "kbs[]."), n)
			}
		}
		KBSettingsSchema().Render(doc)
		out, err = settingschema.Encode(doc)
		return out, true, err
	}
	return nil, false, nil
}

// serverKBsNode is the kbs list as the server file carries it: per KB, the
// keys set in its kbs[] entry or by an override, never those the KB's own
// settings.yaml sets (that is the KB's file, shown with the KB). A KB with
// none is left out: it was discovered in the data directory.
func (v View) serverKBsNode() *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, section := range v.KBs {
		item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, e := range section.Entries {
			if e.Origin == OriginKB {
				continue
			}
			if n := entryNode(e, false); n != nil {
				item.Content = append(item.Content, scalar(strings.TrimPrefix(e.Key, "kbs[].")), n)
			}
		}
		if len(item.Content) == 0 {
			continue
		}
		item.HeadComment = "KB " + section.Name
		seq.Content = append(seq.Content, item)
	}
	if len(seq.Content) == 0 {
		return nil
	}
	return seq
}

// entryNode is the YAML value of one entry, or nil when it is at its default.
// inKB words the line comment for a KB's settings.yaml, where a value from the
// server's own file is the one that needs saying.
func entryNode(e Entry, inKB bool) *yaml.Node {
	if e.Origin == OriginDefault {
		return nil
	}
	var n *yaml.Node
	if e.Secret {
		if e.Value != SecretSet {
			return nil
		}
		n = scalar(Redacted)
	} else {
		n = &yaml.Node{}
		if err := n.Encode(e.Value); err != nil {
			return nil
		}
	}
	switch {
	case e.Origin == OriginEnv && e.Env != "":
		n.LineComment = "from " + e.Env
	case e.Origin == OriginFlag && e.Flag != "":
		n.LineComment = "from " + e.Flag
	case e.Origin == OriginFile && inKB:
		n.LineComment = "from the server's kbs[] entry"
	}
	return n
}

// rolesNode writes the configured roles by name: the settings view carries
// their names, not their rules, so the rules are said to be elsewhere.
func rolesNode(v any) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	names, _ := v.([]string)
	for _, name := range names {
		seq.Content = append(seq.Content, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map",
			Content: []*yaml.Node{scalar("name"), scalar(name)}})
	}
	seq.LineComment = "rules not shown here"
	return seq
}

func scalar(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }
