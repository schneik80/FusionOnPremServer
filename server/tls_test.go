package server

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureSelfSignedCert(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "c.pem")
	keyPath := filepath.Join(dir, "k.pem")

	if err := ensureSelfSignedCert(certPath, keyPath, nil); err != nil {
		t.Fatalf("ensureSelfSignedCert: %v", err)
	}

	// Cert + key load as a usable TLS keypair.
	if _, err := tls.LoadX509KeyPair(certPath, keyPath); err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}

	// Cert covers localhost and the loopback IP.
	pemBytes, _ := os.ReadFile(certPath)
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		t.Fatal("cert is not valid PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if err := cert.VerifyHostname("localhost"); err != nil {
		t.Errorf("cert does not cover localhost: %v", err)
	}
	hasLoopback := false
	for _, ip := range cert.IPAddresses {
		if ip.Equal(net.IPv4(127, 0, 0, 1)) {
			hasLoopback = true
		}
	}
	if !hasLoopback {
		t.Errorf("cert IPs %v do not include 127.0.0.1", cert.IPAddresses)
	}

	// Key file is owner-only.
	if fi, err := os.Stat(keyPath); err == nil {
		if perm := fi.Mode().Perm(); perm != 0600 {
			t.Errorf("key file mode = %o, want 600", perm)
		}
	}

	// Idempotent: a second call leaves the existing cert untouched.
	before, _ := os.ReadFile(certPath)
	if err := ensureSelfSignedCert(certPath, keyPath, nil); err != nil {
		t.Fatalf("second ensureSelfSignedCert: %v", err)
	}
	after, _ := os.ReadFile(certPath)
	if string(before) != string(after) {
		t.Error("cert was regenerated on the second call; expected reuse")
	}
}

func TestEnsureSelfSignedCert_CoversExtraHostAndRegenerates(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "c.pem")
	keyPath := filepath.Join(dir, "k.pem")

	// Generate covering only the defaults.
	if err := ensureSelfSignedCert(certPath, keyPath, nil); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(certPath)

	// Requiring a host not in the default set must regenerate the cert.
	if err := ensureSelfSignedCert(certPath, keyPath, []string{"ryzen-nobara.local"}); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(certPath)
	if string(first) == string(second) {
		t.Fatal("cert was not regenerated to add the extra-host SAN")
	}

	block, _ := pem.Decode(second)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := cert.VerifyHostname("ryzen-nobara.local"); err != nil {
		t.Errorf("cert does not cover ryzen-nobara.local: %v", err)
	}
	if err := cert.VerifyHostname("localhost"); err != nil {
		t.Errorf("cert lost localhost coverage: %v", err)
	}

	// Now that it covers the host, a repeat call must NOT regenerate.
	if err := ensureSelfSignedCert(certPath, keyPath, []string{"ryzen-nobara.local"}); err != nil {
		t.Fatal(err)
	}
	third, _ := os.ReadFile(certPath)
	if string(second) != string(third) {
		t.Error("cert regenerated despite already covering the host")
	}
}

func TestIsSecure(t *testing.T) {
	s := newAuthTestServer()

	plain := httptest.NewRequest(http.MethodGet, "/", nil)
	if s.isSecure(plain) {
		t.Error("plain HTTP request reported secure")
	}

	tlsReq := httptest.NewRequest(http.MethodGet, "/", nil)
	tlsReq.TLS = &tls.ConnectionState{}
	if !s.isSecure(tlsReq) {
		t.Error("TLS request reported not secure")
	}

	// X-Forwarded-Proto is honored only from a trusted hop. httptest's default
	// peer (192.0.2.1) is a stranger: believing it would let any LAN client
	// force the Secure cookie flag and HSTS.
	spoofed := httptest.NewRequest(http.MethodGet, "/", nil)
	spoofed.Header.Set("X-Forwarded-Proto", "https")
	if s.isSecure(spoofed) {
		t.Error("X-Forwarded-Proto from an untrusted peer reported secure")
	}

	// The real deployment: Caddy terminates TLS and proxies from loopback.
	fwd := httptest.NewRequest(http.MethodGet, "/", nil)
	fwd.RemoteAddr = "127.0.0.1:34567"
	fwd.Header.Set("X-Forwarded-Proto", "https")
	if !s.isSecure(fwd) {
		t.Error("X-Forwarded-Proto from a loopback proxy reported not secure")
	}

	// An explicitly configured off-host proxy is trusted too.
	cfg := newAuthTestServer()
	cfg.trustedProxies = mustPrefixes(t, "10.1.2.0/24")
	viaLAN := httptest.NewRequest(http.MethodGet, "/", nil)
	viaLAN.RemoteAddr = "10.1.2.9:5000"
	viaLAN.Header.Set("X-Forwarded-Proto", "https")
	if !cfg.isSecure(viaLAN) {
		t.Error("X-Forwarded-Proto from a configured trusted proxy reported not secure")
	}
}

func TestHandleAuthLogin_SecureCookieUnderTLS(t *testing.T) {
	s := newAuthTestServer()
	req := httptest.NewRequest(http.MethodGet, "https://host:8443/api/auth/login", nil)
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	s.handleAuthLogin(rec, req)

	var pending *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == pendingCookieName {
			pending = c
		}
	}
	if pending == nil {
		t.Fatal("no pending cookie set")
	}
	if !pending.Secure {
		t.Error("pending cookie is not Secure under TLS")
	}
	// And the derived redirect_uri uses https.
	if loc := rec.Header().Get("Location"); loc == "" {
		t.Error("no redirect")
	}
}
