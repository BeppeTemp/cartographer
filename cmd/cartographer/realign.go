package main

import (
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/provisioning"
	"github.com/BeppeTemp/cartographer/internal/settingschema"
)

// dropRemovedFlags takes the flags of removed settings out of serve's arguments
// and warns about each (D903), so a service definition or a container command
// written for an older version still starts. Both spellings are dropped, and the
// value form (--git-sync=false); a bool flag never takes a separate value.
func dropRemovedFlags(args []string) []string {
	removed := config.RemovedFlags()
	kept := make([]string, 0, len(args))
	for _, a := range args {
		name := strings.TrimLeft(a, "-")
		name, _, _ = strings.Cut(name, "=")
		if advice, ok := removed[name]; ok && strings.HasPrefix(a, "-") {
			log.Printf("warning: --%s: %s", name, advice)
			continue
		}
		kept = append(kept, a)
	}
	return kept
}

// announceConfig logs what the layers carried that no longer exists, and brings
// the server YAML up to date (D903): the removed keys dropped, the schema's
// comments written, the original kept beside it. The same on the first start of
// a new version and on every later one: a file already in that form is not
// touched, no backup is written. A file that cannot be rewritten (a read-only
// mount) is left as it is, and works: the removed keys are ignored.
func announceConfig(cfg *config.Config, configPath string) {
	for _, w := range cfg.Warnings {
		log.Printf("warning: %s", w)
	}
	if configPath == "" {
		return
	}
	rep, backup, err := config.RealignServerFile(configPath, version)
	if err != nil {
		// A file only lacking comments is not worth a warning on every start
		// when it cannot be written (a read-only mount); one that holds removed
		// settings is, because they are being ignored, not dropped.
		if rep.Substantive() {
			log.Printf("warning: config %s holds removed settings and cannot be rewritten (%v): they are ignored", configPath, err)
		}
		return
	}
	logRealignment("config "+configPath, rep, "the original is kept as "+backup)
}

func logRealignment(what string, rep settingschema.Report, tail string) {
	if rep.Empty() {
		return
	}
	log.Printf("%s realigned for cartographer %s (%s):", what, version, tail)
	for _, l := range rep.Lines() {
		log.Printf("  - %s", l)
	}
}

// realignKBSettings brings a mounted KB's settings.yaml up to date (D903),
// as one commit of the server's, so it is revertible. It returns a non-empty
// message when the KB must not mount: its settings.yaml still asks for the
// server git profile, which was removed, and mounting it would write straight to
// the branch that profile protected. Any other failure is a warning, never fatal
// (D746: a bad KB file costs its entries, not the server).
func realignKBSettings(k *kb.KB, name string) (refusal string) {
	st, err := k.ReadSettings()
	if err != nil || !st.Present || st.Unparseable {
		return ""
	}
	for _, m := range st.Malformed {
		if m.Refuse {
			return fmt.Sprintf("%s: %s", kb.SettingsFile, m)
		}
	}
	rep, sha, err := k.RealignSettings(config.RealignKBSettings, "cartographer "+version+" drops the settings it no longer has")
	if err != nil {
		log.Printf("warning: KB %q: %s is not realigned: %v", name, kb.SettingsFile, err)
		return ""
	}
	if !rep.Empty() {
		logRealignment(fmt.Sprintf("KB %q %s", name, kb.SettingsFile), rep, "committed as "+sha)
	}
	return ""
}

// realignClientConfig brings ~/.cartographer.yaml up to date on a sync (D903):
// the removed keys dropped, the schema's comments written, the original kept
// beside it. Under the client-state lock, because connect and the paths
// commands write the same file; a lock it cannot take, or a file it cannot
// write, is a warning and never stops the sync.
func realignClientConfig(dir string) {
	release, err := provisioning.LockClientState(dir, provisioning.DefaultClientLockTimeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: %s is not realigned: %v\n", clientconfig.Path(dir), err)
		return
	}
	defer release()
	rep, backup, err := clientconfig.RealignFile(dir, version)
	if err != nil {
		if rep.Substantive() {
			fmt.Fprintf(os.Stderr, "warning: %s holds removed settings and cannot be rewritten (%v): they are ignored\n", clientconfig.Path(dir), err)
		}
		return
	}
	if rep.Empty() {
		return
	}
	fmt.Fprintf(os.Stderr, "%s realigned for cartographer %s (the original is kept as %s):\n", clientconfig.Path(dir), version, backup)
	for _, l := range rep.Lines() {
		fmt.Fprintf(os.Stderr, "  - %s\n", l)
	}
}
