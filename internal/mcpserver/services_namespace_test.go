package mcpserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// D269: services/ is a concept namespace rooted at the KB root, a sibling of
// data/. These tests pin that every operation — map creation, and every read,
// collision check, write and removal of concept_move — agrees on where a
// services/ ID lives.

// treeSnapshot maps every file under root (slash-separated, relative) to its
// bytes, and every directory to "", skipping .git.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if info.Name() == ".cartographer.lock" { // gitWrap's per-KB lock file
			return nil
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			out[rel+"/"] = ""
			return nil
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("treeSnapshot: %v", err)
	}
	return out
}

func sameTree(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	var diffs []string
	for k, v := range before {
		if w, ok := after[k]; !ok {
			diffs = append(diffs, "removed "+k)
		} else if w != v {
			diffs = append(diffs, "changed "+k)
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			diffs = append(diffs, "added "+k)
		}
	}
	if len(diffs) > 0 {
		sort.Strings(diffs)
		t.Fatalf("%s changed the tree: %s", what, strings.Join(diffs, ", "))
	}
}

func writeService(t *testing.T, k *kb.KB, id, body string) {
	t.Helper()
	fm, _ := okf.ParseFrontmatter("type: Service\ntitle: " + filepath.Base(id))
	if _, err := k.WriteConcept(okf.ConceptID(id), fm, body, ""); err != nil {
		t.Fatalf("write %s: %v", id, err)
	}
}

func writeNote(t *testing.T, k *kb.KB, id, body string) {
	t.Helper()
	fm, _ := okf.ParseFrontmatter("type: Note\ntitle: " + filepath.Base(id))
	if _, err := k.WriteConcept(okf.ConceptID(id), fm, body, ""); err != nil {
		t.Fatalf("write %s: %v", id, err)
	}
}

func namespaceServer(t *testing.T) (*kb.KB, *Server) {
	t.Helper()
	k := setupTestKB(t)
	if err := k.CreateMap("application-services", "Application Services", "map", nil, ""); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})
	return k, s
}

// physicalConceptFile is where a concept ID must live on disk, spelled out
// independently of the resolver under test.
func physicalConceptFile(k *kb.KB, id string) string {
	if strings.HasPrefix(id, "services/") {
		return filepath.Join(k.Root, filepath.FromSlash(id)+".md")
	}
	return filepath.Join(k.DataRoot(), filepath.FromSlash(id)+".md")
}

func physicalConceptDir(k *kb.KB, id string) string {
	return strings.TrimSuffix(physicalConceptFile(k, id), ".md")
}

func assertGone(t *testing.T, k *kb.KB, id string) {
	t.Helper()
	if _, err := k.ReadConcept(okf.ConceptID(id)); !errors.Is(err, okf.ErrNotFound) {
		t.Fatalf("%s still readable after the move (err=%v)", id, err)
	}
	for _, p := range []string{physicalConceptFile(k, id), physicalConceptDir(k, id)} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s: %s still on disk (err=%v)", id, p, err)
		}
	}
}

func assertNoShadow(t *testing.T, k *kb.KB) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(k.DataRoot(), "services")); !os.IsNotExist(err) {
		t.Fatalf("a data/services/ shadow tree was created (err=%v)", err)
	}
}

// --- WP1: map_create refuses the reserved name ---

func TestMapCreate_ServicesReservedThroughMCP(t *testing.T) {
	for _, kind := range []string{"map", "journal", ""} {
		for _, withDescriptor := range []bool{false, true} {
			t.Run(fmt.Sprintf("kind=%q/descriptor=%v", kind, withDescriptor), func(t *testing.T) {
				k := setupTestKB(t)
				if withDescriptor {
					writeService(t, k, "services/sample", "svc\n")
				}
				s := New("test")
				RegisterKBTools(s, k, Deps{})
				before := treeSnapshot(t, k.Root)

				args := map[string]any{"name": "services", "title": "Services"}
				if kind != "" {
					args["kind"] = kind
				}
				raw, _ := json.Marshal(args)
				res := callTool(t, s, "map_create", string(raw))
				if !res.IsError {
					t.Fatalf("map_create services succeeded: %+v", res.Content)
				}
				if msg := res.Content[0].Text; !strings.Contains(msg, "reserved for service descriptors") || !strings.Contains(msg, "invalid path") {
					t.Fatalf("map_create services: unexpected message %q", msg)
				}
				sameTree(t, "rejected map_create", before, treeSnapshot(t, k.Root))

				if out := callOK(t, s, "validate", `{}`); !strings.Contains(out, "Validation OK") {
					t.Fatalf("validate after rejection: %s", out)
				}
				if withDescriptor {
					if out := callOK(t, s, "service_get", `{"service_id":"services/sample"}`); !strings.Contains(out, "svc") {
						t.Fatalf("service_get after rejection: %s", out)
					}
				}
			})
		}
	}
}

