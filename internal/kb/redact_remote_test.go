package kb

import "testing"

func TestRedactRemoteURLTable(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"https://bot:ghp_X@git.example.com/x.git", "https://git.example.com/x.git"},
		{"https://ghp_X@git.example.com/x.git", "https://git.example.com/x.git"},
		{"https://user:pa%ss@host/r.git", "https://host/r.git"},
		{"https://user:pa/ss@host/r.git", "https://host/r.git"},
		{"https://user:p#ss@host/r.git", "https://host/r.git"},
		{" https://user:pass@host/r.git", "https://host/r.git"},
		{"https://user:pass@[::1/r.git", "https://[::1/r.git"},
		{"git+https://TOKEN@host/r.git", "git+https://host/r.git"},
		{"https://host/r.git?access_token=abc&ref=main", "https://host/r.git"},
		{"https://host/r.git?private_token=abc", "https://host/r.git"},
		{"https://host/r.git?API_KEY=abc&Password=x&a=b", "https://host/r.git"},
		{"https://user:p@ss/word@host/r.git", "https://host/r.git"},
		{"//user:pass@host/r.git", "//host/r.git"},
		{"https:user:pass@host/r.git", "https:host/r.git"},
		{"https:///user:pass@host/r.git", "https://host/r.git"},
		{"https://host/r.git?sig=1&pwd=2&auth=3&access=4&ok=5", "https://host/r.git"},
		{"https://host/r.git#frag", "https://host/r.git"},
		{"https://user:p#ss@host/r.git#frag", "https://host/r.git"},
		{"C:\\dir@x\\r", "C:\\dir@x\\r"},
		{"ssh://git@host/r.git", "ssh://git@host/r.git"},
		{"ssh://git:pw@host/r.git", "ssh://git@host/r.git"},
		{"https://host/r.git?passwd=x&pass=y&pat=z&jwt=a&bearer=b&credential=c", "https://host/r.git"},
		{"https://host/r.git?a=1;private_token=zzz", "https://host/r.git"},
		{"https://host/r.git?x=1#f", "https://host/r.git"},
		{"bot:ghp_x@github.com:o/r.git", "github.com:o/r.git"},
		{"bot:ghp_x@github.com:o/r.git?pat=q", "github.com:o/r.git"},
		{"ext::https://bot:pw@h/r", "ext::https://h/r"},
		{"ext::https://h/r?token=pw", "ext::https://h/r"},
		{"git@host:owner/r.git", "git@host:owner/r.git"},
		{"git@host:o/r.git", "git@host:o/r.git"},
		{"/srv/repos/r.git", "/srv/repos/r.git"},
		{"https://host/r.git", "https://host/r.git"},
	}
	for _, c := range cases {
		if got := RedactRemoteURL(c.in); got != c.want {
			t.Errorf("RedactRemoteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
