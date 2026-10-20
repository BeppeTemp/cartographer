package main

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	takesValue := config.RemovedValueFlags()
	kept := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		name := strings.TrimLeft(a, "-")
		name, _, hasValue := strings.Cut(name, "=")
		if advice, ok := removed[name]; ok && strings.HasPrefix(a, "-") {
			log.Printf("warning: --%s: %s", name, advice)
			// --flag value: the value is the next argument, dropped with it, or
			// it would end the flags and hide the ones after it.
			if takesValue[name] && !hasValue && i+1 < len(args) {
				i++
			}
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
// mount) is left as it is, and works: the removed keys are ignored. The secrets
// that removed keys hold are placed in the credentials directory first, and a
// file whose secrets are not all in place is not rewritten: it still holds them,
// and the next start lists them again.
func announceConfig(cfg *config.Config, configPath string) {
	for _, w := range cfg.Warnings {
		log.Printf("warning: %s", w)
	}
	placed := migrateCredentials(cfg, configPath)
	if configPath == "" {
		return
	}
	if !placed {
		log.Printf("warning: config %s is not realigned until the credentials above are in place", configPath)
		return
	}
	// The old names of renamed KBs move to the server state file before the key
	// that held them is dropped (D906); a file whose list is not in place keeps it.
	moved, err := config.MigrateKBAliases(configPath, cfg.Data)
	if err != nil {
		log.Printf("warning: config %s is not realigned until its kb_aliases are moved to the server state file: %v", configPath, err)
		return
	}
	if moved > 0 {
		log.Printf("kb_aliases of %s moved to %s (%d item(s), D906)", configPath, config.ServerStatePath(cfg.Data), moved)
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

// migrateCredentials places in the credentials directory the secrets that the
// settings removed by D905 still hold, in the server file at configPath and in
// the environment: a file is copied, never moved, and an inline value written,
// both 0600, and a file already there is never replaced. The report names
// settings and files, never a value; what cannot be written (a read-only mount)
// is listed as the file to create. It reports whether nothing is left to place,
// which is also the case of a second run.
func migrateCredentials(cfg *config.Config, configPath string) (placed bool) {
	credDir := cfg.CredentialsPath()
	var data []byte
	if configPath != "" {
		data, _ = os.ReadFile(configPath) // a file that cannot be read did not load either
	}
	creds, problems := config.LegacyCredentials(data)
	if len(creds) == 0 && len(problems) == 0 {
		return true
	}
	if credDir == "" {
		log.Printf("warning: config %s holds credentials of removed settings, and a session on --kb folders without data has no credentials directory: set credentials_dir to place them", configPath)
		return false
	}
	if err := checkCredentialsDir(cfg); err != nil {
		// Nothing is written into a KB: the start refuses with this error.
		return false
	}
	lines, pending := placeLegacyCredentials(credDir, creds)
	lines = append(lines, problems...)
	if len(lines) > 0 {
		log.Printf("credentials of removed settings, moved to %s (D905):", credDir)
		for _, l := range lines {
			log.Printf("  - %s", l)
		}
	}
	return !pending && len(problems) == 0
}

// placeLegacyCredentials writes each target once, from the first credential
// that names it, except known_hosts, which holds the lines of every one. A
// target already in the directory with the same content is left alone and not
// reported; one that differs is kept and reported. pending is true when a file
// is still to be created by hand.
func placeLegacyCredentials(credDir string, creds []config.LegacyCredential) (lines []string, pending bool) {
	var order []string
	byTarget := map[string][]config.LegacyCredential{}
	for _, c := range creds {
		if _, seen := byTarget[c.Target]; !seen {
			order = append(order, c.Target)
		}
		byTarget[c.Target] = append(byTarget[c.Target], c)
	}
	for _, target := range order {
		group := byTarget[target]
		if target != config.KnownHostsFile {
			group = group[:1]
		}
		settings := make([]string, len(group))
		for i, c := range group {
			settings[i] = c.Setting
		}
		from := strings.Join(settings, ", ")
		path := filepath.Join(credDir, target)
		content, err := legacyCredentialContent(group)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: %v; create %s by hand", from, err, path))
			pending = true
			continue
		}
		if existing, err := os.ReadFile(path); err == nil {
			if !bytes.Equal(bytes.TrimSpace(existing), bytes.TrimSpace(content)) {
				lines = append(lines, fmt.Sprintf("%s: %s already exists with other content, kept as it is", from, path))
			}
			continue
		}
		created, err := config.WriteCredential(credDir, target, content)
		if err != nil {
			lines = append(lines, fmt.Sprintf("%s: cannot write it (%v); create %s", from, err, path))
			pending = true
			continue
		}
		if created {
			lines = append(lines, fmt.Sprintf("%s: placed as %s", from, path))
		}
	}
	return lines, pending
}

// legacyCredentialContent is the content of one target: the inline value or the
// copied file, and for known_hosts every distinct line of every file in order.
// An error is the read error, which names the file and never what is in it.
func legacyCredentialContent(group []config.LegacyCredential) ([]byte, error) {
	var out []byte
	seen := map[string]bool{}
	for _, c := range group {
		data := []byte(c.Value + "\n")
		if c.From != "" {
			var err error
			if data, err = os.ReadFile(c.From); err != nil {
				return nil, err
			}
		}
		if len(group) == 1 {
			return data, nil
		}
		for _, l := range strings.Split(string(data), "\n") {
			if l = strings.TrimRight(l, "\r"); l != "" && !seen[l] {
				seen[l] = true
				out = append(out, l+"\n"...)
			}
		}
	}
	return out, nil
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
