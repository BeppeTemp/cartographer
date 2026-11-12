package config

import (
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/hooks"
)

// hookPlugins pins the plugin "acme" in a store under dir: an absolute path on
// every OS (a literal "/srv/p" is not absolute on Windows), single-quoted so
// backslashes survive YAML.
func hookPlugins(dir string) string {
	return "plugins_dir: '" + dir + "'\nplugins:\n  acme:\n    digest: 0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"
}

const hookEntry = "hooks:\n  - name: acme/review\n    point: write_veto\n    plugin: acme\n    url: https://hooks.example.com/veto\n    credential: acme.token\n    failure_policy: fail_closed\n"

func TestHooks_Load(t *testing.T) {
	cfg, err := loadComponents(t, hookPlugins(t.TempDir())+hookEntry+"    timeout: 3s\n    mode: dry_run\n    kbs: [kb-a]\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hooks) != 1 {
		t.Fatalf("%+v", cfg.Hooks)
	}
	h := cfg.Hooks[0]
	if h.Name != "acme/review" || h.Point != hooks.PointWriteVeto || h.Timeout != 3*time.Second || h.Mode != hooks.ModeDryRun || h.KBs[0] != "kb-a" {
		t.Fatalf("%+v", h)
	}
	cfg, err = loadComponents(t, "web:\n  enabled: true\n")
	if err != nil || len(cfg.Hooks) != 0 {
		t.Fatalf("absent key: %v %v", cfg, err)
	}
}

func TestHooks_IdentityHeaders(t *testing.T) {
	cfg, err := loadComponents(t, hookPlugins(t.TempDir())+strings.Replace(hookEntry, "write_veto", "identity", 1)+"    headers: [X-Session, Cookie]\n    cache_ttl: 5m\n")
	if err != nil {
		t.Fatal(err)
	}
	if h := cfg.Hooks[0]; len(h.Headers) != 2 || h.CacheTTL != 5*time.Minute {
		t.Fatalf("%+v", h)
	}
}

func TestHooks_LoadErrors(t *testing.T) {
	for name, tc := range map[string]struct{ body, want string }{
		"not pinned":           {hookEntry, `plugin "acme" is not pinned`},
		"no policy":            {hookPlugins(t.TempDir()) + strings.Replace(hookEntry, "    failure_policy: fail_closed\n", "", 1), "failure_policy is required"},
		"unknown key":          {hookPlugins(t.TempDir()) + hookEntry + "    failure_polcy: fail_open\n", `unknown key "failure_polcy" under hooks[0]`},
		"bad timeout":          {hookPlugins(t.TempDir()) + hookEntry + "    timeout: soon\n", "timeout"},
		"long timeout":         {hookPlugins(t.TempDir()) + hookEntry + "    timeout: 11s\n", "timeout"},
		"plain http":           {hookPlugins(t.TempDir()) + strings.Replace(hookEntry, "https://hooks.example.com", "http://hooks.example.com", 1), "loopback"},
		"duplicate":            {hookPlugins(t.TempDir()) + hookEntry + hookEntry[len("hooks:\n"):], "twice"},
		"bad mode":             {hookPlugins(t.TempDir()) + hookEntry + "    mode: shadow\n", "mode"},
		"misspelled list":      {hookPlugins(t.TempDir()) + strings.Replace(hookEntry, "hooks:", "hook:", 1), `did you mean "hooks"`},
		"headers off identity": {hookPlugins(t.TempDir()) + hookEntry + "    headers: [X-Session]\n", "headers applies to identity only"},
		"bearer header":        {hookPlugins(t.TempDir()) + strings.Replace(hookEntry, "write_veto", "identity", 1) + "    headers: [authorization]\n", "not an allowed header name"},
		"batch zero":           {hookPlugins(t.TempDir()) + hookEntry + "    batch_size: 0\n", "batch_size"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadComponents(t, tc.body)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

// A misspelled key is refused wherever the hooks list comes from: written in
// place, through an alias of the list, or through a top-level merge.
func TestHooks_UnknownKeyThroughAliasAndMerge(t *testing.T) {
	entry := "{name: acme/review, point: write_veto, plugin: acme, url: 'https://hooks.example.com/v', credential: acme.token, failure_polcy: fail_open}"
	for name, body := range map[string]string{
		"alias of the list": "x: &h [" + entry + "]\nhooks: *h\n",
		"merged section":    "base: &b\n  hooks:\n    - " + entry + "\n<<: *b\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadComponents(t, hookPlugins(t.TempDir())+body)
			if err == nil || !strings.Contains(err.Error(), "failure_polcy") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestHooks_CacheTTLDefaultForConsumers(t *testing.T) {
	body := hookPlugins(t.TempDir()) + strings.Replace(hookEntry, "write_veto", "identity", 1)
	cfg, err := loadComponents(t, body)
	if err != nil || cfg.Hooks[0].CacheTTL != hooks.DefaultCacheTTL {
		t.Fatalf("%v %+v", err, cfg)
	}
}