// The reservation is the exact name, and it is not the Service type: a map
// named like a service collection has the ordinary lifecycle, and a
// lowercase `type: service` descriptor is still found (D158).
func TestMapCreate_NonCollidingMapLifecycle(t *testing.T) {
	k := setupTestKB(t)
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	callOK(t, s, "map_create", `{"name":"application-services","title":"Application Services","kind":"map"}`)
	if out := callOK(t, s, "map_list", `{}`); !strings.Contains(out, "application-services") || !strings.Contains(out, "Application Services") {
		t.Fatalf("map_list lacks the map or its metadata: %s", out)
	}
	if out := callOK(t, s, "index_get", `{"path":"application-services"}`); !strings.Contains(out, "Application Services") {
		t.Fatalf("index_get: %s", out)
	}
	if out := callOK(t, s, "validate", `{}`); !strings.Contains(out, "Validation OK") {
		t.Fatalf("validate: %s", out)
	}
	callOK(t, s, "map_delete", `{"map":"application-services"}`)
	if _, err := os.Stat(filepath.Join(k.DataRoot(), "application-services")); !os.IsNotExist(err) {
		t.Fatalf("map_delete left the directory: %v", err)
	}

	callOK(t, s, "concept_write", `{"id":"services/lower","frontmatter":{"type":"service","title":"Lower"},"body":"lowercase-type\n"}`)
	if out := callOK(t, s, "service_get", `{"service_id":"services/lower"}`); !strings.Contains(out, "lowercase-type") {
		t.Fatalf("service_get lowercase type: %s", out)
	}
	if out := callOK(t, s, "service_list", `{}`); !strings.Contains(out, "services/lower") {
		t.Fatalf("service_list lowercase type: %s", out)
	}
	assertNoShadow(t, k)
}

// --- WP2: concept_move resolves both ends in their real namespace ---

func TestConceptMove_FlatAcrossServicesNamespace(t *testing.T) {
	cases := []struct{ name, src, dst string }{
		{"services-to-map", "services/sample", "application-services/sample"},
		{"map-to-services", "application-services/sample", "services/sample"},
		{"within-services", "services/sample", "services/renamed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, s := namespaceServer(t)
			writeService(t, k, tc.src, "flat-body\n")
			before, err := k.ReadConcept(okf.ConceptID(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]any{"source_id": tc.src, "target_id": tc.dst, "rewrite_links": false})
			out := callOK(t, s, "concept_move", string(args))
			if !strings.Contains(out, "are not updated") {
				t.Fatalf("rewrite_links:false lost its warning: %s", out)
			}
			after, err := k.ReadConcept(okf.ConceptID(tc.dst))
			if err != nil {
				t.Fatalf("destination unreadable: %v", err)
			}
			if after.ContentHash != before.ContentHash {
				t.Fatalf("content changed: %s → %s", before.ContentHash, after.ContentHash)
			}
			if _, err := os.Stat(physicalConceptFile(k, tc.dst)); err != nil {
				t.Fatalf("destination not at its physical path: %v", err)
			}
			assertGone(t, k, tc.src)
			assertNoShadow(t, k)
		})
	}
}

func TestConceptMove_FlatBatchAcrossServicesNamespace(t *testing.T) {
	k, s := namespaceServer(t)
	writeService(t, k, "services/one", "one\n")
	writeService(t, k, "services/two", "two\n")
	writeNote(t, k, "application-services/three", "three\n")
	callOK(t, s, "concept_move", `{"moves":[
		{"source_id":"services/one","target_id":"application-services/one"},
		{"source_id":"services/two","target_id":"services/two-renamed"},
		{"source_id":"application-services/three","target_id":"services/three"}]}`)
	for _, pair := range [][2]string{{"services/one", "application-services/one"}, {"services/two", "services/two-renamed"}, {"application-services/three", "services/three"}} {
		assertGone(t, k, pair[0])
		if _, err := k.ReadConcept(okf.ConceptID(pair[1])); err != nil {
			t.Fatalf("%s unreadable: %v", pair[1], err)
		}
	}
	assertNoShadow(t, k)
}

