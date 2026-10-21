package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"github.com/BeppeTemp/cartographer/internal/client"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientconfig"
	"github.com/BeppeTemp/cartographer/internal/config"
	"gopkg.in/yaml.v3"
)

var configFn = cmdConfig

// settingsTimeout bounds the one GET of the server's settings; an unreachable
// server must cost a line of output, not a hang.
const settingsTimeout = 5 * time.Second

// settingsReply is GET /api/ui/v1/settings (D743).
type settingsReply struct {
	Version       string         `json:"version"`
	LatestVersion string         `json:"latest_version,omitempty"`
	UpdateChannel string         `json:"update_channel,omitempty"`
	Server        []config.Entry `json:"server"`
	KBs           []struct {
		Name    string         `json:"name"`
		Entries []config.Entry `json:"entries"`
	} `json:"kbs"`
}

// configView is what `cartographer config --json` prints.
type configView struct {
	Client []config.Entry `json:"client"`
	Server *settingsReply `json:"server,omitempty"`
	Note   string         `json:"note,omitempty"`
}

// cmdConfig shows every setting with its effective value and where it comes
// from: this machine's client settings, and the server's when one answers.
func cmdConfig(args []string) int {
	fs := flag.NewFlagSet("config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	asJSON := fs.Bool("json", false, "Print the raw structure as JSON")
	all := fs.Bool("all", false, "Also list the keys nothing set, with their default")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		return 2
	}
	dir, err := clientconfig.TargetDir()
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 2
	}
	return runConfig(os.Stdout, dir, *asJSON, *all)
}

func runConfig(w io.Writer, dir string, asJSON, all bool) int {
	view := configView{Client: clientEntries(dir)}
	cfg, err := clientconfig.Load(dir)
	if err != nil {
		view.Note = "no client config (run `cartographer connect` first): server settings not requested"
	} else if reply, note := fetchSettings(cfg); reply != nil {
		view.Server = reply
	} else {
		view.Note = note
	}

	if asJSON {
		if !all {
			view.Client = setOnly(view.Client)
			if view.Server != nil {
				view.Server.Server = setOnly(view.Server.Server)
				for i := range view.Server.KBs {
					view.Server.KBs[i].Entries = setOnly(view.Server.KBs[i].Entries)
				}
			}
		}
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		_ = enc.Encode(view)
		return 0
	}

	printEntries(w, "Client", view.Client, all)
	if view.Server != nil {
		fmt.Fprintf(w, "\nServer (version %s", view.Server.Version)
		if view.Server.LatestVersion != "" {
			fmt.Fprintf(w, ", %s available", view.Server.LatestVersion)
		}
		if view.Server.UpdateChannel != "" {
			fmt.Fprintf(w, ", installed by %s", view.Server.UpdateChannel)
		}
		fmt.Fprintln(w, ")")
		printEntries(w, "", view.Server.Server, all)
		for _, kb := range view.Server.KBs {
			printEntries(w, "KB "+kb.Name, kb.Entries, all)
		}
	}
	if view.Note != "" {
		fmt.Fprintf(w, "\nnote: %s\n", view.Note)
	}
	return 0
}

func setOnly(entries []config.Entry) []config.Entry {
	out := []config.Entry{}
	for _, e := range entries {
		if e.Origin != config.OriginDefault {
			out = append(out, e)
		}
	}
	return out
}

func printEntries(w io.Writer, title string, entries []config.Entry, all bool) {
	if !all {
		entries = setOnly(entries)
	}
	if title != "" {
		fmt.Fprintf(w, "\n%s\n", title)
	}
	width := 0
	for _, e := range entries {
		if len(e.Key) > width {
			width = len(e.Key)
		}
	}
	for _, e := range entries {
		restart := ""
		if e.Restart {
			restart = ", restart required"
		}
		fmt.Fprintf(w, "  %-*s  %v  (%s%s)\n", width, e.Key, e.Value, e.Origin, restart)
	}
}

// clientEntries lists the client settings: a key present in .cartographer.yaml
// is "file", every other key shows its default. A missing file is all defaults.
func clientEntries(dir string) []config.Entry {
	var doc map[string]any
	if b, err := os.ReadFile(clientconfig.Path(dir)); err == nil {
		_ = yaml.Unmarshal(b, &doc)
	}
	out := []config.Entry{}
	for _, k := range clientconfig.Schema.Keys {
		e := config.Entry{Key: k.Path, Value: k.Default, Origin: config.OriginDefault,
			Description: k.Description, Default: k.Default, Env: k.Env, Flag: k.Flag, Restart: k.Restart, Secret: k.Secret}
		if v, ok := lookupPath(doc, k.Path); ok {
			e.Value, e.Origin = v, config.OriginFile
			// A literal secret is never printed; a ${NAME} reference names a
			// variable, not a secret, and is shown as written.
			if k.Secret {
				if str, isStr := v.(string); !isStr || !clientconfig.IsEnvRef(str) {
					e.Value = config.SecretSet
				}
			}
		}
		out = append(out, e)
	}
	return out
}

func lookupPath(doc map[string]any, path string) (any, bool) {
	var cur any = doc
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// fetchSettings asks the configured server for its settings. Any failure is a
// one-line note, never an error: the client section is still worth printing.
func fetchSettings(cfg *clientconfig.Config) (*settingsReply, string) {
	base := strings.TrimSuffix(strings.TrimRight(cfg.ServerURL, "/"), "/mcp")
	if err := client.CheckServerURL(cfg.ServerURL, cfg.InsecureHTTPAllowed()); err != nil {
		return nil, fmt.Sprintf("server settings unavailable: %v", err)
	}
	req, err := http.NewRequest(http.MethodGet, base+"/api/ui/v1/settings", nil)
	if err != nil {
		return nil, fmt.Sprintf("server settings unavailable: %v", err)
	}
	if tok := resolveToken(cfg); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := client.HTTPClient(&http.Client{Timeout: settingsTimeout}, cfg.InsecureHTTPAllowed()).Do(req)
	if err != nil {
		return nil, fmt.Sprintf("server at %s not reachable: server settings not shown", base)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("server at %s answered %d: server settings not shown", base, resp.StatusCode)
	}
	var reply settingsReply
	if err := json.NewDecoder(resp.Body).Decode(&reply); err != nil {
		return nil, fmt.Sprintf("server at %s sent no settings (older version?): server settings not shown", base)
	}
	return &reply, ""
}
