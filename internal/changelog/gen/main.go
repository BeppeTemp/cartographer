// Command gen runs package changelog: it groups a release-please changelog by
// area and checks PR bodies for wrapped conventional lines (D865):
//
//	gen -file CHANGELOG.md   rewrite the file in place (make changelog-areas)
//	gen -stdin               read a changelog or release PR body, write the grouped text
//	gen -check-body          read a PR body, report each wrapped conventional line, exit 1
//	gen -file CHANGELOG.md -section 0.28.0   print that version's section (the release notes)
//	gen -file CHANGELOG.md -section 0.28.0 -split 60000 -out dir   write it as part-1.md, part-2.md, ... and body.md (part 1 framed as release-please writes a release PR body)
//	                                          none over 60000 characters (the release PR body and comments)
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/BeppeTemp/cartographer/internal/changelog"
)

func main() {
	file := flag.String("file", "", "changelog file to regroup in place")
	stdin := flag.Bool("stdin", false, "regroup stdin to stdout")
	check := flag.Bool("check-body", false, "check a PR body on stdin for wrapped conventional lines")
	section := flag.String("section", "", "with -file: print the section of this version instead of regrouping")
	split := flag.Int("split", 0, "with -section: cut the notes into parts of at most this many characters")
	out := flag.String("out", "", "with -split: directory that receives part-1.md, part-2.md, ...")
	flag.Parse()
	if (*split != 0) != (*out != "") || *split != 0 && *section == "" {
		fmt.Fprintln(os.Stderr, "usage: gen -file <changelog> -section <version> -split <limit> -out <dir>")
		os.Exit(2)
	}
	if *section != "" {
		if *file == "" || *stdin || *check {
			fmt.Fprintln(os.Stderr, "usage: gen -file <changelog> -section <version>")
			os.Exit(2)
		}
		in, err := os.ReadFile(*file)
		if err == nil {
			var text string
			if text, err = changelog.Section(string(in), *section); err == nil {
				if *split == 0 {
					fmt.Print(text)
					return
				}
				if err = writeParts(text, *split, *out); err == nil {
					return
				}
			}
		}
		fmt.Fprintf(os.Stderr, "changelog gen: %v\n", err)
		os.Exit(1)
	}
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

// writeParts writes the parts of text to dir (created if missing), and removes
// the part files of an earlier run.
func writeParts(text string, limit int, dir string) error {
	parts, err := changelog.Split(text, limit)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "part-*.md"))
	old = append(old, filepath.Join(dir, "body.md"))
	for _, f := range old {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	body := changelog.ReleasePRBody(parts[0])
	if err := os.WriteFile(filepath.Join(dir, "body.md"), []byte(body), 0o644); err != nil {
		return err
	}
	for i, p := range parts {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("part-%d.md", i+1)), []byte(p), 0o644); err != nil {
			return err
		}
	}
	return nil
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
