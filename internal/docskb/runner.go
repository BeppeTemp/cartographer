package docskb

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/mcpserver"
)

// KBName is the name the documentation KB is mounted under.
const KBName = "cartographer-docs"

// tempPrefix names a runner's private materialization; leftovers older than
// staleAfter (a killed process) are swept by age only, which is safe under
// concurrency: a live runner's directory is never that old.
const (
	tempPrefix = "cartographer-docs-"
	staleAfter = 24 * time.Hour
)

// Runner runs the read tools of the real routed read-min mount (D1184) over
// the embedded documentation, in this process: no listener, no socket, no
// provider, no client config. The tools write per-KB state beside the KB and
// the KB opens on a directory, so the tree is materialized once per runner in
// its own temp directory (never $HOME), removed by Close. A change to
// mcpserver's readMinTools changes what `cartographer docs` exposes.
type Runner struct {
	dir     string
	handler http.Handler
	token   string
	id      atomic.Int64
}

// NewRunner materializes Embedded(version) and mounts it.
func NewRunner(version string) (*Runner, error) {
	files, err := Embedded(version)
	if err != nil {
		return nil, err
	}
	sweep(os.TempDir(), time.Now())
	dir, err := os.MkdirTemp("", tempPrefix)
	if err != nil {
		return nil, err
	}
	r, err := mount(dir, files, version)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return r, nil
}

func mount(dir string, files map[string][]byte, version string) (*Runner, error) {
	if err := WriteTree(files, dir); err != nil {
		return nil, err
	}
	k, err := kb.Open(dir)
	if err != nil {
		return nil, err
	}
	multi := mcpserver.NewMultiKBServer(version)
	multi.MountKB(KBName, func(s *mcpserver.Server) { mcpserver.RegisterKBTools(s, k, mcpserver.Deps{}) })
	if err := multi.EnableRoutedMount(version, nil); err != nil {
		return nil, err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	tok := hex.EncodeToString(b[:])
	store := auth.NewScopedTokenStore([]auth.ScopedToken{{Token: tok, Scopes: []auth.KBScope{{KB: KBName}}}})
	return &Runner{dir: dir, handler: store.Middleware(multi.Handler()), token: tok}, nil
}

// Do sends one JSON-RPC request (method, params) to the read-min endpoint and
// returns its result. A JSON-RPC error is returned as an error.
func (r *Runner) Do(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	raw, err := r.Raw(ctx, method, params)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("%s: %s", method, resp.Error.Message)
	}
	return resp.Result, nil
}

// Raw is Do without reading the answer: the whole JSON-RPC response object,
// error member included, for a caller that classifies it (cartographer docs).
func (r *Runner) Raw(ctx context.Context, method string, params map[string]any) (json.RawMessage, error) {
	id := r.id.Add(1)
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	req := httptest.NewRequest(http.MethodPost, mcpserver.RoutedMountPath+"?"+"access="+mcpserver.AccessReadMinValue, bytes.NewReader(body)).WithContext(ctx)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+r.token)
	rr := httptest.NewRecorder()
	r.handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		return nil, fmt.Errorf("%s: HTTP %d: %s", method, rr.Code, strings.TrimSpace(rr.Body.String()))
	}
	return json.RawMessage(rr.Body.Bytes()), nil
}

// Close removes the runner's materialization.
func (r *Runner) Close() error { return os.RemoveAll(r.dir) }

// sweep removes cartographer-docs-* directories of root older than staleAfter,
// ignoring every error.
func sweep(root string, now time.Time) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), tempPrefix) {
			continue
		}
		if info, err := e.Info(); err == nil && now.Sub(info.ModTime()) > staleAfter {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}
