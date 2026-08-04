package server

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Trusted-proxy boundary.
//
// X-Forwarded-Proto / -Host / -For are hop-by-hop hints any client can forge,
// so they are honored only when the *immediate peer* is a proxy we trust. The
// production deployment (deploy/Caddyfile.example) is Caddy terminating TLS and
// proxying to 127.0.0.1:8080, so loopback is always trusted and the common case
// needs no configuration. -trusted-proxy adds CIDRs for anyone fronting the
// server from another machine.
//
// Two things ride on this: the session cookie's Secure flag / HSTS (isSecure)
// and the per-client key used for logging and the auth rate limiter (clientIP).
// Behind a proxy every request's RemoteAddr is the proxy's, so without the
// X-Forwarded-For half a per-IP limiter would meter all users as one.

// parseTrustedProxies turns the -trusted-proxy CIDR list into prefixes. A bare
// address is accepted and treated as a single-host prefix.
func parseTrustedProxies(entries []string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if p, err := netip.ParsePrefix(e); err == nil {
			out = append(out, p.Masked())
			continue
		}
		addr, err := netip.ParseAddr(e)
		if err != nil {
			return nil, fmt.Errorf("invalid -trusted-proxy entry %q: want an IP or CIDR", e)
		}
		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return out, nil
}

// trustedAddr reports whether an address is a trusted hop: loopback (the
// same-host reverse proxy) or inside a configured -trusted-proxy prefix.
func (s *Server) trustedAddr(addr netip.Addr) bool {
	if !addr.IsValid() {
		return false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() {
		return true
	}
	for _, p := range s.trustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// trustedPeer reports whether the request's immediate peer is a trusted proxy,
// i.e. whether this request's X-Forwarded-* headers may be believed.
func (s *Server) trustedPeer(r *http.Request) bool {
	addr, ok := peerAddr(r)
	return ok && s.trustedAddr(addr)
}

// peerAddr parses r.RemoteAddr (host:port, or a bare host in some tests) into
// an address.
func peerAddr(r *http.Request) (netip.Addr, bool) {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr, true
}

// clientIP returns the address to attribute the request to: the peer, or —
// when the peer is a trusted proxy — the right-most X-Forwarded-For entry that
// is not itself a trusted hop (the address the outermost trusted proxy saw).
// Walking from the right means a client-supplied prefix of the header can never
// spoof the result.
func (s *Server) clientIP(r *http.Request) string {
	if s.trustedPeer(r) {
		fwd := r.Header.Values("X-Forwarded-For")
		for i := len(fwd) - 1; i >= 0; i-- {
			parts := strings.Split(fwd[i], ",")
			for j := len(parts) - 1; j >= 0; j-- {
				addr, err := netip.ParseAddr(strings.Trim(strings.TrimSpace(parts[j]), "[]"))
				if err != nil {
					continue
				}
				if !s.trustedAddr(addr) {
					return addr.Unmap().String()
				}
			}
		}
	}
	if addr, ok := peerAddr(r); ok {
		return addr.Unmap().String()
	}
	return r.RemoteAddr
}

// isSecure reports whether the request reached the client over TLS: either it
// arrived here over TLS, or a trusted proxy terminated TLS and said so. The
// session cookie's Secure flag and HSTS are set from this, so the same binary
// works over plain-HTTP LAN today and auto-hardens behind TLS — but an
// untrusted client can no longer force either by sending the header itself.
func (s *Server) isSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return s.trustedPeer(r) && strings.EqualFold(forwardedFirst(r, "X-Forwarded-Proto"), "https")
}

// requestOrigin is the external scheme://host the client used to reach the
// server, honoring a TLS-terminating proxy's X-Forwarded-Proto/-Host.
func (s *Server) requestOrigin(r *http.Request) string {
	scheme := "http"
	if s.isSecure(r) {
		scheme = "https"
	}
	host := r.Host
	if s.trustedPeer(r) {
		if h := forwardedFirst(r, "X-Forwarded-Host"); h != "" {
			host = h
		}
	}
	return scheme + "://" + host
}

// forwardedFirst returns the first value of a comma-separated forwarded header
// (the outermost client-facing hop wrote it first).
func forwardedFirst(r *http.Request, name string) string {
	v := r.Header.Get(name)
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
