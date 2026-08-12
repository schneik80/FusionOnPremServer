package main

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
)

// Pairing is the whole security model of this program.
//
// A URL scheme is a public entry point: any web page in any browser can
// navigate to fusionlocal://…, and the OS will launch us with whatever that
// page chose. If we simply did as we were told, a hostile page could drive the
// user's Fusion — opening arbitrary documents, or inserting geometry into
// whatever they have open.
//
// So the server named in a launch URL must be one the user explicitly paired
// with, on this machine, from a terminal. Nothing in a browser can add a
// pairing; nothing about a launch URL can bypass one. A launch from an unpaired
// server does nothing except say why.
//
// Pairing also solves a practical problem. fusionlocalserver serves HTTPS from
// a self-signed certificate by default, which no system trust store will
// accept. Rather than telling users to disable verification (which would make
// the pairing meaningless — anyone on the network could then impersonate the
// server), `pair` records the server's certificate fingerprint at the moment
// the user vouches for it, and later connections must present that exact
// certificate. Trust on first use, pinned thereafter.

// pairingVersion is the file's schema version, following the same
// refuse-a-newer-file rule the server's stores use.
const pairingVersion = 1

// pairedServer is one trusted fusionlocalserver.
type pairedServer struct {
	// Origin is normalized to scheme://host[:port].
	Origin string `json:"origin"`
	// Fingerprint is the SHA-256 of the server's leaf certificate, hex, lower
	// case. Empty for a plain-http server (nothing to pin) and for one whose
	// certificate the system trust store already validates.
	Fingerprint string `json:"fingerprint,omitempty"`
	PairedAt    string `json:"pairedAt,omitempty"`
}

// pairingFile mirrors helper.json.
type pairingFile struct {
	Version int            `json:"version"`
	Servers []pairedServer `json:"servers"`
}

// pairingPath is <config>/helper.json, alongside the server's own config. Same
// directory on every platform, matching config.Dir() — a helper and a server on
// one machine should keep their state in one place.
func pairingPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating your home directory: %w", err)
	}
	dir := filepath.Join(home, ".config", "fusionlocalserver")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("creating %s: %w", dir, err)
	}
	return filepath.Join(dir, "helper.json"), nil
}

