package mcpserver

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
)

// A KB with a known number of findings: n pages, each with one broken link.
func lintPageHandler(t *testing.T, n int) http.Handler {
	t.Helper()
	k := uiFixtureKB(t, "docs")
	for i := 0; i < n; i++ {
		page := fmt.Sprintf("---\ntype: Note\ntitle: P%d\n---\n[gone](missing%d.md)\n", i, i)
		if err := os.WriteFile(filepath.Join(k.DataRoot(), "visible", fmt.Sprintf("p%02d.md", i)), []byte(page), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	multi := NewMultiKBServer("test")
	multi.MountKB("docs", func(s *Server) { RegisterKBTools(s, k, Deps{}) })
	multi.EnableWeb(nil)
	return auth.NewTokenStore(nil).Middleware(multi.Handler())
}

func findingKeys(t *testing.T, body map[string]interface{}) []string {
	t.Helper()
	var keys []string
	for _, f := range body["findings"].([]interface{}) {
		row := f.(map[string]interface{})
		keys = append(keys, fmt.Sprint(row["check"], "|", row["path"], "|", row["message"]))
	}
	return keys
}

// Lint is a page at a time: the pages concatenate to the whole ordered list,
// `count` is the total whatever the page, and the last page has no cursor.
func TestUIAPI_LintPaginates(t *testing.T) {
	t.Parallel()
	handler := lintPageHandler(t, 7)
	url := UIAPIPrefix + "/kbs/docs/lint"
	whole := decodeUI(t, getUI(t, handler, url, ""))
	all := findingKeys(t, whole)
	if len(all) < 7 || whole["next_cursor"] != nil || whole["count"].(float64) != float64(len(all)) {
		t.Fatalf("unpaged: %d findings, count %v, cursor %v", len(all), whole["count"], whole["next_cursor"])
	}

	var got []string
	cursor, pages := "", 0
	for {
		q := url + "?limit=3"
		if cursor != "" {
			q += "&cursor=" + cursor
		}
		page := decodeUI(t, getUI(t, handler, q, ""))
		if page["count"].(float64) != float64(len(all)) || !reflect.DeepEqual(page["by_check"], whole["by_check"]) {
			t.Fatalf("a page lost the totals: %v", page)
		}
		keys := findingKeys(t, page)
		if len(keys) > 3 {
			t.Fatalf("page of %d with limit 3", len(keys))
		}
		got = append(got, keys...)
		pages++
		next, more := page["next_cursor"].(string)
		if !more {
			break
		}
		if pages > len(all) {
			t.Fatal("the cursor never ends")
		}
		cursor = next
	}
	if !reflect.DeepEqual(got, all) {
		t.Errorf("pages differ from the whole list:\n%v\n%v", got, all)
	}
	if want := (len(all) + 2) / 3; pages != want {
		t.Errorf("%d pages, want %d", pages, want)
	}
}

func TestUIAPI_LintRefusesABadCursorAndEndsOnAStaleOne(t *testing.T) {
	t.Parallel()
	handler := lintPageHandler(t, 2)
	url := UIAPIPrefix + "/kbs/docs/lint"
	for _, q := range []string{"?cursor=abc", "?cursor=-1", "?limit=0", "?limit=x"} {
		if rr := getUI(t, handler, url+q, ""); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", q, rr.Code)
		}
	}
	// A list that shrank since the cursor was handed out: an empty last page.
	page := decodeUI(t, getUI(t, handler, url+"?cursor=99999", ""))
	if len(page["findings"].([]interface{})) != 0 || page["next_cursor"] != nil {
		t.Errorf("stale cursor: %v", page)
	}
}
