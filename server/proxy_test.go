package server

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
)

// mustPrefixes parses -trusted-proxy style entries for tests.
func mustPrefixes(t *testing.T, entries ...string) []netip.Prefix {
	t.Helper()
	p, err := parseTrustedProxies(entries)
	if err != nil {
		t.Fatalf("parseTrustedProxies(%v): %v", entries, err)
	}
	return p
}

func TestParseTrustedProxies(t *testing.T) {
	got, err := parseTrustedProxies([]string{"10.0.0.0/8", " 192.168.1.5 ", "", "fd00::/8"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("parsed %d prefixes, want 3: %v", len(got), got)
	}
	// A bare address becomes a single-host prefix.
	if got[1].Bits() != 32 || !got[1].Contains(netip.MustParseAddr("192.168.1.5")) {
		t.Errorf("bare address became %v, want a /32 host prefix", got[1])
	}
	if _, err := parseTrustedProxies([]string{"not-an-ip"}); err == nil {
		t.Error("invalid entry accepted")
	}
}

func TestClientIPTrustBoundary(t *testing.T) {
	plain := &Server{}
	behindProxy := &Server{trustedProxies: mustPrefixes(t, "10.1.2.0/24")}

	cases := []struct {
		name    string
		s       *Server
		remote  string
		fwd     []string
		want    string
		comment string
	}{
		{
			name:    "untrusted peer's header ignored",
			s:       plain,
			remote:  "203.0.113.7:4444",
			fwd:     []string{"1.2.3.4"},
			want:    "203.0.113.7",
			comment: "a stranger must not be able to pick its own limiter key",
		},
		{
			name:   "no header falls back to the peer",
			s:      plain,
			remote: "203.0.113.7:4444",
			want:   "203.0.113.7",
		},
		{
			name:   "loopback proxy is trusted",
			s:      plain,
			remote: "127.0.0.1:5555",
			fwd:    []string{"198.51.100.23"},
			want:   "198.51.100.23",
		},
		{
			name:    "client-supplied prefix cannot spoof",
			s:       plain,
			remote:  "127.0.0.1:5555",
			fwd:     []string{"1.2.3.4, 198.51.100.23"},
			want:    "198.51.100.23",
			comment: "the right-most untrusted entry is what our own proxy appended",
		},
		{
			name:   "trusted hops are skipped from the right",
			s:      behindProxy,
			remote: "10.1.2.9:5555",
			fwd:    []string{"198.51.100.23, 10.1.2.9", "127.0.0.1"},
			want:   "198.51.100.23",
		},
		{
			name:   "configured proxy honored, stranger not",
			s:      behindProxy,
			remote: "203.0.113.7:4444",
			fwd:    []string{"198.51.100.23"},
			want:   "203.0.113.7",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = tc.remote
			for _, v := range tc.fwd {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := tc.s.clientIP(r); got != tc.want {
				t.Errorf("clientIP = %q, want %q (%s)", got, tc.want, tc.comment)
			}
		})
	}
}

func TestRequestOriginHonorsTrustedForwardedHost(t *testing.T) {
	s := &Server{}

	direct := httptest.NewRequest(http.MethodGet, "http://fls.lan:8080/api/meta", nil)
	direct.Header.Set("X-Forwarded-Host", "evil.example")
	direct.Header.Set("X-Forwarded-Proto", "https")
	if got := s.requestOrigin(direct); got != "http://fls.lan:8080" {
		t.Errorf("origin = %q, want the real host — an untrusted peer rewrote it", got)
	}

	viaProxy := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8080/api/meta", nil)
	viaProxy.RemoteAddr = "127.0.0.1:5555"
	viaProxy.Header.Set("X-Forwarded-Host", "fls.example.com")
	viaProxy.Header.Set("X-Forwarded-Proto", "https")
	if got := s.requestOrigin(viaProxy); got != "https://fls.example.com" {
		t.Errorf("origin = %q, want https://fls.example.com", got)
	}
}