// loadPairings reads the trusted-server list. A missing file is not an error —
// it just means nothing is trusted yet.
func loadPairings() (pairingFile, error) {
	path, err := pairingPath()
	if err != nil {
		return pairingFile{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return pairingFile{Version: pairingVersion}, nil
	}
	if err != nil {
		return pairingFile{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var pf pairingFile
	if err := json.Unmarshal(data, &pf); err != nil {
		// Refuse rather than reset: silently starting from an empty trust list
		// would turn a corrupt file into "nothing works", with no explanation.
		return pairingFile{}, fmt.Errorf("%s is not valid JSON: %w", path, err)
	}
	if pf.Version > pairingVersion {
		return pairingFile{}, fmt.Errorf("%s was written by a newer fls-helper (version %d); upgrade this one", path, pf.Version)
	}
	return pf, nil
}

func savePairings(pf pairingFile) error {
	path, err := pairingPath()
	if err != nil {
		return err
	}
	pf.Version = pairingVersion
	if pf.Servers == nil {
		pf.Servers = []pairedServer{}
	}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.WriteFile(path, append(data, '\n'), 0600)
}

// pair adds a server to the trusted list, pinning its certificate if it serves
// HTTPS from one the system does not already trust.
func pair(raw string) error {
	origin := fusionlink.NormalizeOrigin(raw)
	if origin == "" {
		return fmt.Errorf("%q is not an absolute http(s) URL — try https://host:port", raw)
	}
	fingerprint, err := probeCertificate(origin)
	if err != nil {
		return err
	}
	pf, err := loadPairings()
	if err != nil {
		return err
	}
	entry := pairedServer{
		Origin:      origin,
		Fingerprint: fingerprint,
		PairedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	if i := slices.IndexFunc(pf.Servers, func(s pairedServer) bool { return s.Origin == origin }); i >= 0 {
		// Re-pairing is how a user accepts a rotated certificate. It is an
		// explicit, local, deliberate act — which is exactly the bar that
		// should be met before a pin changes.
		pf.Servers[i] = entry
	} else {
		pf.Servers = append(pf.Servers, entry)
	}
	return savePairings(pf)
}

// unpair removes a server from the trusted list.
func unpair(raw string) error {
	origin := fusionlink.NormalizeOrigin(raw)
	if origin == "" {
		return fmt.Errorf("%q is not an absolute http(s) URL", raw)
	}
	pf, err := loadPairings()
	if err != nil {
		return err
	}
	pf.Servers = slices.DeleteFunc(pf.Servers, func(s pairedServer) bool { return s.Origin == origin })
	return savePairings(pf)
}

// trusted resolves a launch URL's server to its pairing. Comparison is on the
// normalized origin, so scheme, host and port must all match: pairing with
// https://host:8080 does not trust http://host:8080, and trusting one host
// never trusts another on the same machine.
func trusted(rawServer string) (pairedServer, bool) {
	origin := fusionlink.NormalizeOrigin(rawServer)
	if origin == "" {
		return pairedServer{}, false
	}
	pf, err := loadPairings()
	if err != nil {
		return pairedServer{}, false
	}
	i := slices.IndexFunc(pf.Servers, func(s pairedServer) bool { return s.Origin == origin })
	if i < 0 {
		return pairedServer{}, false
	}
	return pf.Servers[i], true
}

// probeCertificate connects to an HTTPS origin and returns the leaf
// certificate's SHA-256 fingerprint, or "" when there is nothing to pin
// (plain http, or a certificate the system already trusts).
//
// It deliberately connects without verification: the point of pairing is to
// record what this server presents *right now*, at a moment the user has
// vouched for it out of band. Failing here would make the common case — the
// self-signed certificate the server generates on first run — impossible to
// pair with at all.
func probeCertificate(origin string) (string, error) {
	u, err := url.Parse(origin)
	if err != nil {
		return "", err
	}
	if u.Scheme != "https" {
		return "", nil
	}
	host := u.Host
	if _, _, err := net.SplitHostPort(host); err != nil {
		host = net.JoinHostPort(host, "443")
	}
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", host,
		&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // see doc comment: TOFU by design
	if err != nil {
		return "", fmt.Errorf("cannot reach %s: %w", origin, err)
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("%s presented no certificate", origin)
	}
	leaf := certs[0]

	// If the system trust store already accepts it, there is nothing to pin:
	// normal verification is stronger than a pin, because it survives renewal.
	if _, verr := leaf.Verify(x509.VerifyOptions{DNSName: u.Hostname()}); verr == nil {
		return "", nil
	}
	sum := sha256.Sum256(leaf.Raw)
	return hex.EncodeToString(sum[:]), nil
}

// httpClientFor builds the client used to talk to one paired server. With no
// pin it is an ordinary client doing ordinary verification; with a pin it
// accepts exactly one certificate and nothing else — not "any certificate",
// which is what InsecureSkipVerify alone would mean.
func httpClientFor(p pairedServer) *http.Client {
	if p.Fingerprint == "" {
		return &http.Client{Timeout: serverTimeout}
	}
	want := strings.ToLower(p.Fingerprint)
	return &http.Client{
		Timeout: serverTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				// Verification is not skipped, it is REPLACED: the callback
				// below is the whole check, and it is stricter than the default
				// one — the certificate must be byte-identical to the pinned one.
				InsecureSkipVerify: true, //nolint:gosec // replaced by VerifyPeerCertificate
				VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
					if len(rawCerts) == 0 {
						return errors.New("server presented no certificate")
					}
					sum := sha256.Sum256(rawCerts[0])
					if hex.EncodeToString(sum[:]) != want {
						return errors.New("server certificate does not match the one recorded when you paired; " +
							"re-run `fls-helper pair` if it was legitimately replaced")
					}
					return nil
				},
			},
		},
	}
}
