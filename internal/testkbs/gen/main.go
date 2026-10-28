// Command gen writes the test KB fleet of package testkbs into a data
// directory, one subdirectory per KB: `go run ./internal/testkbs/gen <dir>`,
// and beside them the client settings report of an imaginary workstation
// (client-report.json, not a KB). `make dev` runs it when no DATA is given
// (D818).
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/testkbs"
)

const usage = "usage: gen <data dir>\n\nWrites the test KB fleet and a client report into <data dir>.\n"

func main() {
	dir, code := parseArgs(os.Args[1:], os.Stdout, os.Stderr)
	if dir == "" {
		os.Exit(code)
	}
	if err := testkbs.GenerateFleet(dir, time.Now()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := testkbs.WriteClientReport(dir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// parseArgs returns the data directory, or "" and the exit code: -h and --help
// print the usage and succeed, any other argument starting with "-" is a usage
// error (it used to be taken as the directory and created as `--help/`, #1315),
// and only a plain path is the data directory.
func parseArgs(args []string, stdout, stderr io.Writer) (dir string, code int) {
	if len(args) == 1 && (args[0] == "-h" || args[0] == "--help") {
		fmt.Fprint(stdout, usage)
		return "", 0
	}
	if len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(stderr, usage)
		return "", 2
	}
	return args[0], 0
}
