package sops

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// The server's age key is the only identity Cartographer gives sops (D260), so
// it only decrypts, edits or creates files whose data key is wrapped for age.
// A file (or a creation rule) that also names a KMS, PGP or Vault master key
// would be opened with whatever identity the host itself carries, which is not
// something the KB's author is entitled to borrow. Both checks below are
// allow-lists: a key type sops learns tomorrow is refused until it is added here.

// ErrNotAgeOnly is the kind of the refusal of a file that names a master key
// other than age.
var ErrNotAgeOnly = fmt.Errorf("names a master key that is not an age key")

// metadataKeys are the keys a `sops:` block may carry besides its master keys.
var metadataKeys = map[string]bool{
	"age": true, "key_groups": true, "lastmodified": true, "mac": true, "version": true,
	"shamir_threshold": true, "mac_only_encrypted": true,
	"unencrypted_suffix": true, "encrypted_suffix": true,
	"unencrypted_regex": true, "encrypted_regex": true,
	"unencrypted_comment_regex": true, "encrypted_comment_regex": true,
}

// ruleKeys are the keys of one creation rule that Cartographer accepts.
var ruleKeys = map[string]bool{
	"path_regex": true, "age": true, "key_groups": true, "shamir_threshold": true,
	"mac_only_encrypted": true,
	"unencrypted_suffix": true, "encrypted_suffix": true,
	"unencrypted_regex": true, "encrypted_regex": true,
	"unencrypted_comment_regex": true, "encrypted_comment_regex": true,
}

// groupKeys are the keys of one key group; `merge` holds further groups.
var groupKeys = map[string]bool{"age": true, "merge": true}

// requireAgeOnlyFile refuses an encrypted document whose sops block lists a
// master key other than age. The block is in clear, so nothing is decrypted.
func requireAgeOnlyFile(root *yaml.Node, relativePath string) error {
	var blocks []*yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if k := root.Content[i]; k.Kind == yaml.ScalarNode && k.Value == "sops" {
			blocks = append(blocks, root.Content[i+1])
		}
	}
	if len(blocks) != 1 {
		return newError(nil, nil, "%s must carry exactly one sops block", relativePath)
	}
	if err := checkMapping(blocks[0], metadataKeys); err != nil {
		return newError(ErrNotAgeOnly, err, "%s %v; Cartographer decrypts only files encrypted for the server's age key", relativePath, ErrNotAgeOnly)
	}
	return nil
}

// checkMapping accepts a mapping whose non-empty keys are all in allowed;
// key_groups and merge are visited as lists of groups.
func checkMapping(n *yaml.Node, allowed map[string]bool) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("not a mapping")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			return fmt.Errorf("non-scalar key")
		}
		if emptyNode(v) {
			continue
		}
		if !allowed[k.Value] {
			return fmt.Errorf("key %q", k.Value)
		}
		if k.Value == "key_groups" || k.Value == "merge" {
			if v.Kind != yaml.SequenceNode {
				return fmt.Errorf("%s is not a list", k.Value)
			}
			for _, g := range v.Content {
				if err := checkMapping(g, groupKeys); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// emptyNode reports whether a master-key entry names nothing (sops writes
// `kms: null` and `pgp: []` in the metadata of an age-only file).
func emptyNode(v *yaml.Node) bool {
	switch v.Kind {
	case yaml.ScalarNode:
		return v.Tag == "!!null" || (v.Tag == "!!str" && strings.TrimSpace(v.Value) == "")
	case yaml.SequenceNode:
		return len(v.Content) == 0
	}
	return false
}
