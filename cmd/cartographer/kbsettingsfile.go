package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/config"
	"gopkg.in/yaml.v3"
)

// newKBSettingsFile is the settings.yaml a new KB starts with (D746): every
// key it may carry, documented and commented out, rendered from the kb scope of
// the config schema. Nothing is set, so the KB behaves as it would without the
// file until someone uncomments a line (or, with kb create --branch, until it
// names the branch). nil on a render failure: the KB is
// created without the file rather than not at all.
func newKBSettingsFile(branch string) []byte {
	var set []byte
	if branch != "" {
		// kb create --branch (D1093): the team's branch, committed with the
		// scaffold on the remote's default branch, where it is read from.
		// Marshalled, not concatenated: a valid git name such as `true`, `123`,
		// `&a` or `!t` would otherwise parse as another YAML value.
		set, _ = yaml.Marshal(map[string]string{"branch": branch})
	}
	body, err := config.KBSettingsSchema().Format(set)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: settings.yaml not written: %v\n", err)
		return nil
	}
	header := "# Policy of this KB, whoever serves it (settings.yaml). It is read from the KB\n" +
		"# repository on every mount, so a setting here wins over the same one in a\n" +
		"# server's kbs[] entry. Machine-bound keys (author_*) stay in the server\n" +
		"# config, and every secret is a file of the server's credentials_dir, never of\n" +
		"# the KB. Change it through git review.\n\n"
	// An empty mapping renders as "{}", which would make a key typed below it
	// invalid YAML: the file is comments only until someone adds a key.
	body = bytes.TrimPrefix(body, []byte("{}\n\n"))
	return append([]byte(header), body...)
}
