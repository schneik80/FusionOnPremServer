package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// isolateHome points the pairing file at a throwaway directory. pairingPath
// resolves through os.UserHomeDir, which reads $HOME.
func isolateHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	return dir
}

func TestPair_RefusesNonAbsoluteURLs(t *testing.T) {
	isolateHome(t)
	// A pairing is a trust grant; anything we cannot reduce to a definite
	// scheme://host origin must be refused rather than stored as a wildcard.
	for _, raw := range []string{"", "host:8080", "file:///etc/passwd", "fusionlocal://v1/open", "not a url"} {
		if err := pair(raw); err == nil {
			t.Errorf("pair(%q): want error, got nil", raw)
		}
	}
}

func TestPairUnpairAndTrust_PlainHTTP(t *testing.T) {
	isolateHome(t)

	// Nothing is trusted before pairing — this is what stops a hostile page
	// from driving Fusion through a fabricated launch URL.
	if _, ok := trusted("http://fusion.example:8080"); ok {
		t.Fatal("an unpaired server was trusted")
	}
	if err := pair("http://fusion.example:8080/"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	p, ok := trusted("http://Fusion.Example:8080")
	if !ok {
		t.Fatal("paired server was not trusted (case-insensitive host)")
	}
	if p.Origin != "http://fusion.example:8080" {
		t.Errorf("origin = %q, want the normalized form", p.Origin)
	}
	if p.Fingerprint != "" {
		t.Errorf("fingerprint = %q, want empty for plain http", p.Fingerprint)
	}

	if err := unpair("http://fusion.example:8080"); err != nil {
		t.Fatalf("unpair: %v", err)
	}
	if _, ok := trusted("http://fusion.example:8080"); ok {
		t.Error("server is still trusted after unpair")
	}
}

func TestTrust_SchemeHostAndPortAllMatter(t *testing.T) {
	isolateHome(t)
	if err := pair("http://fusion.example:8080"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	// Trusting one origin must not trust a downgrade, another port, or another
	// host — each is a different server that could be run by someone else.
	for _, other := range []string{
		"https://fusion.example:8080",
		"http://fusion.example:9090",
		"http://evil.example:8080",
		"http://fusion.example",
	} {
		if _, ok := trusted(other); ok {
			t.Errorf("pairing http://fusion.example:8080 also trusted %q", other)
		}
	}
}

func TestPair_IsIdempotentAndRepairUpdatesInPlace(t *testing.T) {
	isolateHome(t)
	for range 3 {
		if err := pair("http://fusion.example:8080"); err != nil {
			t.Fatalf("pair: %v", err)
		}
	}
	pf, err := loadPairings()
	if err != nil {
		t.Fatalf("loadPairings: %v", err)
	}
	if len(pf.Servers) != 1 {
		t.Errorf("pairing three times produced %d entries, want 1", len(pf.Servers))
	}
}

func TestPair_PinsASelfSignedCertificate(t *testing.T) {
	isolateHome(t)
	// httptest's TLS server uses a certificate no trust store accepts —
	// exactly the situation fusionlocalserver creates on first run.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	if err := pair(srv.URL); err != nil {
		t.Fatalf("pair: %v", err)
	}
	p, ok := trusted(srv.URL)
	if !ok {
		t.Fatal("paired TLS server was not trusted")
	}
	sum := sha256.Sum256(srv.Certificate().Raw)
	if p.Fingerprint != hex.EncodeToString(sum[:]) {
		t.Errorf("fingerprint = %q, want the leaf certificate's SHA-256", p.Fingerprint)
	}
}

func TestHttpClientFor_AcceptsOnlyThePinnedCertificate(t *testing.T) {
	isolateHome(t)
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	if err := pair(srv.URL); err != nil {
		t.Fatalf("pair: %v", err)
	}
	p, _ := trusted(srv.URL)

	// The pinned client reaches the server it was paired with...
	resp, err := httpClientFor(p).Get(srv.URL)
	if err != nil {
		t.Fatalf("pinned client could not reach the paired server: %v", err)
	}
	resp.Body.Close()

	// ...and refuses a DIFFERENT self-signed server. This is the whole point:
	// InsecureSkipVerify is set, but verification is replaced by the pin, not
	// removed — otherwise anyone on the network could impersonate the server.
	//
	// The impostor needs its own certificate: every httptest.NewTLSServer
	// shares one built-in cert, so a second plain httptest server would match
	// the pin and prove nothing.
	other := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	other.TLS = &tls.Config{Certificates: []tls.Certificate{freshCert(t)}}
	other.StartTLS()
	defer other.Close()
	if _, err := httpClientFor(p).Get(other.URL); err == nil {
		t.Error("pinned client accepted a different certificate")
	}
}

// freshCert mints a throwaway self-signed certificate for 127.0.0.1, distinct
// from the one httptest bakes in.
func freshCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "impostor"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

func TestHttpClientFor_UnpinnedDoesOrdinaryVerification(t *testing.T) {
	// With no pin recorded (an http server, or one with a trusted cert), the
	// client must do NORMAL verification — never blanket-accept.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()

	c := httpClientFor(pairedServer{Origin: srv.URL})
	if _, err := c.Get(srv.URL); err == nil {
		t.Error("unpinned client accepted an untrusted certificate")
	}
	// Sanity: the failure above is certificate verification, not the transport.
	var cfg *tls.Config
	if tr, ok := c.Transport.(*http.Transport); ok && tr != nil {
		cfg = tr.TLSClientConfig
	}
	if cfg != nil && cfg.InsecureSkipVerify {
		t.Error("unpinned client has InsecureSkipVerify set")
	}
}

func TestLoadPairings_RefusesCorruptAndFutureFiles(t *testing.T) {
	home := isolateHome(t)
	path := filepath.Join(home, ".config", "fusionlocalserver", "helper.json")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}

	// Corrupt: refuse rather than silently starting from an empty trust list,
	// which would turn a bad file into "nothing works" with no explanation.
	if err := os.WriteFile(path, []byte("{not json"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPairings(); err == nil {
		t.Error("corrupt helper.json: want error, got nil")
	}

	// Written by a newer build: refuse, so an old helper cannot rewrite it and
	// drop fields it does not understand.
	future, _ := json.Marshal(pairingFile{Version: pairingVersion + 1})
	if err := os.WriteFile(path, future, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPairings(); err == nil {
		t.Error("future-version helper.json: want error, got nil")
	}
}

func TestPairingFileIsOwnerOnly(t *testing.T) {
	home := isolateHome(t)
	if err := pair("http://fusion.example:8080"); err != nil {
		t.Fatalf("pair: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, ".config", "fusionlocalserver", "helper.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("helper.json mode = %04o, want 0600", perm)
	}
}
