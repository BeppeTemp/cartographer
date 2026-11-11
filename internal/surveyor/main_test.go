package surveyor

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/gitx/gitxtest"
)

// execHelperFlag makes the test binary act as an operator's exec recipe (no
// interpreter needed, so it runs on every OS): one added entry, then the cursor.
const execHelperFlag = "--exec-helper"

// TestMain: see gitxtest.QuietGit (allow_test.go builds a KB).
func TestMain(m *testing.M) {
	if len(os.Args) > 2 && os.Args[1] == execHelperFlag && os.Args[2] == "system" {
		systemHelper()
	}
	if len(os.Args) > 1 && os.Args[1] == execHelperFlag {
		var rq struct{ Verb string }
		b, _ := io.ReadAll(os.Stdin)
		_ = json.Unmarshal(b, &rq)
		if len(os.Args) > 2 && strings.HasPrefix(os.Args[2], "v2") {
			v2Helper(os.Args[2], rq.Verb)
		}
		if len(os.Args) > 2 && strings.HasPrefix(os.Args[2], "fetch") {
			fetchHelper(os.Args[2], os.Args[3], b)
		}
		if rq.Verb != "list" { // these listers implement list only
			os.Exit(64)
		}
		if len(os.Args) > 2 && os.Args[2] == "content" {
			fmt.Println(`{"entry":{"key":"a.md","title":"A","revision":"r1","change":"added","content":"Inline page text."}}`)
			fmt.Println(`{"next_cursor":"c1"}`)
			os.Exit(0)
		}
		if len(os.Args) > 2 && os.Args[2] == "fail" {
			fmt.Fprintln(os.Stderr, "lister broke")
			os.Exit(3)
		}
		fmt.Println(`{"entry":{"key":"a.md","title":"A","revision":"r1","change":"added","locator":"https://wiki.example.com/a"}}`)
		fmt.Println(`{"next_cursor":"c1"}`)
		os.Exit(0)
	}
	gitxtest.QuietGit()
	os.Exit(m.Run())
}

// systemHelper is a system lister driven by files in $SYS_FIXTURE_DIR: the
// request's verb is appended to verbs, check.json is the check answer and
// listing.jsonl the listing. A missing file is a failure (exit 3); spec is not
// implemented (exit 64).
func systemHelper() {
	dir := os.Getenv("SYS_FIXTURE_DIR")
	b, _ := io.ReadAll(os.Stdin)
	var req struct{ Verb string }
	_ = json.Unmarshal(b, &req)
	if f, err := os.OpenFile(filepath.Join(dir, "verbs"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
		fmt.Fprintln(f, req.Verb)
		f.Close()
	}
	name := map[string]string{"check": "check.json", "list": "listing.jsonl"}[req.Verb]
	if name == "" {
		os.Exit(64)
	}
	out, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		fmt.Fprintln(os.Stderr, "no fixture for", req.Verb)
		os.Exit(3)
	}
	os.Stdout.Write(out)
	os.Exit(0)
}

// v2Helper is a feed lister that implements spec, check and list: v2-nocheck
// does not declare check (and fails if it is called), v2-badcheck answers
// ok: false.
func v2Helper(mode, verb string) {
	declared := `["spec","check","list"]`
	if mode == "v2-nocheck" {
		declared = `["spec","list"]`
	}
	switch verb {
	case "spec":
		fmt.Println(`{"spec":{"title":"Wiki","description":"d","shapes":["feed"],"verbs":` + declared + `,"settings":{"space":{"type":"string","required":true}},"env":[]}}`)
	case "check":
		switch mode {
		case "v2-nocheck":
			fmt.Fprintln(os.Stderr, "check must not run")
			os.Exit(3)
		case "v2-badcheck":
			fmt.Println(`{"check":{"ok":false,"detail":"no route to the wiki"}}`)
		default:
			fmt.Println(`{"check":{"ok":true,"identity":"svc-reader","detail":"12 spaces"}}`)
		}
	case "list":
		content := `,"content":"x"`
		if mode == "v2-nofetch" { // lists without content, and does not declare fetch
			content = ""
		}
		fmt.Println(`{"entry":{"key":"a.md","title":"A","revision":"r1","change":"added"` + content + `}}`)
		fmt.Println(`{"next_cursor":"c1"}`)
	case "fetch":
		fmt.Fprintln(os.Stderr, "fetch must not run")
		os.Exit(3)
	}
	os.Exit(0)
}

// fetchHelper is a feed recipe that lists a.md and b.md with no content and
// fetches it: mode "fetch" answers, "fetchfail" exits 3, "fetchbad" answers a key
// it was not asked. args[3] is the file that logs, one line per call, the keys
// asked.
func fetchHelper(mode, log string, stdin []byte) {
	var rq struct {
		Verb string
		Keys []string
	}
	_ = json.Unmarshal(stdin, &rq)
	switch rq.Verb {
	case "list":
		if mode == "fetchmany" { // 30 entries, none with content
			for i := 0; i < 30; i++ {
				fmt.Printf(`{"entry":{"key":"k%02d.md","title":"K","revision":"r1","change":"added"}}`+"\n", i)
			}
			fmt.Println(`{"next_cursor":"c1"}`)
			break
		}
		fmt.Println(`{"entry":{"key":"a.md","title":"A","revision":"r1","change":"added"}}`)
		fmt.Println(`{"entry":{"key":"b.md","title":"B","revision":"r1","change":"modified","content":"Inline b."}}`)
		fmt.Println(`{"delete":{"key":"gone.md"}}`)
		fmt.Println(`{"next_cursor":"c1"}`)
	case "fetch":
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		fmt.Fprintln(f, strings.Join(rq.Keys, ","))
		f.Close()
		switch mode {
		case "fetchfail":
			fmt.Fprintln(os.Stderr, "backend down")
			os.Exit(3)
		case "fetchbad":
			fmt.Println(`{"content":{"key":"zzz","content":"x"}}`)
			fmt.Println(`{"done":true}`)
		default:
			for _, k := range rq.Keys {
				fmt.Printf(`{"content":{"key":%q,"content":"Fetched %s."}}`+"\n", k, k)
			}
			fmt.Println(`{"done":true}`)
		}
	default:
		os.Exit(64)
	}
	os.Exit(0)
}
