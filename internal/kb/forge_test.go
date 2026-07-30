package kb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestGitHubForgeCreatesLabeledPR(t *testing.T) {
	var sawCreate, sawLabel bool
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch r.URL.Path {
		case "/repos/acme/wiki/pulls":
			sawCreate = r.Method == http.MethodPost
			_, _ = w.Write([]byte(`{"number":7,"html_url":"https://example/pr/7","state":"open","head":{"sha":"abc"}}`))
		case "/repos/acme/wiki/issues/7/labels":
			sawLabel = r.Method == http.MethodPost
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer s.Close()
	g := &GitHubForge{APIURL: s.URL, Token: "secret", Client: s.Client()}
	pr, err := g.CreatePR(context.Background(), "acme", "wiki", "cartographer/kb", "main", "title", "body")
	if err != nil {
		t.Fatal(err)
	}
	if pr.Number != 7 || !sawCreate || !sawLabel {
		t.Fatalf("pr=%#v create=%v label=%v", pr, sawCreate, sawLabel)
	}
}

func TestGitHubForgePaginatesOpenPRsAndUsesScopedQuery(t *testing.T) {
	var pages []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pages = append(pages, r.URL.RawQuery)
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if page == 1 {
			prs := make([]string, 100)
			for i := range prs {
				prs[i] = fmt.Sprintf(`{"number":%d,"state":"open","head":{"sha":"h%d"}}`, i+1, i+1)
			}
			_, _ = w.Write([]byte("[" + strings.Join(prs, ",") + "]"))
			return
		}
		_, _ = w.Write([]byte(`[{"number":101,"state":"open","head":{"sha":"h101"}}]`))
	}))
	defer s.Close()
	prs, err := (&GitHubForge{APIURL: s.URL, Client: s.Client()}).FindOpenPR(context.Background(), "acme", "wiki", "work", "main")
	if err != nil || len(prs) != 101 {
		t.Fatalf("FindOpenPR = %d, %v", len(prs), err)
	}
	if len(pages) != 2 || !strings.Contains(pages[0], "state=open") || !strings.Contains(pages[0], "head=acme%3Awork") || !strings.Contains(pages[0], "base=main") {
		t.Fatalf("unexpected pagination queries: %q", pages)
	}
}

