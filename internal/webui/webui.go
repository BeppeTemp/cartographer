// Package webui serves the embedded read-only Atlas UI (D227).
//
// The production bundle under dist/ is committed to the repository and
// embedded with go:embed. That is deliberate and it is the reason a generated
// artifact lives in version control: `go install
// github.com/BeppeTemp/cartographer/cmd/cartographer@latest` cannot run an npm
// build, and the promise is that every installation path — Homebrew, the
// winget archive, the install script, the container, `go install` and the
// native service — serves the same UI from the one binary. A committed bundle
// is the price of that promise.
//
// What keeps the committed bundle honest is provenance.json: `make web` writes
// the hash of the web/ source tree into it, and TestBundleMatchesSource
// recomputes that hash and fails when the two disagree. The check is pure Go,
// so `make gate` catches a stale bundle on a machine with no Node installed.
// There is deliberately no reproducible-rebuild check: Vite output is not
// byte-stable across Node patch releases, and a gate that fails for reasons
// unrelated to the change is a gate that gets bypassed.
package webui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

//go:embed all:dist
var bundle embed.FS

// MountPath is where the UI is served. The trailing slash is part of it: the
// SPA owns everything below this prefix and nothing at all outside it.
const MountPath = "/ui/"

// ProvenanceFile records which source tree produced the embedded bundle.
const ProvenanceFile = "provenance.json"

// Provenance is the manifest `make web` writes next to the bundle.
type Provenance struct {
	// SourceHash is the hash of the web/ source tree, as SourceHash computes
	// it. It is the whole point of the file.
	SourceHash string `json:"source_hash"`
	// Files is the number of source files that went into the hash, carried for
	// a human reading a diff: a hash that changed because one file moved looks
	// exactly like a hash that changed because the tree was emptied.
	Files int `json:"files"`
}

// Assets returns the embedded bundle rooted at its own directory, so the
// caller sees "index.html" rather than "dist/index.html".
func Assets() (fs.FS, error) {
	return fs.Sub(bundle, "dist")
}

// ReadProvenance returns the manifest embedded beside the bundle.
func ReadProvenance() (Provenance, error) {
	assets, err := Assets()
	if err != nil {
		return Provenance{}, err
	}
	raw, err := fs.ReadFile(assets, ProvenanceFile)
	if err != nil {
		return Provenance{}, fmt.Errorf("webui: reading %s: %w", ProvenanceFile, err)
	}
	var p Provenance
	if err := json.Unmarshal(raw, &p); err != nil {
		return Provenance{}, fmt.Errorf("webui: parsing %s: %w", ProvenanceFile, err)
	}
	return p, nil
}

// skipDir names the directories SourceHash never descends into: npm's install
// tree, the build output (hashing the bundle into its own provenance would be
// circular) and editor droppings.
func skipDir(name string) bool {
	switch name {
	case "node_modules", "dist", ".vite", ".git", "coverage", "playwright-report", "test-results":
		return true
	}
	return false
}

// SourceHash hashes a frontend source tree: every file path and its content,
// in sorted order. Paths are included, not only contents, so renaming a file
// changes the hash — a rename is a change to what the bundle was built from
// even when no byte of source moved.
func SourceHash(root string) (string, int, error) {
	digest := sha256.New()
	var paths []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		paths = append(paths, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return "", 0, fmt.Errorf("webui: walking %s: %w", root, err)
	}
	sort.Strings(paths)

	for _, rel := range paths {
		fmt.Fprintf(digest, "%s\x00", rel)
		file, err := os.Open(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			return "", 0, err
		}
		if _, err := io.Copy(digest, file); err != nil {
			file.Close()
			return "", 0, err
		}
		file.Close()
		fmt.Fprint(digest, "\x00")
	}
	return hex.EncodeToString(digest.Sum(nil)), len(paths), nil
}

// Handler serves the embedded UI under MountPath.
//
// Hashed asset filenames are immutable, so they are cached forever; the shell
// is not, so it must be revalidated or a browser keeps loading last release's
// entry point against this release's API. Any unknown path below MountPath
// serves the shell, because client-side routing owns those URLs — and nothing
// outside MountPath is routed here at all, which is what keeps the SPA
// fallback from swallowing /mcp, /health or /api.
func Handler() (http.Handler, error) {
	assets, err := Assets()
	if err != nil {
		return nil, err
	}
	shell, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		return nil, fmt.Errorf("webui: the embedded bundle has no index.html: %w", err)
	}
	files := http.FileServer(http.FS(assets))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		rel := strings.TrimPrefix(r.URL.Path, MountPath)
		// Clean resolves any ".." the request encoded, so a traversal can at
		// worst land on another file of the bundle -- never outside it.
		rel = strings.TrimPrefix(path.Clean("/"+rel), "/")

		// The provenance manifest is build metadata, not part of the UI: it is
		// embedded so a test can read it, not so a browser can.
		if rel != "" && rel != "." && rel != ProvenanceFile {
			if file, err := assets.Open(rel); err == nil {
				info, statErr := file.Stat()
				file.Close()
				if statErr == nil && !info.IsDir() {
					writeSecurityHeaders(w)
					if strings.HasPrefix(rel, "assets/") {
						w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
					} else {
						w.Header().Set("Cache-Control", "no-cache")
					}
					r2 := r.Clone(r.Context())
					r2.URL.Path = "/" + rel
					files.ServeHTTP(w, r2)
					return
				}
			} else if !errors.Is(err, fs.ErrNotExist) {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
		}

		writeSecurityHeaders(w)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodHead {
			return
		}
		w.Write(shell)
	}), nil
}

// writeSecurityHeaders applies the policy the UI is built to satisfy: no
// inline script, no eval, no remote origin of any kind. The UI ships every
// asset it uses, so `default-src 'self'` costs it nothing — which is exactly
// why it can be this strict. style-src allows 'unsafe-inline' because React
// sets element style attributes for the few values that are computed per
// element; a style attribute cannot execute script.
func writeSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		// No worker-src: the UI runs no worker since the live force layout
		// was removed, so default-src 'self' governs workers and a blob:
		// worker is refused.
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data:",
		"font-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'none'",
	}, "; "))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
}

// RedirectRoot sends a browser that asked for "/" to the UI, and passes
// everything else through untouched.
//
// It wraps the *outside* of the authentication chain on purpose. A person
// typing the server's bare address has no bearer token yet, so inside the
// chain "/" would answer 401 and the UI would be reachable only by someone who
// already knew to type /ui/. Exempting "/" from authentication instead would
// weaken the middleware's default for every future endpoint; a redirect that
// reveals nothing is the narrower fix.
func RedirectRoot(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			http.Redirect(w, r, MountPath, http.StatusMovedPermanently)
			return
		}
		next.ServeHTTP(w, r)
	})
}