func TestConceptMove_ExpandedAcrossServicesNamespace(t *testing.T) {
	asset := []byte{0x00, 0xff, 0x10, 0x80, 'b', 'i', 'n'}
	cases := []struct{ name, src, dst string }{
		{"services-to-map", "services/owner", "application-services/owner"},
		{"map-to-services", "application-services/owner", "services/owner"},
		{"within-services", "services/owner", "services/moved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, s := namespaceServer(t)
			writeService(t, k, tc.src, "owner-body\n")
			if err := k.ExpandConcept(okf.ConceptID(tc.src)); err != nil {
				t.Fatal(err)
			}
			writeNote(t, k, tc.src+"/sat", "quokkasatellite\n")
			// The asset API only serves data/ concepts, so under services/
			// the asset is placed by hand: a move must carry it either way.
			if strings.HasPrefix(tc.src, "services/") {
				p := filepath.Join(physicalConceptDir(k, tc.src), "files", "blob.bin")
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, asset, 0o644); err != nil {
					t.Fatal(err)
				}
			} else if _, err := k.WriteAsset(okf.ConceptID(tc.src), "files/blob.bin", asset, "", nil); err != nil {
				t.Fatal(err)
			}
			if out := searchText(t, s, "quokkasatellite"); !strings.Contains(out, tc.src+"/sat") {
				t.Fatalf("satellite not indexed before the move: %s", out)
			}

			args, _ := json.Marshal(map[string]any{"source_id": tc.src, "target_id": tc.dst})
			callOK(t, s, "concept_move", string(args))

			dstDir := physicalConceptDir(k, tc.dst)
			for _, rel := range []string{"index.md", "sat.md"} {
				if _, err := os.Stat(filepath.Join(dstDir, rel)); err != nil {
					t.Fatalf("destination tree lacks %s: %v", rel, err)
				}
			}
			got, err := os.ReadFile(filepath.Join(dstDir, "files", "blob.bin"))
			if err != nil || !bytes.Equal(got, asset) {
				t.Fatalf("asset bytes: %v %v", got, err)
			}
			if _, err := k.ReadConcept(okf.ConceptID(tc.dst + "/sat")); err != nil {
				t.Fatalf("satellite unreadable under the new id: %v", err)
			}
			assertGone(t, k, tc.src)
			assertGone(t, k, tc.src+"/sat")
			assertNoShadow(t, k)

			out := searchText(t, s, "quokkasatellite")
			if !strings.Contains(out, tc.dst+"/sat") || strings.Contains(out, `"`+tc.src+`/sat"`) {
				t.Fatalf("search does not follow the move: %s", out)
			}
			ids := map[string]bool{}
			if err := k.WalkConcepts(func(id okf.ConceptID, _ string) error { ids[string(id)] = true; return nil }); err != nil {
				t.Fatal(err)
			}
			if ids[tc.src] || ids[tc.src+"/sat"] || !ids[tc.dst] || !ids[tc.dst+"/sat"] {
				t.Fatalf("concept walk after the move: %v", ids)
			}
		})
	}
}

