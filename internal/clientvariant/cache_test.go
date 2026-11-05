package clientvariant_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/clientvariant"
	"github.com/BeppeTemp/cartographer/internal/configurator"
)

func cacheOf(t *testing.T, descriptors map[string]string) (string, clientvariant.Cache) {
	t.Helper()
	dir := t.TempDir()
	c := clientvariant.Cache{}
	for n, d := range descriptors {
		c[n] = clientvariant.NewCacheEntry("kb-a", []byte(d), time.Unix(0, 0))
	}
	if err := clientvariant.SaveCache(dir, c); err != nil {
		t.Fatal(err)
	}
	return dir, c
}

func hostEnv(dir string) clientvariant.Env {
	return clientvariant.Env{GOOS: "linux", Home: dir, Getenv: func(string) string { return "" }}
}

func reset(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { _ = configurator.ReplaceVariants(nil) })
	_ = configurator.ReplaceVariants(nil)
}

func TestCacheRoundTripAndRestart(t *testing.T) {
	reset(t)
	dir, _ := cacheOf(t, map[string]string{"fork-cli": forkCLI})
	got, warns := clientvariant.RegisterCached(dir, hostEnv(dir))
	if len(warns) != 0 || len(got) != 1 || got[0] != "fork-cli" {
		t.Fatalf("registered %v, warnings %v", got, warns)
	}
	if _, ok := configurator.Lookup("fork-cli"); !ok {
		t.Fatal("the variant is not registered")
	}
	// A restart: the registry is empty again, the cache restores it.
	_ = configurator.ReplaceVariants(nil)
	if _, ok := configurator.Lookup("fork-cli"); ok {
		t.Fatal("reset did not empty the registry")
	}
	if got, _ := clientvariant.RegisterCached(dir, hostEnv(dir)); len(got) != 1 {
		t.Fatalf("restart registered %v", got)
	}
}

func TestRegisterCachedRevalidates(t *testing.T) {
	reset(t)
	escaping := strings.Replace(forkCLI, `"{xdg_config}/fork-cli/skills"`, `"{home}/../x"`, 1)
	for name, tc := range map[string]struct {
		mut  func(dir string, c clientvariant.Cache)
		want string
	}{
		"edited bytes": {func(dir string, c clientvariant.Cache) {
			e := c["fork-cli"]
			e.DescriptorB64 = base64.StdEncoding.EncodeToString([]byte(escaping))
			c["fork-cli"] = e
		}, "hash"},
		"escaping path, rehashed": {func(dir string, c clientvariant.Cache) {
			c["fork-cli"] = clientvariant.NewCacheEntry("kb-a", []byte(escaping), time.Unix(0, 0))
		}, "skipped"},
		"not yaml": {func(dir string, c clientvariant.Cache) {
			c["fork-cli"] = clientvariant.NewCacheEntry("kb-a", []byte("{not"), time.Unix(0, 0))
		}, "skipped"},
	} {
		t.Run(name, func(t *testing.T) {
			dir, c := cacheOf(t, map[string]string{"fork-cli": forkCLI})
			tc.mut(dir, c)
			if err := clientvariant.SaveCache(dir, c); err != nil {
				t.Fatal(err)
			}
			got, warns := clientvariant.RegisterCached(dir, hostEnv(dir))
			if len(got) != 0 || len(warns) != 1 || !strings.Contains(warns[0], tc.want) {
				t.Fatalf("registered %v, warnings %v", got, warns)
			}
			if _, ok := configurator.Lookup("fork-cli"); ok {
				t.Fatal("an invalid cache entry registered")
			}
		})
	}
}

func TestCorruptCacheIsAWarningNotAFailure(t *testing.T) {
	reset(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, clientvariant.CacheFileName), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, warns := clientvariant.RegisterCached(dir, hostEnv(dir)); len(got) != 0 || len(warns) != 1 {
		t.Fatalf("registered %v, warnings %v", got, warns)
	}
	if got, warns := clientvariant.RegisterCached(t.TempDir(), hostEnv(dir)); len(got) != 0 || len(warns) != 0 {
		t.Fatalf("absent cache: %v %v", got, warns)
	}
}

func TestRetiredNeedsACompletePull(t *testing.T) {
	c := clientvariant.Cache{"a": {}, "b": {}, "c": {}}
	declared := map[string]bool{"b": true}
	if got := c.Retired(declared, true); strings.Join(got, ",") != "a,c" {
		t.Fatalf("retired %v", got)
	}
	if got := c.Retired(declared, false); got != nil {
		t.Fatalf("a partial pull retired %v", got)
	}
}