func TestGitHubForgeReadyChecksLatestReviewsAndChecks(t *testing.T) {
	for _, tc := range []struct {
		name, reviews, checks string
		want                  bool
	}{
		{"approval success", `[{"state":"APPROVED","user":{"login":"a"}}]`, `{"total_count":1,"check_runs":[{"status":"completed","conclusion":"success"}]}`, true},
		{"latest request changes", `[{"state":"CHANGES_REQUESTED","user":{"login":"a"}},{"state":"APPROVED","user":{"login":"a"}}]`, `{"total_count":0}`, false},
		{"pending check", `[{"state":"APPROVED","user":{"login":"a"}}]`, `{"total_count":1,"check_runs":[{"status":"in_progress","conclusion":""}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.Contains(r.URL.Path, "/reviews"):
					_, _ = w.Write([]byte(tc.reviews))
				case strings.Contains(r.URL.Path, "/check-runs"):
					_, _ = w.Write([]byte(tc.checks))
				case strings.Contains(r.URL.Path, "/pulls/1"):
					_, _ = w.Write([]byte(`{"number":1,"state":"open","head":{"sha":"head"}}`))
				default:
					t.Fatalf("unexpected request %s", r.URL.String())
				}
			}))
			defer s.Close()
			got, err := (&GitHubForge{APIURL: s.URL, Client: s.Client()}).PRReady(context.Background(), "a", "b", 1)
			if err != nil || got != tc.want {
				t.Fatalf("PRReady = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestGitHubForgeMergeEndpointAndErrors(t *testing.T) {
	var mergeBody string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/a/b/pulls/9/merge" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		mergeBody = string(body)
		_, _ = w.Write([]byte(`{"merged":true,"sha":"merge-sha"}`))
	}))
	defer s.Close()
	got, err := (&GitHubForge{APIURL: s.URL, Client: s.Client()}).MergeSquash(context.Background(), "a", "b", 9, "head-sha")
	if err != nil || !got.Merged || got.SHA != "merge-sha" || !strings.Contains(mergeBody, `"merge_method":"squash"`) || !strings.Contains(mergeBody, `"sha":"head-sha"`) {
		t.Fatalf("MergeSquash = %#v, %v; body=%s", got, err, mergeBody)
	}
}

func TestGitHubForgeNeverUpdatesRefsOrBase(t *testing.T) {
	var requests []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if strings.Contains(r.URL.Path, "/git/refs") || strings.Contains(r.URL.Path, "/branches/") {
			t.Fatalf("forge attempted protected-ref endpoint: %s %s", r.Method, r.URL.Path)
		}
		switch {
		case r.URL.Path == "/repos/a/b/pulls" && r.Method == http.MethodGet:
			_, _ = w.Write([]byte(`[]`))
		case r.URL.Path == "/repos/a/b/pulls" && r.Method == http.MethodPost:
			_, _ = w.Write([]byte(`{"number":1,"state":"open","head":{"sha":"head"}}`))
		case r.URL.Path == "/repos/a/b/issues/1/labels":
			w.WriteHeader(http.StatusOK)
		case strings.Contains(r.URL.Path, "/reviews"):
			_, _ = w.Write([]byte(`[{"state":"APPROVED","user":{"login":"r"}}]`))
		case r.URL.Path == "/repos/a/b/pulls/1":
			_, _ = w.Write([]byte(`{"number":1,"state":"open","head":{"sha":"head"}}`))
		case strings.Contains(r.URL.Path, "/check-runs"):
			_, _ = w.Write([]byte(`{"total_count":0}`))
		case r.URL.Path == "/repos/a/b/pulls/1/merge":
			_, _ = w.Write([]byte(`{"merged":true,"sha":"merge"}`))
		default:
			t.Fatalf("unexpected forge request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer s.Close()
	g := &GitHubForge{APIURL: s.URL, Client: s.Client()}
	if _, err := g.FindOpenPR(context.Background(), "a", "b", "work", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreatePR(context.Background(), "a", "b", "work", "main", "t", "b"); err != nil {
		t.Fatal(err)
	}
	if ok, err := g.PRReady(context.Background(), "a", "b", 1); err != nil || !ok {
		t.Fatalf("PRReady=%v, %v", ok, err)
	}
	if _, err := g.MergeSquash(context.Background(), "a", "b", 1, "head"); err != nil {
		t.Fatal(err)
	}
	if len(requests) == 0 {
		t.Fatal("expected forge requests")
	}
}

func TestGitHubForgeRedactsOnlyNonEmptyToken(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("token secret failed"))
	}))
	defer s.Close()
	_, err := (&GitHubForge{APIURL: s.URL, Token: "secret", Client: s.Client()}).FindOpenPR(context.Background(), "a", "b", "h", "base")
	if err == nil || contains(err.Error(), "secret") {
		t.Fatalf("error was not redacted: %v", err)
	}
	_, err = (&GitHubForge{APIURL: s.URL, Client: s.Client()}).FindOpenPR(context.Background(), "a", "b", "h", "base")
	if err == nil || !contains(err.Error(), "token secret failed") {
		t.Fatalf("empty token corrupted error: %v", err)
	}
}

func TestGitHubForgeReadyUsesLatestReview(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case contains(r.URL.Path, "/reviews"):
			_, _ = w.Write([]byte(`[{"state":"CHANGES_REQUESTED","user":{"login":"reviewer"}},{"state":"APPROVED","user":{"login":"reviewer"}}]`))
		case contains(r.URL.Path, "/pulls/1"):
			_, _ = w.Write([]byte(`{"number":1,"state":"open","head":{"sha":"head"}}`))
		default:
			t.Fatalf("unexpected request %s", r.URL.String())
		}
	}))
	defer s.Close()
	ready, err := (&GitHubForge{APIURL: s.URL, Client: s.Client()}).PRReady(context.Background(), "a", "b", 1)
	if err != nil || ready {
		t.Fatalf("ready=%v err=%v", ready, err)
	}
}

func contains(s, part string) bool { return strings.Contains(s, part) }
