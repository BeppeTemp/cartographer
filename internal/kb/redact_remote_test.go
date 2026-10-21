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
		{"https://host/r.git?access_token=abc&ref=main", "https://host/r.git?ref=main"},
		{"https://host/r.git?private_token=abc", "https://host/r.git"},
		{"https://host/r.git?API_KEY=abc&Password=x&a=b", "https://host/r.git?a=b"},
		{"ssh://git@host/r.git", "ssh://git@host/r.git"},
		{"ssh://git:pw@host/r.git", "ssh://git@host/r.git"},
		{"git@host:owner/r.git", "git@host:owner/r.git"},
		{"/srv/repos/r.git", "/srv/repos/r.git"},
		{"https://host/r.git", "https://host/r.git"},
	}
	for _, c := range cases {
		if got := RedactRemoteURL(c.in); got != c.want {
			t.Errorf("RedactRemoteURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
