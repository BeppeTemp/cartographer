package main

import (
	"errors"

	"github.com/BeppeTemp/cartographer/internal/config"
	"gopkg.in/yaml.v3"
)

// The kb_aliases list is edited through the same yaml.Node document as the
// kbs[] entry, so a rename or an unmount is one read and one write of the
// server config: comments, key order and unmodelled fields survive, and there
// is no state in which the entry is rewritten and the alias is not (D772).

const kbAliasesKey = "kb_aliases"

// rootMapping returns the top-level mapping of the document, creating it for
// an empty file.
func rootMapping(doc *yaml.Node) (*yaml.Node, error) {
	if doc.Kind == 0 {
		doc.Kind = yaml.DocumentNode
	}
	if doc.Kind != yaml.DocumentNode {
		return nil, errors.New("not a YAML document")
	}
	if len(doc.Content) == 0 {
		doc.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("the top level is not a mapping")
	}
	return doc.Content[0], nil
}

// aliasItem returns the from and to of one kb_aliases item, "" for a missing
// key (config.Load has already rejected a file where that matters).
func aliasItem(item *yaml.Node) (from, to *yaml.Node) {
	return mapValue(item, "from"), mapValue(item, "to")
}

func setScalar(n *yaml.Node, value string) {
	n.Value = value
	n.Tag = "!!str"
	n.Style = 0
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// kbAliasSeq returns the kb_aliases sequence of root, nil when there is none.
func kbAliasSeq(root *yaml.Node) *yaml.Node {
	if seq := mapValue(root, kbAliasesKey); seq != nil && seq.Kind == yaml.SequenceNode {
		return seq
	}
	return nil
}

// dropKBAliasesKey removes an emptied kb_aliases key, so a config that never
// needed aliases again reads as it did before.
func dropKBAliasesKey(root *yaml.Node) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == kbAliasesKey {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			return
		}
	}
}

// recordKBRename keeps the list flat and single hop (D772): the item whose
// from is the restored name goes (that KB is mounted again), every item
// pointing at the old name now points at the new one, and old → new is added.
func recordKBRename(root *yaml.Node, old, newName string) {
	seq := kbAliasSeq(root)
	switch existing := mapValue(root, kbAliasesKey); {
	case seq != nil:
	case existing != nil:
		// `kb_aliases:` with no value, or `[]`: fill it in place.
		*existing = yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", HeadComment: existing.HeadComment, LineComment: existing.LineComment}
		seq = existing
	default:
		seq = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content, scalarNode(kbAliasesKey), seq)
		config.Schema.CommentKey(root, kbAliasesKey, kbAliasesKey)
	}
	kept := seq.Content[:0]
	for _, item := range seq.Content {
		from, to := aliasItem(item)
		if from != nil && (from.Value == newName || from.Value == old) {
			continue
		}
		if to != nil && to.Value == old {
			setScalar(to, newName)
		}
		kept = append(kept, item)
	}
	seq.Content = append(kept, &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		scalarNode("from"), scalarNode(old),
		scalarNode("to"), scalarNode(newName),
	}})
}

// pruneKBAliases removes the items that point at name: an alias records where
// a KB went, not who owns a name, so once that KB is unmounted a different KB
// later mounted under the name must not be reached by an old URL (D772).
func pruneKBAliases(root *yaml.Node, name string) {
	seq := kbAliasSeq(root)
	if seq == nil {
		return
	}
	kept := seq.Content[:0]
	for _, item := range seq.Content {
		if _, to := aliasItem(item); to != nil && to.Value == name {
			continue
		}
		kept = append(kept, item)
	}
	seq.Content = kept
	if len(kept) == 0 {
		dropKBAliasesKey(root)
	}
}

// aliasesPointingAt lists the items of cfg whose target is name, as
// "from → name" for a preflight.
func aliasesPointingAt(cfg *config.Config, name string) []string {
	var out []string
	for _, a := range cfg.KBAliases {
		if a.To == name {
			out = append(out, a.From+" → "+a.To)
		}
	}
	return out
}
