package settingschema_test

import (
	"testing"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
	"gopkg.in/yaml.v3"
)

// Every real schema renders to a fixed point whatever key ends a mapping: each
// key with an Example is set alone and then all together, in the table's order
// and in the reverse one, so a block list or a mapping is the last key of its
// mapping in turn (auth.tokens and auth.roles of the server, for one). Format of
// a formatted file changes nothing, and realigning it reports nothing: a file
// that differed on every run would be rewritten, with its backup, on every sync.
func TestRealSchemasRenderToAFixedPoint(t *testing.T) {
	kbs := config.KBSettingsSchema()
	for name, s := range map[string]settingschema.Schema{"server": config.Schema, "kb": kbs, "client": clientconfig.Schema} {
		t.Run(name, func(t *testing.T) {
			var all, rev []settingschema.Key
			files := map[string][]byte{}
			for _, k := range s.Keys {
				if k.Example == "" {
					continue
				}
				files[k.Path] = exampleFile(t, k)
				all = append(all, k)
				rev = append([]settingschema.Key{k}, rev...)
			}
			files["all"], files["reversed"] = exampleFile(t, all...), exampleFile(t, rev...)
			files["empty"] = nil
			for what, in := range files {
				once, err := s.Format(in)
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				twice, err := s.Format(once)
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				if string(twice) != string(once) {
					t.Errorf("%s: Format is not idempotent:\n%s\n--- second pass\n%s", what, once, twice)
					continue
				}
				out, rep, err := s.Realign(once, settingschema.Registry{})
				if err != nil || !rep.Empty() || string(out) != string(once) {
					t.Errorf("%s: Realign of a formatted file changes it: err=%v report=%v\n%s\n--- realigned\n%s", what, err, rep.Lines(), once, out)
				}
			}
		})
	}
}

// exampleFile is a settings file that sets the keys at their Example, in order.
func exampleFile(t *testing.T, keys ...settingschema.Key) []byte {
	t.Helper()
	if len(keys) == 0 {
		return nil
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode}
	for _, k := range keys {
		var val yaml.Node
		if err := yaml.Unmarshal([]byte(k.Example), &val); err != nil || len(val.Content) == 0 {
			t.Fatalf("example of %s is not YAML: %v", k.Path, err)
		}
		settingschema.Set(doc, k.Path, val.Content[0])
	}
	b, err := settingschema.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
