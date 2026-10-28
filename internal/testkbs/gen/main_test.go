package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args    []string
		dir     string
		code    int
		onOut   bool
		onError bool
	}{
		{[]string{"-h"}, "", 0, true, false},
		{[]string{"--help"}, "", 0, true, false},
		{[]string{"/tmp/data"}, "/tmp/data", 0, false, false},
		{[]string{"--bogus"}, "", 2, false, true},
		{[]string{"-x"}, "", 2, false, true},
		{[]string{"-h", "x"}, "", 2, false, true},
		{nil, "", 2, false, true},
		{[]string{"a", "b"}, "", 2, false, true},
	}
	for _, c := range cases {
		var out, errb bytes.Buffer
		dir, code := parseArgs(c.args, &out, &errb)
		if dir != c.dir || code != c.code {
			t.Errorf("%v: got (%q, %d), want (%q, %d)", c.args, dir, code, c.dir, c.code)
		}
		if (out.Len() > 0) != c.onOut || (errb.Len() > 0) != c.onError {
			t.Errorf("%v: stdout %q stderr %q", c.args, out.String(), errb.String())
		}
		if c.onOut && !strings.Contains(out.String(), "usage: gen") {
			t.Errorf("%v: no usage text", c.args)
		}
	}
}
