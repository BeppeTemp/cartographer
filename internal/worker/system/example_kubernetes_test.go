package system

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BeppeTemp/cartographer/internal/worker/recipe"
)

// The reference lister of examples/system-recipes/kubernetes, run through the
// real exec runner with fixtures in place of kubectl. Skipped where no Python 3
// is found (CI also runs on Windows).

var exampleDir = filepath.Join("..", "..", "..", "examples", "system-recipes", "kubernetes")

func python(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if out, err := exec.Command(p, "-c", "import sys; print(sys.version_info[0])").Output(); err == nil && strings.TrimSpace(string(out)) == "3" {
			return p
		}
	}
	t.Skip("no Python 3 interpreter in PATH: the example lister is not run")
	return ""
}

// fixtures copies testdata/view to a temp directory the test may edit.
func fixtures(t *testing.T) string {
	t.Helper()
	dst := t.TempDir()
	src := filepath.Join(exampleDir, "testdata", "view")
	entries, err := os.ReadDir(src)
	if err != nil || len(entries) == 0 {
		t.Fatalf("fixtures: %v", err)
	}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dst
}

func edit(t *testing.T, dir, file, from, to string) {
	t.Helper()
	p := filepath.Join(dir, file)
	b, err := os.ReadFile(p)
	if err != nil || !strings.Contains(string(b), from) {
		t.Fatalf("edit %s: %v", file, err)
	}
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(string(b), from, to)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func lister(t *testing.T, fix string) recipe.SystemRecipe {
	t.Helper()
	t.Setenv("LISTER_FIXTURES_DIR", fix)
	script, err := filepath.Abs(filepath.Join(exampleDir, "lister.py")) // the child runs in a temp directory
	if err != nil {
		t.Fatal(err)
	}
	return recipe.NewExec(recipe.ExecConfig{Name: "k8s-dev", Shape: recipe.ShapeSystem, Command: python(t),
		Args: []string{script}, Env: map[string]string{"LISTER_FIXTURES_DIR": "LISTER_FIXTURES_DIR"},
		Timeout: time.Minute, MaxOutput: 1 << 20, MaxContent: 64 << 10, Instructions: "x"}).(recipe.SystemRecipe)
}

func list(t *testing.T, r recipe.SystemRecipe, req recipe.Request) (recipe.Listing, error) {
	t.Helper()
	v, err := r.Preflight(context.Background(), req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	return r.ListSystem(context.Background(), v, req)
}

func TestExampleKubernetesPreflight(t *testing.T) {
	fix := fixtures(t)
	r := lister(t, fix)
	v, err := r.Preflight(context.Background(), recipe.Request{ID: "s/k"})
	if err != nil || v.Identity() != "dev-viewer" || !strings.HasPrefix(v.Detail(), "checked: ") {
		t.Fatalf("view identity: %+v %v", v, err)
	}
	for name, tc := range map[string]struct{ file, to, want string }{
		"cluster-admin":      {"auth_can-i_all_all.txt", "yes\n", "can-i * * answered yes"},
		"reads secrets":      {"auth_can-i_get_secrets.txt", "yes\n", "can-i get secrets"},
		"can patch":          {"auth_can-i_patch_deployments.apps.txt", "yes\n", "can-i patch deployments.apps"},
		"lists secrets":      {"auth_can-i_list_secrets.txt", "yes\n", "can-i list secrets answered yes"},
		"watches secrets":    {"auth_can-i_watch_secrets.txt", "yes\n", "can-i watch secrets answered yes"},
		"execs into pods":    {"auth_can-i_create_pods-exec.txt", "yes\n", "can-i create pods/exec answered yes"},
		"port-forwards":      {"auth_can-i_create_pods-portforward.txt", "yes\n", "can-i create pods/portforward answered yes"},
		"writes services":    {"auth_can-i_create_services.txt", "yes\n", "can-i create services answered yes"},
		"writes sets":        {"auth_can-i_delete_statefulsets.apps.txt", "yes\n", "can-i delete statefulsets.apps answered yes"},
		"cannot list a read": {"auth_can-i_list_services.txt", "no\n", "can-i list services answered no"},
		"reads nothing":      {"auth_can-i_list_namespaces.txt", "no\n", "can-i list namespaces answered no"},
		"not yes or no":      {"auth_can-i_delete_pods.txt", "maybe\n", "exit 2"},
		"a failing call":     {"auth_can-i_create_pods.txt", "", "exit 2"},
	} {
		d := fixtures(t)
		if tc.file == "auth_can-i_create_pods.txt" {
			os.Remove(filepath.Join(d, tc.file)) // kubectl could not run
		} else {
			os.WriteFile(filepath.Join(d, tc.file), []byte(tc.to), 0o600)
		}
		_, err := lister(t, d).Preflight(context.Background(), recipe.Request{ID: "s/k"})
		e, ok := err.(*recipe.Error)
		if !ok || e.Code != recipe.CodeIdentityNotReadOnly || !strings.Contains(e.Detail, tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
	// The cluster-admin refusal still names the identity.
	d := fixtures(t)
	edit(t, d, "auth_can-i_all_all.txt", "no", "yes")
	if _, err := lister(t, d).Preflight(context.Background(), recipe.Request{}); err == nil || !strings.Contains(err.Error(), "dev-viewer") {
		t.Errorf("admin: %v", err)
	}
}

func TestExampleKubernetesListingIsStableAndLeaksNothing(t *testing.T) {
	d := fixtures(t)
	r := lister(t, d)
	l1, err := list(t, r, recipe.Request{ID: "s/k"})
	if err != nil || len(l1.Entries) != 2 || l1.Entries[0].Key != "namespace/team-a" {
		t.Fatalf("listing = %+v %v", l1, err)
	}
	l2, _ := list(t, r, recipe.Request{ID: "s/k"})
	g1, err1 := ValidateListing(l1.Entries, 64<<10)
	g2, err2 := ValidateListing(l2.Entries, 64<<10)
	if err1 != nil || err2 != nil || g1[0] != g2[0] || g1[1] != g2[1] {
		t.Fatal("two listings differ")
	}
	var all strings.Builder
	for _, e := range l1.Entries {
		all.WriteString(e.Key + e.Title + e.Content)
	}
	for _, leak := range []string{"CANARY", "Q0FOQVJZ", "credentials", "DB_PASSWORD", "resourceVersion", "uid-", "2031-01-02", "managedFields", "readyReplicas"} {
		if strings.Contains(all.String(), leak) {
			t.Errorf("the listing holds %q", leak)
		}
	}
	for _, want := range []string{"Deployment api replicas=3 containers=web:registry.example.com/team-a/api:1.4.2 ports=8080,8443", "Ingress api hosts=api.example.com", "ConfigMap settings", "PersistentVolumeClaim data class=fast size=20Gi"} {
		if !strings.Contains(l1.Entries[0].Content, want) {
			t.Errorf("team-a lacks %q", want)
		}
	}
	// Volatile fields move and no hash changes.
	edit(t, d, "get_deployments.apps,statefulsets.apps,daemonsets.apps,cronjobs.batch,services,ingresses.networking.k8s.io,persistentvolumeclaims,configmaps_-n_team-a_-o_json.json", `"resourceVersion": "555"`, `"resourceVersion": "999"`)
	edit(t, d, "get_deployments.apps,statefulsets.apps,daemonsets.apps,cronjobs.batch,services,ingresses.networking.k8s.io,persistentvolumeclaims,configmaps_-n_team-a_-o_json.json", `"readyReplicas": 3`, `"readyReplicas": 1`)
	edit(t, d, "get_deployments.apps,statefulsets.apps,daemonsets.apps,cronjobs.batch,services,ingresses.networking.k8s.io,persistentvolumeclaims,configmaps_-n_team-a_-o_json.json", "2031-01-02T03:04:05Z", "2031-03-04T05:06:07Z")
	l3, _ := list(t, lister(t, d), recipe.Request{ID: "s/k"})
	g3, _ := ValidateListing(l3.Entries, 64<<10)
	if g3[0].Revision != g1[0].Revision || g3[1].Revision != g1[1].Revision {
		t.Error("a volatile field changed a revision")
	}
}

// raw runs the lister directly so the bytes can be compared.
func raw(t *testing.T, fix, request string) []byte {
	t.Helper()
	cmd := exec.Command(python(t), filepath.Join(exampleDir, "lister.py"))
	cmd.Env = append(os.Environ(), "LISTER_FIXTURES_DIR="+fix)
	cmd.Stdin = strings.NewReader(request)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("lister: %v", err)
	}
	if bytes.Contains(out.Bytes(), []byte("\r")) {
		t.Error("CR in the output")
	}
	return out.Bytes()
}

func TestExampleKubernetesNeverUsesTheLocator(t *testing.T) {
	d := fixtures(t) // only the files the pinned commands name: any other command fails on a missing fixture
	neutral := raw(t, d, `{"protocol":1,"mode":"list","source":{"id":"s/k","locator":"kubernetes://dev-cluster","include":[]},"max_content_bytes":65536}`)
	hostile := raw(t, d, `{"protocol":1,"mode":"list","source":{"id":"s/k","locator":"kubernetes://other-context","include":[]},"max_content_bytes":65536}`)
	if !bytes.Equal(neutral, hostile) || len(neutral) == 0 {
		t.Error("the locator changed the output")
	}
	pf1 := raw(t, d, `{"mode":"preflight","source":{"locator":"kubernetes://a"}}`)
	pf2 := raw(t, d, `{"mode":"preflight","source":{"locator":"--context=evil"}}`)
	if !bytes.Equal(pf1, pf2) {
		t.Error("the locator changed the preflight")
	}
	// include filters on the key; another mode fails.
	inc := raw(t, d, `{"mode":"list","source":{"include":["namespace/team-b"]},"max_content_bytes":65536}`)
	if strings.Contains(string(inc), "team-a") || !strings.Contains(string(inc), "team-b") {
		t.Errorf("include: %s", inc)
	}
	cmd := exec.Command(python(t), filepath.Join(exampleDir, "lister.py"))
	cmd.Stdin = strings.NewReader(`{"mode":"delete"}`)
	if err := cmd.Run(); err == nil {
		t.Error("an unknown mode exits zero")
	}
}

func TestExampleKubernetesNamespaceOverTheCapFailsByName(t *testing.T) {
	r := lister(t, fixtures(t))
	v, err := r.Preflight(context.Background(), recipe.Request{ID: "s/k"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.ListSystem(context.Background(), v, recipe.Request{ID: "s/k", MaxContentBytes: 100})
	if e, ok := err.(*recipe.Error); !ok || e.Code != recipe.CodeListFailed || !strings.Contains(e.Detail, "namespace team-a is over max_content_bytes") {
		t.Errorf("err = %v", err)
	}
}