// Every refusal happens before anything is applied, so a batch whose last
// entry is bad leaves the tree exactly as it was.
func TestConceptMove_ServicesPreflightRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, k *kb.KB)
		last  string // the batch's bad final entry
		want  string
	}{
		{"flat-target-occupied", func(t *testing.T, k *kb.KB) { writeService(t, k, "services/taken", "x\n") },
			`{"source_id":"application-services/b","target_id":"services/taken"}`, "conflict: target already exists"},
		{"expanded-target-occupied", func(t *testing.T, k *kb.KB) {
			writeService(t, k, "services/taken", "x\n")
			if err := k.ExpandConcept("services/taken"); err != nil {
				t.Fatal(err)
			}
		}, `{"source_id":"application-services/b","target_id":"services/taken"}`, "conflict"},
		{"asset-only-target-dir", func(t *testing.T, k *kb.KB) {
			if err := os.MkdirAll(filepath.Join(k.Root, "services", "orphan"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(k.Root, "services", "orphan", "data.bin"), []byte{1, 2}, 0o644); err != nil {
				t.Fatal(err)
			}
		}, `{"source_id":"application-services/b","target_id":"services/orphan"}`, "conflict: target directory already exists"},
		{"expanded-into-asset-only-dir", func(t *testing.T, k *kb.KB) {
			if err := k.ExpandConcept("application-services/b"); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Join(k.Root, "services", "orphan"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, `{"source_id":"application-services/b","target_id":"services/orphan"}`, "conflict: target directory already exists"},
		{"missing-source", nil, `{"source_id":"services/nope","target_id":"application-services/nope"}`, "read source"},
		{"escaping-target", nil, `{"source_id":"application-services/b","target_id":"services/../../etc"}`, "target_id"},
		{"invalid-target", nil, `{"source_id":"application-services/b","target_id":"services/Bad Name"}`, "invalid target_id"},
		{"duplicate-source", nil, `{"source_id":"services/a","target_id":"application-services/other"}`, "duplicate source_id"},
		{"duplicate-target", nil, `{"source_id":"application-services/b","target_id":"application-services/a"}`, "duplicate target_id"},
		{"overlapping-expanded", func(t *testing.T, k *kb.KB) {
			if err := k.ExpandConcept("application-services/b"); err != nil {
				t.Fatal(err)
			}
			writeNote(t, k, "application-services/b/sat", "sat\n")
		}, `{"source_id":"application-services/b","target_id":"services/b"},{"source_id":"application-services/b/sat","target_id":"services/sat"}`, "expanded concept moves cannot overlap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, s := namespaceServer(t)
			writeService(t, k, "services/a", "a\n")
			writeNote(t, k, "application-services/b", "b\n")
			if tc.setup != nil {
				tc.setup(t, k)
			}
			before := treeSnapshot(t, k.Root)
			args := `{"moves":[{"source_id":"services/a","target_id":"application-services/a"},` + tc.last + `]}`
			res := callTool(t, s, "concept_move", args)
			if !res.IsError {
				t.Fatalf("batch accepted: %s", res.Content[0].Text)
			}
			if !strings.Contains(res.Content[0].Text, tc.want) {
				t.Fatalf("message %q lacks %q", res.Content[0].Text, tc.want)
			}
			sameTree(t, "refused batch", before, treeSnapshot(t, k.Root))
		})
	}
}

// Link maintenance is namespace-independent: inbound links, the moved body's
// own relative links, a curated index and superseded_by all follow a move
// across the services/ boundary.
func TestConceptMove_ServicesLinkMaintenance(t *testing.T) {
	k := setupTestKB(t)
	contract := kb.MapContract{RequireIndexEntry: true}
	if err := k.CreateMapWithContract("application-services", "Application Services", "map", nil, "", contract); err != nil {
		t.Fatal(err)
	}
	s := New("test")
	RegisterKBTools(s, k, Deps{})

	writeService(t, k, "services/sample", "See [peer](peer.md).\n")
	writeService(t, k, "services/peer", "peer\n")
	writeNote(t, k, "manutenzione/ref", "Uses [[services/sample]] and [s](../services/sample.md).\n")
	pred, _ := okf.ParseFrontmatter("type: Note\ntitle: Old\nstatus: superseded\nsuperseded_by: services/sample")
	if _, err := k.WriteConcept("manutenzione/old", pred, "old\n", ""); err != nil {
		t.Fatal(err)
	}

	out := callOK(t, s, "concept_move", `{"source_id":"services/sample","target_id":"application-services/sample"}`)
	if !strings.Contains(out, "added an entry for application-services/sample") {
		t.Fatalf("curated index not maintained: %s", out)
	}
	moved, err := k.ReadConcept("application-services/sample")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(moved.Body, "../services/peer.md") {
		t.Fatalf("outbound link not rebased: %s", moved.Body)
	}
	ref, err := k.ReadConcept("manutenzione/ref")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ref.Body, "[[application-services/sample]]") || !strings.Contains(ref.Body, "../application-services/sample.md") {
		t.Fatalf("inbound links not rewritten: %s", ref.Body)
	}
	if got := supersededBy(t, s, "manutenzione/old"); got != "application-services/sample" {
		t.Fatalf("superseded_by = %q", got)
	}
	idx, err := k.ReadIndex("application-services")
	if err != nil || !strings.Contains(idx, "sample.md") {
		t.Fatalf("destination index: %q %v", idx, err)
	}
	assertGone(t, k, "services/sample")

	// And back, without rewriting: links are left alone and the warning says so.
	out = callOK(t, s, "concept_move", `{"source_id":"application-services/sample","target_id":"services/sample","rewrite_links":false}`)
	if !strings.Contains(out, "inbound links to application-services/sample are not updated") {
		t.Fatalf("rewrite_links:false warning: %s", out)
	}
	ref, _ = k.ReadConcept("manutenzione/ref")
	if !strings.Contains(ref.Body, "[[application-services/sample]]") {
		t.Fatalf("rewrite_links:false rewrote links: %s", ref.Body)
	}
	assertGone(t, k, "application-services/sample")
	assertNoShadow(t, k)
}

