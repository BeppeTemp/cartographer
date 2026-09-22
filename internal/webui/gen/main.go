// Command webui-provenance writes internal/webui/dist/provenance.json with the
// hash of the web/ source tree. `make web` runs it right after the Vite build,
// so the committed bundle always carries the fingerprint of what produced it
// and TestBundleMatchesSource can tell a stale bundle from a current one
// without a Node toolchain.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/webui"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: webui-provenance <web-source-dir> <bundle-dir>")
		os.Exit(2)
	}
	source, bundle := os.Args[1], os.Args[2]

	hash, files, err := webui.SourceHash(source)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webui-provenance: %v\n", err)
		os.Exit(1)
	}
	payload, err := json.MarshalIndent(webui.Provenance{SourceHash: hash, Files: files}, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "webui-provenance: %v\n", err)
		os.Exit(1)
	}
	out := filepath.Join(bundle, webui.ProvenanceFile)
	if err := os.WriteFile(out, append(payload, '\n'), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "webui-provenance: writing %s: %v\n", out, err)
		os.Exit(1)
	}
	fmt.Printf("webui: %s (%d source files) -> %s\n", hash[:12], files, out)
}
