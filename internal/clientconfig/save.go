package clientconfig

import (
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// droppedKeys are keys older versions wrote that Save removes: the server-owned
// caches now live in the state file (D747), the legacy `kbs` alias was replaced
// by them (D169), and doctor_timer_opt_out is gone (D704).
var droppedKeys = map[string]bool{
	"kbs": true, "known_kbs": true, "server_mount_mode": true, "server_routed_path": true,
	"doctor_timer_opt_out": true,
	// Removed by D906 (the realignment registry, Retired, names what replaced
	// each): they must not survive a save as unknown keys. agents and
	// sync_timer_opt_out are written to the state file by the same Save.
	"token_env": true, "trust": true, "write_findings_hook": true, "search_depth": true,
	"agents": true, "sync_timer_opt_out": true, "update": true,
}

// yamlKeys lists the top-level YAML keys of yamlConfig, in field order.
func yamlKeys() []string {
	t := reflect.TypeOf(yamlConfig{})
	var keys []string
	for i := 0; i < t.NumField(); i++ {
		if tag := strings.Split(t.Field(i).Tag.Get("yaml"), ",")[0]; tag != "" && tag != "-" {
			keys = append(keys, tag)
		}
	}
	return keys
}

// keepWhenDefault lists the keys Save writes only when they differ from the
// default. An explicit line of the default value that a person wrote is kept.
var keepWhenDefault = map[string]bool{"usage_scan": true}

// buildDocument returns the YAML document that Save writes: the existing file's
// document with the typed keys of y set in place, so every comment, every
// unknown key and the order of the file survive. This is a node round-trip, not
// a marshal of a map, because a map has no room for comments: marshalling one
// dropped every comment on every save (D747).
func buildDocument(existing []byte, y *yamlConfig, extra map[string]interface{}) (*yaml.Node, error) {
	var doc yaml.Node
	if len(existing) > 0 {
		if err := yaml.Unmarshal(existing, &doc); err != nil {
			// A file that does not parse is replaced, as Save always has.
			doc = yaml.Node{}
		}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	root := doc.Content[0]

	var typed yaml.Node
	if err := typed.Encode(y); err != nil {
		return nil, fmt.Errorf("clientconfig: encode fields: %w", err)
	}
	want := map[string]*yaml.Node{}
	for i := 0; i+1 < len(typed.Content); i += 2 {
		want[typed.Content[i].Value] = typed.Content[i+1]
	}

	for _, key := range yamlKeys() {
		nv, emit := want[key]
		idx := pairIndex(root, key)
		switch {
		case emit && idx >= 0:
			mergeNode(root.Content[idx+1], nv)
		case emit:
			root.Content = append(root.Content, scalarKey(key), nv)
		case idx >= 0:
			if keepWhenDefault[key] && root.Content[idx+1].Kind == yaml.ScalarNode && root.Content[idx+1].Value == "true" {
				continue
			}
			removePair(&doc, root, idx)
		}
	}
	for key := range droppedKeys {
		if idx := pairIndex(root, key); idx >= 0 {
			removePair(&doc, root, idx)
		}
	}
	for _, key := range sortedKeys(extra) {
		if pairIndex(root, key) >= 0 {
			continue
		}
		var v yaml.Node
		if err := v.Encode(extra[key]); err != nil {
			return nil, fmt.Errorf("clientconfig: encode %s: %w", key, err)
		}
		root.Content = append(root.Content, scalarKey(key), &v)
	}
	return &doc, nil
}

func scalarKey(k string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k}
}

func pairIndex(m *yaml.Node, key string) int {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return i
		}
	}
	return -1
}

// removePair deletes the key at idx. A comment a person wrote above it is not
// lost with it: it moves to the next key, or to the end of the file. The
// comments the schema generated are dropped with their key.
func removePair(doc, m *yaml.Node, idx int) {
	k := m.Content[idx]
	carry := k.HeadComment
	if sk, ok := Schema.Lookup(k.Value); ok && carry == strings.Join(settingschema.Comment(sk), "\n") {
		carry = ""
	}
	m.Content = append(m.Content[:idx], m.Content[idx+2:]...)
	if carry == "" {
		return
	}
	if idx < len(m.Content) {
		next := m.Content[idx]
		next.HeadComment = strings.TrimRight(carry+"\n"+next.HeadComment, "\n")
		return
	}
	doc.FootComment = strings.TrimRight(doc.FootComment+"\n"+carry, "\n")
}

// mergeNode makes old hold the value of nv. Mappings merge key by key so the
// comments inside them survive; anything else is replaced only when the value
// really differs, so a value keeps the style it was written in.
func mergeNode(old, nv *yaml.Node) {
	if old.Kind == yaml.MappingNode && nv.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(nv.Content); i += 2 {
			if j := pairIndex(old, nv.Content[i].Value); j >= 0 {
				mergeNode(old.Content[j+1], nv.Content[i+1])
			} else {
				old.Content = append(old.Content, nv.Content[i], nv.Content[i+1])
			}
		}
		for i := 0; i+1 < len(old.Content); {
			if pairIndex(nv, old.Content[i].Value) < 0 {
				old.Content = append(old.Content[:i], old.Content[i+2:]...)
				continue
			}
			i += 2
		}
		return
	}
	var a, b interface{}
	if old.Decode(&a) == nil && nv.Decode(&b) == nil && reflect.DeepEqual(a, b) {
		return
	}
	head, line, foot := old.HeadComment, old.LineComment, old.FootComment
	*old = *nv
	old.HeadComment, old.LineComment, old.FootComment = head, line, foot
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Small and rare: insertion order would be random, so sort for stable files.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// writeYAML renders the document with the schema comments and writes it, unless
// the file already holds exactly those bytes: a save that changes no setting
// leaves .cartographer.yaml untouched, mtime included.
//
// private is set when the file holds a literal token: it is then written with
// mode 0600, and kept so on a rewrite (D906).
func writeYAML(dir string, doc *yaml.Node, private bool) error {
	Schema.Render(doc)
	data, err := settingschema.Encode(doc)
	if err != nil {
		return fmt.Errorf("clientconfig: marshal: %w", err)
	}
	if old, err := os.ReadFile(Path(dir)); err == nil && string(old) == string(data) {
		// Nothing to rewrite, but a file with a literal token must be private
		// whatever mode it was created with.
		if private {
			return tightenFile(Path(dir))
		}
		return nil
	}
	mode := os.FileMode(0o644)
	if private {
		mode = 0o600
	}
	return writeAtomicMode(dir, FileName, data, mode)
}

// fileIsPrivate reports whether path is closed to group and others. Always true
// on Windows, where the mode bits mean nothing, and for a missing file.
func fileIsPrivate(path string) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	fi, err := os.Stat(path)
	return err != nil || fi.Mode().Perm()&0o077 == 0
}

// tightenFile takes group and other access off a regular file (a symlink is
// left alone: chmod would follow it to somewhere that is not ours).
func tightenFile(path string) error {
	if fileIsPrivate(path) {
		return nil
	}
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return nil
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("clientconfig: chmod %s: %w", path, err)
	}
	return nil
}