// failingFS makes the late filesystem steps of concept_move fail on demand,
// without relying on host permissions (root and Windows ignore a read-only
// directory).
func failingFS(t *testing.T, removeErr, renameErr error) {
	t.Helper()
	origRemove, origRename := conceptMoveRemove, conceptMoveRename
	t.Cleanup(func() { conceptMoveRemove, conceptMoveRename = origRemove, origRename })
	if removeErr != nil {
		conceptMoveRemove = func(string) error { return removeErr }
	}
	if renameErr != nil {
		conceptMoveRename = func(string, string) error { return renameErr }
	}
}

func TestConceptMove_LateSourceRemovalFailureIsAnError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"remove-fails", errors.New("injected remove failure")},
		// Not-found after preflight read the source is unexpected, not success.
		{"source-vanished", &os.PathError{Op: "remove", Path: "x", Err: os.ErrNotExist}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k, s := namespaceServer(t)
			writeService(t, k, "services/sample", "svc\n")
			logBefore, _ := os.ReadFile(filepath.Join(k.DataRoot(), "log.md"))
			failingFS(t, tc.err, nil)

			res := callTool(t, s, "concept_move", `{"source_id":"services/sample","target_id":"application-services/sample"}`)
			if !res.IsError {
				t.Fatalf("late failure reported as success: %s", res.Content[0].Text)
			}
			msg := res.Content[0].Text
			for _, want := range []string{"services/sample", "application-services/sample", "already written"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("message %q lacks %q", msg, want)
				}
			}
			if strings.Contains(msg, "rolled back") && !strings.Contains(msg, "nothing was rolled back") {
				t.Fatalf("message claims a rollback: %q", msg)
			}
			if logAfter, _ := os.ReadFile(filepath.Join(k.DataRoot(), "log.md")); !bytes.Equal(logBefore, logAfter) {
				t.Fatalf("a failed move logged success:\n%s", logAfter)
			}
		})
	}
}

func TestConceptMove_LateExpandedRenameFailureIsAnError(t *testing.T) {
	k, s := namespaceServer(t)
	writeService(t, k, "services/owner", "svc\n")
	if err := k.ExpandConcept("services/owner"); err != nil {
		t.Fatal(err)
	}
	logBefore, _ := os.ReadFile(filepath.Join(k.DataRoot(), "log.md"))
	failingFS(t, nil, errors.New("injected rename failure"))

	res := callTool(t, s, "concept_move", `{"source_id":"services/owner","target_id":"application-services/owner"}`)
	if !res.IsError {
		t.Fatalf("late failure reported as success: %s", res.Content[0].Text)
	}
	if msg := res.Content[0].Text; !strings.Contains(msg, "services/owner") || !strings.Contains(msg, "application-services/owner") {
		t.Fatalf("message does not name both ids: %q", msg)
	}
	if logAfter, _ := os.ReadFile(filepath.Join(k.DataRoot(), "log.md")); !bytes.Equal(logBefore, logAfter) {
		t.Fatalf("a failed move logged success:\n%s", logAfter)
	}
	if _, err := k.ReadConcept("services/owner"); err != nil {
		t.Fatalf("source lost after a failed rename: %v", err)
	}
}
