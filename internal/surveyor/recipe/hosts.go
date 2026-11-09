package recipe

import (
	"net"
	"net/url"
	"strings"
)

// HostAllowed reports whether host is in the allow list: an exact host
// (lower-cased, port ignored) or "*.example.com" for any subdomain of it. An IP
// literal matches only when listed verbatim. An empty list allows nothing.
func HostAllowed(host string, list []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return false
	}
	isIP := net.ParseIP(host) != nil
	for _, a := range list {
		a = strings.ToLower(strings.TrimSpace(a))
		switch {
		case a == "":
		case a == host:
			return true
		case !isIP && strings.HasPrefix(a, "*.") && strings.HasSuffix(host, a[1:]) && len(host) > len(a)-1:
			return true
		}
	}
	return false
}

// ValidHostPattern reports whether an allow_hosts entry is well formed: a bare
// host or "*." plus a host, no scheme, port, path or credential.
func ValidHostPattern(p string) bool {
	if net.ParseIP(strings.Trim(p, "[]")) != nil {
		return !strings.HasPrefix(p, "*.")
	}
	h := strings.TrimPrefix(p, "*.")
	return h != "" && !strings.ContainsAny(h, "*/@?#\\ \t:")
}

// httpsRemote returns the clean https URL of a repository locator, or the
// error that refuses it before anything is fetched. Only https remotes without
// userinfo whose host is listed pass: file://, git://, ssh, scp-like and local
// paths are refused, so is a locator a KB writer aimed at the internal network.
func httpsRemote(locator string, hosts []string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(locator))
	switch {
	case err != nil || u.Scheme != "https" || u.Host == "" || u.Hostname() == "":
		return nil, fail(CodeBadLocator, "the locator must be an https repository URL")
	case u.User != nil:
		return nil, fail(CodeBadLocator, "a repository URL with credentials is refused: use an exec recipe or an MCP connector")
	case u.RawQuery != "" || u.Fragment != "":
		return nil, fail(CodeBadLocator, "the repository URL has no query or fragment")
	case !HostAllowed(u.Hostname(), hosts):
		return nil, fail(CodeHostNotAllowed, "%s is not in git.allow_hosts", strings.ToLower(u.Hostname()))
	}
	return u, nil
}
