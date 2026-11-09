package connector

import (
	"fmt"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
)

// ChildEnv builds the environment of a child: exactly the variables listed, plus
// PATH and HOME (on Windows also SystemRoot, TEMP, TMP and USERPROFILE, without
// which most programs do not start). listed maps the name the child sees to the
// name of the worker's own variable holding the value, so the model-provider key
// is exposed to a child only if the operator lists it. A listed variable that is
// unset or empty is an error naming the variable, never a value.
func ChildEnv(listed map[string]string) ([]string, error) {
	var out []string
	names := make([]string, 0, len(listed))
	for n := range listed {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		src := listed[n]
		v, err := EnvValue(src)
		if err != nil {
			return nil, err
		}
		out = append(out, n+"="+v)
	}
	base := []string{"PATH", "HOME"}
	if runtime.GOOS == "windows" {
		base = append(base, "SystemRoot", "TEMP", "TMP", "USERPROFILE")
	}
	for _, b := range base {
		if _, listedToo := listed[b]; listedToo {
			continue
		}
		if v, ok := os.LookupEnv(b); ok {
			out = append(out, b+"="+v)
		}
	}
	return out, nil
}

// EnvValue reads the variable a config key names; unset or empty is an error that
// names the variable and never a value.
func EnvValue(name string) (string, error) {
	v := os.Getenv(name)
	if v == "" {
		return "", fmt.Errorf("environment variable %s is unset or empty", name)
	}
	return v, nil
}

// RedactEnv replaces the value of each listed variable found in s with ***, for a
// text that may reach a feed_error or a log line. Short values (under 4 bytes)
// are left alone: replacing them would mangle unrelated text.
func RedactEnv(s string, listed map[string]string) string {
	var vals []string
	for _, src := range listed {
		if v := os.Getenv(src); len(v) >= 4 {
			vals = append(vals, v)
		}
	}
	slices.SortFunc(vals, func(a, b string) int { return len(b) - len(a) })
	for _, v := range vals {
		s = strings.ReplaceAll(s, v, "***")
	}
	return s
}
