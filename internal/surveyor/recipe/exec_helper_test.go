package recipe

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The test binary re-executed as the operator's script: no interpreter, so the
// tests run the same on Linux, macOS and Windows.
const execHelperFlag = "--exec-helper"

// platformHelpers are helper modes that need an OS facility (setsid).
var platformHelpers = map[string]func(args []string){}

func runExecHelper(mode string, args []string) {
	out := os.Stdout
	if f := platformHelpers[mode]; f != nil {
		f(args)
	}
	switch mode {
	case "ok":
		fmt.Fprintln(out, `{"entry":{"key":"a.md","title":"A","revision":"r1","change":"added","content":"hello","future_field":1}}`)
		fmt.Fprintln(out)
		fmt.Fprintln(out, `{"entry":{"key":"b.md","title":"B","revision":"r2","change":"modified","locator":"https://example.com/b"}}`)
		fmt.Fprintln(out, `{"delete":{"key":"c.md"}}`)
		fmt.Fprintln(out, `{"next_cursor":"cur-2"}`)
	case "stdin": // args[0] = file to copy stdin into
		b, _ := io.ReadAll(os.Stdin)
		_ = os.WriteFile(args[0], b, 0o600)
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
	case "env": // args[0] = file
		var names []string
		for _, e := range os.Environ() {
			n, _, _ := strings.Cut(e, "=")
			names = append(names, n)
		}
		sort.Strings(names)
		_ = os.WriteFile(args[0], []byte(strings.Join(names, "\n")), 0o600)
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
	case "argv": // args[0] = file, the rest is what the test sent
		_ = os.WriteFile(args[0], []byte(strings.Join(args[1:], "\x00")), 0o600)
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
	case "cwd": // args[0] = file
		wd, _ := os.Getwd()
		_ = os.WriteFile(args[0], []byte(wd), 0o600)
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
	case "exit3":
		fmt.Fprintln(os.Stderr, "boom with s3cr3t-token-value inside\x1b[31m and\nmore")
		os.Exit(3)
	case "nocursor":
		fmt.Fprintln(out, `{"entry":{"key":"a","title":"A","revision":"1","change":"added"}}`)
	case "cursornotlast":
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
		fmt.Fprintln(out, `{"entry":{"key":"a","title":"A","revision":"1","change":"added"}}`)
	case "many":
		for i := 0; i < 5; i++ {
			fmt.Fprintf(out, `{"entry":{"key":"k%d","title":"T","revision":"1","change":"added"}}`+"\n", i)
		}
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
	case "dup":
		for i := 0; i < 2; i++ {
			fmt.Fprintln(out, `{"entry":{"key":"same","title":"T","revision":"1","change":"added"}}`)
		}
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
	case "malformed":
		fmt.Fprintln(out, `{"entry":`)
	case "unknownline":
		fmt.Fprintln(out, `{"hello":1}`)
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
	case "badentry": // args[0] = the entry JSON
		fmt.Fprintln(out, `{"entry":`+args[0]+`}`)
		fmt.Fprintln(out, `{"next_cursor":"x"}`)
	case "flood": // never stops: the surveyor must kill it
		junk := strings.Repeat("x", 1<<10) // one endless line: the cap is on bytes, not lines
		for {
			if _, err := fmt.Fprint(out, junk); err != nil {
				os.Exit(0)
			}
		}
	case "hang": // args[0] = file for the grandchild's pid
		if len(args) > 0 {
			c := exec.Command(os.Args[0], execHelperFlag, "sleep")
			_ = c.Start()
			_ = os.WriteFile(args[0], []byte(strconv.Itoa(c.Process.Pid)), 0o600)
		}
		time.Sleep(time.Minute)
	case "partial64": // prints part of an answer, then exits 64
		fmt.Fprintln(out, `{"content":{"key":"a","content":"x"}}`)
		os.Exit(64)
	case "exit64": // implements no verb
		os.Exit(64)
	case "lines": // every argument is printed as one line, verbatim: the test writes the whole answer
		for _, l := range args {
			fmt.Fprintln(out, l)
		}
	case "byverb": // args[0] = log of the verbs received; args[1], args[2] = answer to spec and to check ("-": exit 64; "exitN": stderr and exit N)
		b, _ := io.ReadAll(os.Stdin)
		var req struct{ Verb string }
		_ = json.Unmarshal(b, &req)
		f, _ := os.OpenFile(args[0], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		fmt.Fprintln(f, req.Verb)
		f.Close()
		answer := map[string]string{"spec": args[1], "check": args[2], "list": `{"next_cursor":"x"}`}[req.Verb]
		switch {
		case answer == "" || answer == "-":
			os.Exit(64)
		case strings.HasPrefix(answer, "exit"):
			n, _ := strconv.Atoi(strings.TrimPrefix(answer, "exit"))
			fmt.Fprintln(os.Stderr, "no kubeconfig")
			os.Exit(n)
		}
		fmt.Fprintln(out, answer)
	case "sys": // args[0] = log of the verbs received, args[1] = the check answer, args[2] = "exit4" (check exits 4) or "cursor" (the listing ends with a cursor)
		b, _ := io.ReadAll(os.Stdin)
		var m struct{ Verb string }
		_ = json.Unmarshal(b, &m)
		f, _ := os.OpenFile(args[0], os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		fmt.Fprintln(f, m.Verb)
		f.Close()
		if m.Verb == "check" {
			fmt.Fprintln(out, args[1])
			if len(args) > 2 && args[2] == "exit4" {
				fmt.Fprintln(os.Stderr, "no kubeconfig")
				os.Exit(4)
			}
			break
		}
		fmt.Fprintln(out, `{"entry":{"key":"namespace/a","content":"line1\nline2","revision":"ignored","change":"modified"}}`)
		fmt.Fprintln(out, `{"entry":{"key":"namespace/b","title":"B","content":"x"}}`)
		if len(args) > 2 && args[2] == "cursor" { // a lister that stopped early
			fmt.Fprintln(out, `{"next_cursor":"resume-here"}`)
		} else {
			fmt.Fprintln(out, `{"next_cursor":""}`)
		}
	case "nullcursor":
		fmt.Fprintln(out, `{"next_cursor":null}`)
	case "sleep":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}
