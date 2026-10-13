package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/config"
)

// newKBSettingsFile is the settings.yaml a new KB starts with (D746): every
// key it may carry, documented and commented out, rendered from the kb scope of
// the config schema. Nothing is set, so the KB behaves as it would without the
// file until someone uncomments a line. nil on a render failure: the KB is
// created without the file rather than not at all.
func newKBSettingsFile() []byte {
	body, err := config.KBSettingsSchema().Format(nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: settings.yaml not written: %v\n", err)
		return nil
	}
	header := "# Policy of this KB, whoever serves it (settings.yaml). It is read from the KB\n" +
		"# repository on every mount, so a setting here wins over the same one in a\n" +
		"# server's kbs[] entry. Machine-bound keys (ssh_key, author_*, sops_age_key_file,\n" +
		"# tokens) stay in the server config. Change it through git review.\n\n"
	// An empty mapping renders as "{}", which would make a key typed below it
	// invalid YAML: the file is comments only until someone adds a key.
	body = bytes.TrimPrefix(body, []byte("{}\n\n"))
	return append([]byte(header), body...)
}
