package starcore

import (
	"net/netip"
	"testing"
)

func TestNetMatch(t *testing.T) {
	for _, c := range []struct {
		host string
		pats []string
		want bool
	}{
		{"api.example.com", []string{"api.example.com"}, true},
		{"API.Example.com.", []string{"api.example.com"}, true},
		{"api.example.com", []string{"example.com"}, false},
		{"a.example.com", []string{"*.example.com"}, true},
		{"a.b.example.com", []string{"*.example.com"}, true},
		{"example.com", []string{"*.example.com"}, false},
		{"evilexample.com", []string{"*.example.com"}, false},
		{".example.com", []string{"*.example.com"}, false},
		{"x..example.com", []string{"*.example.com"}, false},
		{"example.com.evil.org", []string{"*.example.com"}, false},
	} {
		if _, got := netMatch(c.host, c.pats); got != c.want {
			t.Errorf("netMatch(%q, %v) = %v", c.host, c.pats, got)
		}
	}
}

func TestForbiddenAddr(t *testing.T) {
	for addr, want := range map[string]bool{
		"127.0.0.1": true, "::1": true, "10.1.2.3": true, "192.168.0.1": true, "172.16.0.1": true,
		"169.254.169.254": true, "fe80::1": true, "0.0.0.0": true, "224.0.0.1": true, "100.64.0.1": true,
		"::ffff:127.0.0.1": true, "fc00::1": true,
		"0.1.2.3": true, "192.0.0.8": true, "198.18.0.1": true, "198.19.255.1": true,
		"64:ff9b::7f00:1": true, "64:ff9b::a9fe:a9fe": true, "64:ff9b::5db8:d822": false,
		"2002:7f00:1::": true, "2002:a9fe:a9fe::1": true, "2002:5db8:d822::1": false,
		"198.20.0.1":    false,
		"93.184.216.34": false, "2606:4700::1111": false,
	} {
		if got := forbiddenAddr(netip.MustParseAddr(addr)); got != want {
			t.Errorf("forbiddenAddr(%s) = %v", addr, got)
		}
	}
}

func TestNewNetStateDefaultsTheProxy(t *testing.T) {
	// The default is the environment's proxy function.
	if n := newNetState(&CallCtx{}, "", nil); n.seams.Proxy == nil {
		t.Error("no default proxy")
	}
}
