// Command gen writes the test KB fleet of package testkbs into a data
// directory, one subdirectory per KB: `go run ./internal/testkbs/gen <dir>`,
// and beside them the client settings report of an imaginary workstation
// (client-report.json, not a KB). `make dev` runs it when no DATA is given
// (D818).
package main

import (
	"fmt"
	"os"
	"time"

	"github.com/BeppeTemp/cartographer/internal/testkbs"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gen <data dir>")
		os.Exit(2)
	}
	if err := testkbs.GenerateFleet(os.Args[1], time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := testkbs.WriteClientReport(os.Args[1]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
