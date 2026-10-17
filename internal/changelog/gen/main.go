// Command gen groups a release-please changelog by area and checks PR bodies
// for wrapped conventional lines (D865):
//
//	gen -file CHANGELOG.md   rewrite the file in place (make changelog-areas)
//	gen -stdin               read a changelog or release PR body, write the grouped text
//	gen -check-body          read a PR body, report each wrapped conventional line, exit 1
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/BeppeTemp/cartographer/internal/changelog"
)

func main() {
	file := flag.String("file", "", "changelog file to regroup in place")
	stdin := flag.Bool("stdin", false, "regroup stdin to stdout")
	check := flag.Bool("check-body", false, "check a PR body on stdin for wrapped conventional lines")
	flag.Parse()
	if *stdin == *check && *file == "" || *file != "" && (*stdin || *check) {
		fmt.Fprintln(os.Stderr, "usage: gen -file <changelog> | -stdin | -check-body")
		os.Exit(2)
	}
	var err error
	if *file != "" {
		err = regroupFile(*file)
	} else if in, rerr := io.ReadAll(os.Stdin); rerr != nil {
		err = rerr
	} else if *stdin {
		fmt.Print(changelog.Regroup(string(in)))
	} else {
		err = checkBody(string(in))
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "changelog gen: %v\n", err)
		os.Exit(1)
	}
}

func checkBody(body string) error {
	bad := changelog.WrappedLines(body)
	for _, n := range bad {
		fmt.Printf("::error::PR body line %d: %s\n", n, changelog.WrapMessage)
	}
	if len(bad) > 0 {
		return fmt.Errorf("%d wrapped conventional line(s) in the PR body", len(bad))
	}
	return nil
}

// regroupFile rewrites path only when the grouping changes it, so a run on an
// already grouped file leaves it untouched (and git clean).
func regroupFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	in, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out := changelog.Regroup(string(in))
	if out == string(in) {
		return nil
	}
	return os.WriteFile(path, []byte(out), info.Mode().Perm())
}
