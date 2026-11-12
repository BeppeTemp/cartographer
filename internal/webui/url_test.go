package webui

import "testing"

func TestURL(t *testing.T) {
	cases := map[string]string{
		"":                  "",
		":39273":            "http://127.0.0.1:39273/ui/",
		"127.0.0.1:39273":   "http://127.0.0.1:39273/ui/",
		"0.0.0.0:9090":      "http://127.0.0.1:9090/ui/",
		"[::]:9090":         "http://127.0.0.1:9090/ui/",
		"10.0.0.5:9090":     "http://10.0.0.5:9090/ui/",
		"not-a-valid-value": "",
	}
	for addr, want := range cases {
		if got := URL(addr); got != want {
			t.Errorf("URL(%q) = %q, want %q", addr, got, want)
		}
	}
}

// #1945: kb_status reports the Atlas address only for a loopback or wildcard
// bind; a specific other address would tell every caller where the server
// listens.
func TestLoopbackURL(t *testing.T) {
	cases := map[string]string{
		"":                        "",
		"not-a-valid-value":       "",
		":39273":                  "http://127.0.0.1:39273/ui/",
		"0.0.0.0:9090":            "http://127.0.0.1:9090/ui/",
		"[::]:9090":               "http://127.0.0.1:9090/ui/",
		"[::0]:9090":              "http://127.0.0.1:9090/ui/",
		"127.0.0.1:9090":          "http://127.0.0.1:9090/ui/",
		"127.0.0.2:9090":          "http://127.0.0.2:9090/ui/",
		"[::1]:9090":              "http://[::1]:9090/ui/",
		"localhost:9090":          "http://localhost:9090/ui/",
		"localhost.:9090":         "http://localhost.:9090/ui/",
		"LOCALHOST:9090":          "http://LOCALHOST:9090/ui/",
		"127.0.0.1.example.com:1": "",
		"10.0.0.5:9090":           "",
		"192.0.2.7:9090":          "",
		"[2001:db8::5]:9090":      "",
		"[fe80::1%en0]:9090":      "",
		"server.example.com:9090": "",
	}
	for addr, want := range cases {
		if got := LoopbackURL(addr); got != want {
			t.Errorf("LoopbackURL(%q) = %q, want %q", addr, got, want)
		}
	}
}

func TestKBURL(t *testing.T) {
	if got, want := KBURL("http://127.0.0.1:39273/ui/", "kb a&b"), "http://127.0.0.1:39273/ui/?kb=kb+a%26b"; got != want {
		t.Errorf("KBURL = %q, want %q", got, want)
	}
}
