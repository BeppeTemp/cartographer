package main

import (
	"fmt"
	"io"
	"os"

	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/gitx"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/service"
)

// localServer reports whether the server cfg describes is a personal install:
// stdio, or HTTP with authentication off. With tokens the writer is the one the
// request names (D904, D950) and nothing here applies.
func localServer(cfg *config.Config) bool {
	if cfg.HTTP == "" {
		return true
	}
	on, err := resolveAuth(cfg.Auth)
	return err == nil && !on
}

// localIdentFn is the git identity of the user running a local server, for the
// KB at kbPath: the global git config when it is uniform, else what git would
// record for a commit in the KB itself (an includeIf that picks the identity by
// folder, #1910). Tests stub it so no run depends on the developer's git config.
var localIdentFn = func(kbPath string) (name, email string, ok bool) {
	ident, perFolder := globalIdentFn()
	if ident == "" && perFolder {
		ident = gitx.WorkTreeIdent(kbPath)
	}
	if ident == "" {
		return "", "", false
	}
	n, e, problem := gitx.ParseIdent(ident)
	return n, e, problem == ""
}

// ensureLocalGitIdentity is the personal-install step of `connect` and
// `status`: it finds the identity a local server will commit as and, when write
// is set and the server file has no git.identities.default, writes it there so
// it stays stable across restarts and is visible in the file. It says at once
// what is missing when no real identity exists. configPath "" is the service's.
func ensureLocalGitIdentity(w io.Writer, configPath string, write bool) {
	if configPath == "" {
		p, err := service.ConfigPath()
		if err != nil {
			return
		}
		configPath = p
	}
	cfg, err := loadReauthorConfig(configPath)
	if err != nil || cfg.HasIdentityEntry("") {
		return
	}
	// A token server on a loopback URL (an SSH tunnel) is a shared server: the
	// writer is the one the request names, never the identity of this user.
	if !localServer(cfg) {
		return
	}
	ident, perFolder := globalIdentFn()
	if perFolder {
		return // the identity is picked by folder: the server asks each KB's own
	}
	name, email, problem := gitx.ParseIdent(ident)
	if problem != "" {
		fmt.Fprintf(w, "warning: no git identity: writes to a KB with a remote will be refused: %s\n", kb.IdentityFixHint)
		return
	}
	if !write {
		return
	}
	if _, statErr := os.Stat(configPath); statErr != nil {
		return // no server file to write: the server derives it at start
	}
	wrote, err := config.SetDefaultGitIdentity(configPath, name+" <"+email+">")
	switch {
	case err != nil:
		fmt.Fprintf(w, "warning: git identity %s <%s> not written to %s: %v\n", name, email, configPath, err)
	case wrote:
		fmt.Fprintf(w, "git identity: %s <%s> written to git.identities.default in %s\n", name, email, configPath)
	}
}
