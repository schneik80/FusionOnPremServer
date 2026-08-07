package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/schneik80/fusionlocalserver/config"
)

// TLS cert/key basenames cached under config.Dir() when -tls is used without a
// caller-supplied cert.
const (
	tlsCertFile = "tls-cert.pem"
	tlsKeyFile  = "tls-key.pem"
)

// resolveTLSPaths returns the cert and key paths for a TLS run. A
// caller-supplied pair (cert and key both set) is used as-is; otherwise a
// self-signed pair is generated once and cached under config.Dir(), then
// reused. extraHosts (e.g. the -public-url hostname) are added to the cert's
// SANs so the canonical address validates. Browsers warn on the self-signed
// cert — that's expected on a LAN without a real CA; the point is to encrypt
// the wire so the session cookie can carry the Secure flag.
func resolveTLSPaths(cert, key string, extraHosts []string) (certPath, keyPath string, selfSigned bool, err error) {
	if cert != "" && key != "" {
		return cert, key, false, nil
	}
	dir, err := config.Dir()
	if err != nil {
		return "", "", false, err
	}
	certPath = filepath.Join(dir, tlsCertFile)
	keyPath = filepath.Join(dir, tlsKeyFile)
	if err := ensureSelfSignedCert(certPath, keyPath, extraHosts); err != nil {
		return "", "", false, err
	}
	return certPath, keyPath, true, nil
}

// ensureSelfSignedCert writes a self-signed cert+key to the given paths unless
// a usable pair already exists that covers every host in extraHosts. The cert
// covers localhost, the loopback addresses, the machine's hostname, its
// non-loopback IPv4 interface addresses, and any extraHosts (each treated as an
// IP SAN if it parses as an IP, else a DNS SAN). A cached cert that doesn't
// cover a newly-required host is regenerated.
func ensureSelfSignedCert(certPath, keyPath string, extraHosts []string) error {
	if fileExists(certPath) && fileExists(keyPath) && certCovers(certPath, extraHosts) {
		return nil
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generating serial: %w", err)
	}

	dnsNames := certDNSNames()
	ips := certIPs()
	for _, h := range extraHosts {
		if ip := net.ParseIP(h); ip != nil {
			ips = append(ips, ip)
		} else if h != "" {
			dnsNames = append(dnsNames, h)
		}
	}

	tmpl := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "fusionlocalserver"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(5, 0, 0),
		// IsCA marks the cert as its own trust anchor. Chromium-family
		// verifiers (including the Fusion palette's embedded browser) refuse a
		// CA:FALSE self-signed leaf even when it is imported into the OS trust
		// store — ERR_CERT_AUTHORITY_INVALID with no interstitial — so the
		// "trust this cert once per machine" install step only works when the
		// cert can stand as an anchor. Scope stays tight: it signs nothing but
		// itself, and ExtKeyUsage remains ServerAuth-only.
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("creating certificate: %w", err)
	}

	if err := writePEM(certPath, "CERTIFICATE", der, 0644); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return fmt.Errorf("marshaling key: %w", err)
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0600); err != nil {
		return err
	}
	return nil
}

func certDNSNames() []string {
	names := []string{"localhost"}
	if h, err := os.Hostname(); err == nil && h != "" && h != "localhost" {
		names = append(names, h)
	}
	return names
}

func certIPs() []net.IP {
	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() {
			continue
		}
		if v4 := ipnet.IP.To4(); v4 != nil {
			ips = append(ips, v4)
		}
	}
	return ips
}

func writePEM(path, blockType string, der []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: der})
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// certCovers reports whether the cert at certPath validates for every host in
// hosts (DNS name or IP literal). An empty list is trivially covered. Used to
// decide whether a cached self-signed cert must be regenerated to add a SAN.
func certCovers(certPath string, hosts []string) bool {
	if len(hosts) == 0 {
		return true
	}
	pemBytes, err := os.ReadFile(certPath)
	if err != nil {
		return false
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false
	}
	for _, h := range hosts {
		if h == "" {
			continue
		}
		if cert.VerifyHostname(h) != nil {
			return false
		}
	}
	return true
}
