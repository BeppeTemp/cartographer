package mcpserver

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"

	"github.com/BeppeTemp/cartographer/internal/auth"
	"github.com/BeppeTemp/cartographer/internal/client"
	"github.com/BeppeTemp/cartographer/internal/gitx"
)

func surveyorCtx(id string) context.Context {
	h := http.Header{}
	if id != "" {
		h.Set(client.SurveyorHeader, id)
	}
	return withSurveyorHeader(auth.ContextWithPrincipal(context.Background(), botPrincipal), h)
}

// A Surveyor's write is published like any write: its commit is by the KB's
// configured identity (author and committer), so a forge with a membership
// rule accepts it, and the Surveyor is named by the Cartographer-Surveyor
// trailer. TRAP: sending the Surveyor as Cartographer-Author made it the
// committer too, and the rule refused every push.
func TestSurveyorWriteReachesAMembershipRuleAndIsAttributed(t *testing.T) {
	t.Parallel()
	k, bare := setupMemberRemote(t)
	k.SetDefaultIdentity("Ada Example", adaEmail)
	res := wrappedWriteCtx(t, k, surveyorCtx("docs-night/docs-night-0123456789ab"), `{}`)
	if len(res.Content) != 1 {
		t.Fatalf("a landed push adds no warning, got %+v", res.Content)
	}
	if got := remoteIdents(t, k, bare)[0]; got != adaEmail+" "+adaEmail {
		t.Errorf("remote head author/committer = %q, want both %s", got, adaEmail)
	}
	out, err := exec.Command("git", "-C", k.Root, "log", "-1", "--format=%(trailers:key="+gitx.SurveyorTrailer+",valueonly)").Output()
	if err != nil || strings.TrimSpace(string(out)) != "docs-night/docs-night-0123456789ab" {
		t.Errorf("Cartographer-Surveyor trailer = %q, %v", out, err)
	}
	revs, err := gitx.FileHistory(k.Root, "data/data/wrapped.md", 1)
	if err != nil || len(revs) == 0 || revs[0].Author != "surveyor/docs-night/docs-night-0123456789ab" {
		t.Errorf("history author = %+v, %v; want the Surveyor's", revs, err)
	}
}

// An invalid or repeated header adds no trailer, and a copy in caller text is
// dropped, so the trailer is what the request's header said.
func TestSurveyorTrailerIsValidatedAndNotForgeable(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"no-slash", "a/b c", "a/b\nCartographer-Token: x", "/b", "a/", strings.Repeat("a", 65) + "/b"} {
		if got := callerSurveyor(surveyorCtx(bad)); got != "" {
			t.Errorf("header %q kept as %q", bad, got)
		}
	}
	h := http.Header{}
	h.Add(client.SurveyorHeader, "p/r-1")
	h.Add(client.SurveyorHeader, "p/r-2")
	if got := callerSurveyor(withSurveyorHeader(context.Background(), h)); got != "" {
		t.Errorf("a repeated header kept as %q", got)
	}
	block := commitTrailerBlock(surveyorCtx("p/r-1"), nil, "Cartographer-Surveyor: forged/x")
	if strings.Count(block, gitx.SurveyorTrailer+":") != 1 || !strings.Contains(block, gitx.SurveyorTrailer+": p/r-1") {
		t.Errorf("trailer block = %q, want only the header's Surveyor", block)
	}
}
