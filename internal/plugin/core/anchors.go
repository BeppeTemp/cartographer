package core

import (
	"fmt"
	"path"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/safewrite"
)

// anchors expands the placeholder anchors of a file path ({home},
// {xdg_config}, {app_data}, {local_app_data}, {documents}) against a machine.
// It is the one authority: the providers resolver and the plugin file
// capabilities both go through it, so the two cannot disagree on where an
// anchor lies.
type anchors struct {
	env  ProviderEnv
	home string // env.Home, cleaned, in slash form
}

func newAnchors(env ProviderEnv) *anchors {
	a := &anchors{env: env}
	a.home = a.slash(env.Home)
	return a
}

func (a *anchors) slash(s string) string {
	if a.env.GOOS == "windows" {
		s = strings.ReplaceAll(s, `\`, "/")
	}
	return strings.TrimSuffix(path.Clean(s), "/")
}

func (a *anchors) eq(x, y string) bool {
	return x == y || a.env.GOOS != "linux" && strings.EqualFold(x, y)
}

// under returns the segments of base below home, false if it lies elsewhere.
func (a *anchors) under(base string) ([]string, bool) {
	home := a.home
	if a.eq(base, home) {
		return nil, true
	}
	if len(base) > len(home) && base[len(home)] == '/' && a.eq(base[:len(home)], home) {
		return strings.Split(base[len(home)+1:], "/"), true
	}
	return nil, false
}

func (a *anchors) docs() string {
	if a.env.Documents != nil {
		return a.slash(a.env.Documents(a.env.Home))
	}
	return a.slash(safewrite.DocumentsDir(a.env.Home))
}

// anchor returns the root ("" for home) and the segments below home it adds.
func (a *anchors) anchor(name string) (root string, pre []string, err error) {
	env := a.env
	if env.Home == "" {
		return "", nil, fmt.Errorf("the home directory is unknown")
	}
	base := ""
	if env.Getenv != nil {
		base = env.Getenv(anchorEnv[name])
	}
	switch {
	case name == "home":
		return "", nil, nil
	case name == "documents":
		d := a.docs()
		if d == "" || d == "." {
			return "", nil, fmt.Errorf("the Documents folder is unknown")
		}
		segs, _ := a.under(d)
		return d, segs, nil
	case name == "xdg_config" && base == "":
		return "", []string{".config"}, nil
	case name != "xdg_config" && env.GOOS != "windows":
		return "", nil, fmt.Errorf("{%s} exists on windows only", name)
	case base == "":
		return "", nil, fmt.Errorf("{%s} is not set", name)
	}
	segs, ok := a.under(a.slash(base))
	if !ok {
		return "", nil, fmt.Errorf("{%s} resolves outside the home directory", name)
	}
	return "", segs, nil
}

// ExpandAnchor returns the directory an anchor name ("home", "xdg_config",
// "app_data", "local_app_data", "documents") stands for, cleaned and in slash
// form. It lies inside the home directory or the call is an error; the part of
// the path below it is the caller's to validate.
func ExpandAnchor(env ProviderEnv, name string) (string, error) {
	a := newAnchors(env)
	root, pre, err := a.anchor(name)
	if err != nil {
		return "", err
	}
	if root != "" {
		if _, ok := a.under(root); !ok {
			return "", fmt.Errorf("{%s} resolves outside the home directory", name)
		}
		return root, nil
	}
	return strings.Join(append([]string{a.home}, pre...), "/"), nil
}
