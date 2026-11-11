// Command gen writes docs/openapi.json.
//
// The file is the OpenAPI document generated from the API route table. `make openapi` runs it;
// TestOpenAPI_CommittedCopyIsCurrent fails when the file is stale.
package main

import (
	"fmt"
	"os"

	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: openapi-gen <output file>")
		os.Exit(2)
	}
	doc, err := mcpserver.OpenAPIDocument()
	if err == nil {
		err = os.WriteFile(os.Args[1], append(doc, '\n'), 0o644)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "openapi gen: %v\n", err)
		os.Exit(1)
	}
}
