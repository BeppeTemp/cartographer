package lint

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/BeppeTemp/cartographer/internal/kb"
	"github.com/BeppeTemp/cartographer/internal/okf"
)

// FirstMachinePath returns the first client-local path in s as the machine_path
// check sees it (URLs excluded, no allow-prefixes), or "".
func FirstMachinePath(s string) string { return firstDisallowedMachinePath(s) }

// uncitedSourceFinding implements source_uncited (D278): a Source marked
// ingested that no concept cites through provenance was marked done while its
// content never reached the KB. cited is kb.SourceCitations of the whole KB.
func uncitedSourceFinding(relPath string, id okf.ConceptID, content string, cited map[string][]string) (Finding, bool) {
	fmRaw, _, _ := okf.SplitFrontmatter(content)
	fm, err := okf.ParseFrontmatter(fmRaw)
	if err != nil || !kb.IsSourceType(fm.Type()) {
		return Finding{}, false
	}
	if v, _ := fm.Get("ingest_status"); v != "ingested" || len(cited[string(id)]) > 0 {
		return Finding{}, false
	}
	return newFinding("source_uncited", Finding{
		Path:    relPath,
		Message: fmt.Sprintf("source %s is marked ingested but no concept lists it in provenance — cite it from the pages built on it, or set ingest_status back to pending", id),
	}), true
}

// secretQueryNames are the normalized (lowercase, no `-`/`_`) names of a query
// parameter that carries a credential. A name ending in token or secret matches
// too.
var secretQueryNames = map[string]bool{
	"password": true, "passwd": true, "pwd": true, "apikey": true, "accesskey": true,
	"secretkey": true, "privatekey": true, "credential": true, "credentials": true,
	"auth": true, "authorization": true, "sig": true, "signature": true,
}

// URLCredential reports why s, read as a URL, carries a credential, or "" when
// it does not (D1201): any userinfo (a token is often the username), or a query
// parameter named like a secret. Only an absolute URL (scheme and host) is
// judged; anything else is not a URL. The value is never echoed.
func URLCredential(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	if u.User != nil {
		return "userinfo"
	}
	for name := range u.Query() {
		n := strings.NewReplacer("-", "", "_", "").Replace(strings.ToLower(name))
		if secretQueryNames[n] || strings.HasSuffix(n, "token") || strings.HasSuffix(n, "secret") {
			return "query parameter " + name
		}
	}
	return ""
}
