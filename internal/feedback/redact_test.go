package feedback

import (
	"strings"
	"testing"
)

// Tokens are assembled at runtime (D259, D1202): never a literal in source.
func tok() string { return "ghp_" + strings.Repeat("a1B2", 9) }

// known is the fixture: placeholder-style values only (D259).
func known() Known {
	return Known{
		KBNames:     []string{"team-kb", "docs-kb"},
		ServerHosts: []string{"meridian.example.com:8443", "git.example.net"},
		User:        "alice",
		Home:        "/home/alice",
		Workspaces:  []string{"/home/alice/work/app", "/home/alice/work"},
		ConceptIDs:  []string{"ops/runbook-db", "dev/design-notes"},
	}
}

func TestRedactClasses(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, in, want string }{
		{"secretscan", "token " + tok() + " end", "token [removed] end"},
		{"jwt", "got eyJhbGciOi.eyJzdWIiOiIx.c2ln here", "got [removed] here"},
		{"bearer", "got Bearer abcdEFGH1234", "got Bearer [removed]"},
		{"authorization header", "Authorization: Bearer abcdEFGH1234", "Authorization: [removed]"},
		{"authorization token", "Authorization: token abcd", "Authorization: [removed]"},
		{"password key", "password: hunter2hunter2 ok", "password: [removed]"},
		{"password phrase", "password: correct horse battery", "password: [removed]"},
		{"password quoted phrase", "password = 'my secret phrase here' next", "password = '[removed]' next"},
		{"url userinfo host", "https://user:pw@git.company.es/x", "https://example.com"},
		{"url single label", "https://user:pw@nas/x", "https://example.com"},
		{"url ipv6", "https://[fd00::5]:8080/x", "https://example.com"},
		{"url ipv6 userinfo", "http://u:p@[fd00::5]/x", "http://example.com"},
		{"country hosts", "nas.se and gitlab.corp.ru, svc_a.corp.lan", "example.com and example.org, example.net"},
		{"file names kept", "see main.go, config.yaml and v1.2.3", "see main.go, config.yaml and v1.2.3"},
		{"quoted env", `API_TOKEN="abc"`, `API_TOKEN="[removed]"`},
		{"email", "mail alice@corp.example.com now", "mail user@example.com now"},
		{"scp remote", "git clone git@gitlab.corp.example.com:team/repo.git", "git clone user@example.com:path"},
		{"bare fqdn", "ping db1.corp.example.org and main.go", "ping example.com and main.go"},
		{"allowed host", "docs at beppetemp.github.io and github.com", "docs at beppetemp.github.io and github.com"},
		{"own url with userinfo", "https://alice@github.com/" + Repo + "/issues", "https://example.com"},
		{"own url with port", "https://github.com:444/" + Repo, "https://example.com"},
		{"own pages lookalike", "https://beppetemp.github.io/cartographer-evil/x", "https://example.com"},
		{"own pages", "https://beppetemp.github.io/cartographer/x", "https://beppetemp.github.io/cartographer/x"},
		{"long run", "id " + strings.Repeat("0a", 20) + " x", "id [removed] x"},
		{"long run letters only is kept", "id " + strings.Repeat("a", 40), "id " + strings.Repeat("a", 40)},
		{"userinfo", "ssh bob:hunter2@host", "ssh [removed]@host"},
		{"query", "GET /x?api_key=abc123&page=2", "GET /x?api_key=[removed]&page=2"},
		{"url", "see https://other.example.org/a/b?q=1#f", "see https://example.com"},
		{"own url kept", "see https://github.com/" + Repo + "/issues/1", "see https://github.com/" + Repo + "/issues/1"},
		{"own url case", "see https://github.com/beppetemp/cartographer/issues", "see https://github.com/beppetemp/cartographer/issues"},
		{"lookalike repo", "https://github.com/" + Repo + "-fork/x", "https://example.com"},
		{"server host bare", "dial meridian.example.com:8443 and git.example.net.", "dial example.com and example.org."},
		{"two hosts distinct", "https://a.example.org/x https://b.example.org/y https://a.example.org/z", "https://example.com https://example.org https://example.com"},
		{"ipv4", "to 10.0.0.7 failed", "to example.com failed"},
		{"workspace", "in /home/alice/work/app/cmd and /home/alice/work/x", "in $HOME/project/cmd and $HOME/project-2/x"},
		{"home", "cfg /home/alice/.config/x", "cfg $HOME/.config/x"},
		{"windows home", `C:\Users\Alice\x`, `C:\Users\Alice\x`},
		{"user token", "hello alice, alice@host", "hello user, user@host"},
		{"user is a prefix only", "alicia", "alicia"},
		{"concept id", "see [[ops/runbook-db]] and dev/design-notes.md and ops/runbook-db", "see [[area/page-1]] and area/page-2 and area/page-1"},
		{"kb names", "in team-kb and docs-kb, not team-kb-2", "in kb-a and kb-b, not team-kb-2"},
	} {
		k := known()
		if c.name == "windows home" {
			k.Home = `C:\Users\Alice`
			c.want = `$HOME\x`
		}
		got, _ := Redact(c.in, k)
		if got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestRedactShortUser(t *testing.T) {
	t.Parallel()
	k := Known{User: "al"}
	got, _ := Redact("al@host, /home/al/x, C:\\Users\\al\\x, also al and all", k)
	if want := "user@host, /home/user/x, C:\\Users\\user\\x, also al and all"; got != want {
		t.Errorf("got %q", got)
	}
}

func TestRedactIdempotentAndSummary(t *testing.T) {
	t.Parallel()
	in := strings.Join([]string{
		"team-kb at meridian.example.com:8443 by alice in /home/alice/work/app", "https://other.example.org/p?token=zz9",
		"[[ops/runbook-db]] ops/runbook-db.md", tok(), "Bearer abcdEFGH1234", "10.1.2.3", "bob:pw@host", "bob@corp.example.com git@git2.example.net:t/r.git db1.corp.example.org", "password: x1x1x1 Authorization: token abcd",
	}, "\n")
	once, sum := Redact(in, known())
	twice, sum2 := Redact(once, known())
	if once != twice {
		t.Errorf("not idempotent:\n%q\n%q", once, twice)
	}
	if sum.Total() == 0 || sum2.Total() != 0 || sum.Secrets < 4 || sum.KBNames != 1 || sum.ConceptIDs != 2 || sum.Users != 1 {
		t.Errorf("summary %+v then %+v", sum, sum2)
	}
}

// A draft that cites every known value in several shapes must come out with
// none of them.
func TestRedactNoKnownValueSurvives(t *testing.T) {
	t.Parallel()
	k := known()
	values := []string{"team-kb", "docs-kb", "meridian.example.com:8443", "git.example.net", "alice", "/home/alice/work/app", "ops/runbook-db", "dev/design-notes"}
	for _, shape := range []string{
		"plain %s end", "https://%s/x", `"%s"`, "[link](%s)", "[[%s]]", "(%s)", "%s.md", "path:%s;", "x=%s,", "`%s`", "%s:", "/%s/", "**%s**",
	} {
		for _, v := range values {
			in := strings.ReplaceAll(shape, "%s", v)
			out, _ := Redact(in, k)
			if strings.Contains(strings.ToLower(out), strings.ToLower(v)) {
				t.Errorf("%q -> %q still holds %q", in, out, v)
			}
		}
	}
}

func TestFixtureUsesPlaceholdersOnly(t *testing.T) {
	t.Parallel()
	k := known()
	for _, h := range k.ServerHosts {
		if host := hostOnly(h); !strings.HasSuffix(host, ".example.com") && !strings.HasSuffix(host, ".example.net") {
			t.Errorf("fixture host %q is not a placeholder", h)
		}
	}
	if k.User != "alice" || !strings.HasPrefix(k.Home, "/home/alice") {
		t.Errorf("fixture user/home %q %q", k.User, k.Home)
	}
}

func TestRedactSecretKindsInEitherOrder(t *testing.T) {
	t.Parallel()
	gh := tok()
	aws := "AKIA" + strings.Repeat("AB23", 4)
	sk := "sk-" + strings.Repeat("aB3d", 12)
	pem := "-----BEGIN RSA PRIVATE KEY-----\n" + strings.Repeat("MIIEowIBAAKCAQEA", 2) + "\n-----END RSA PRIVATE KEY-----"
	for _, in := range []string{
		aws + " " + gh, gh + " " + aws, sk + " " + gh, gh + " " + sk, gh + "," + aws + "," + sk,
		pem + "\n" + gh, gh + "\n" + pem + "\n" + aws, "x " + gh + " " + pem,
	} {
		out, sum := Redact(in, Known{})
		for _, secret := range []string{gh, aws, sk, "MIIEow"} {
			if strings.Contains(out, secret) {
				t.Errorf("%q leaks %q", in, secret[:6])
			}
		}
		if sum.Secrets == 0 || strings.Contains(out, "PRIVATE KEY") && strings.Contains(in, "MIIEow") && strings.Contains(out, "AKIA") {
			t.Errorf("%q -> %q (%+v)", in, out, sum)
		}
	}
}
