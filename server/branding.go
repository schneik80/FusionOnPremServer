package server

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	// Decoders for the raster formats we accept. Registered for their side
	// effect only: image.DecodeConfig reads the header to recover the
	// intrinsic size without ever decoding the pixels.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/schneik80/fusionlocalserver/config"
	"github.com/schneik80/fusionlocalserver/internal/atomicfile"
)

// The sign-in logo: an image the operator uploads once, which replaces the
// generic icon on the sign-in screen.
//
// It is deliberately NOT hub data, and that is the whole design:
//
//   - The sign-in screen renders before anyone has a session, so there is no
//     hub to scope it to and no identity to authorize against. GET is public
//     by necessity — treat the logo as world-readable to anyone who can reach
//     the server, and do not put anything confidential in it.
//   - A hub-scoped logo would be worse, not better: hubs are IP boundaries
//     between clients, and serving one client's mark on a login page every
//     other client sees would leak across exactly the boundary the rest of
//     the local stores exist to hold. Server-wide branding for a server-wide
//     screen is the honest scope.
//
// The bytes are stored verbatim under <config>/branding/logo.<ext> and are
// never re-encoded: a logo is usually an SVG (which no raster pipeline could
// preserve) or a lossless PNG with alpha, and re-compressing either to save
// bytes on a localhost round-trip is a bad trade. The size cap is what bounds
// the transfer instead; the sign-in screen constrains the DISPLAY height.
const (
	// maxLogoBytes caps one upload. Generous for any real mark, small enough
	// that the whole file sits in memory and rides a single response.
	maxLogoBytes = 2 << 20 // 2 MiB

	// logoVersionLen is how much of the content hash identifies a revision.
	// 48 bits: this is a cache key, not a security boundary — the file it
	// names is public and immutable, and a collision would at worst serve a
	// stale logo.
	logoVersionLen = 12
)

// Sniffed content type → stored extension. The client's declared type is never
// trusted; the allow-list is keyed by what the bytes actually are.
//
// WebP is accepted but has no stdlib decoder, so its intrinsic size stays
// unknown (zero) — the sign-in screen simply doesn't reserve an aspect box for
// it, which costs a reflow on first paint and nothing else.
var logoTypes = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/svg+xml": ".svg",
}

// errUnsupportedLogo is what SaveLogo returns for bytes that are not one of
// the accepted image formats. The handler maps it to a 400 the SPA localizes.
var errUnsupportedLogo = errors.New("unsupported image format")

// LogoMeta is the persisted description of the stored logo. It lives in
// server.json beside the port (see Settings) rather than in a file of its own:
// it is a handful of scalars describing one blob, and keeping it there means
// the existing load-modify-save cycle already protects it.
type LogoMeta struct {
	ContentType string `json:"contentType"`
	Ext         string `json:"ext"`
	// SHA256 is the full hex digest of the stored bytes. Version() shortens it
	// into the cache-busting token the client puts in the image URL.
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// Width/Height are the intrinsic pixel size, 0 when it could not be
	// determined (WebP, or an SVG with neither explicit size nor a viewBox).
	Width     int       `json:"width,omitempty"`
	Height    int       `json:"height,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Version is the short content hash identifying this revision of the logo. It
// rides in the public meta payload and in the image URL, so replacing the logo
// changes the URL and retires every cached copy at once.
func (m LogoMeta) Version() string {
	if len(m.SHA256) < logoVersionLen {
		return m.SHA256
	}
	return m.SHA256[:logoVersionLen]
}

// logoStore is the server's cached copy of the logo. The sign-in screen is the
// one page every visitor loads before doing anything else, so its image is
// read from disk once per process and served from memory after that. The zero
// value is a valid, empty store that loads on first use.
type logoStore struct {
	mu     sync.RWMutex
	loaded bool
	meta   LogoMeta
	data   []byte
}

// get returns the cached logo, loading it from disk on first call. ok is false
// when no logo is configured (or the stored file has gone missing).
func (l *logoStore) get() (LogoMeta, []byte, bool) {
	l.mu.RLock()
	if l.loaded {
		meta, data := l.meta, l.data
		l.mu.RUnlock()
		return meta, data, data != nil
	}
	l.mu.RUnlock()

	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.loaded { // another goroutine may have loaded it while we waited
		l.meta, l.data = loadLogoFromDisk()
		l.loaded = true
	}
	return l.meta, l.data, l.data != nil
}

// set replaces the cached logo after a successful write.
func (l *logoStore) set(meta LogoMeta, data []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.meta, l.data, l.loaded = meta, data, true
}

// clear drops the cached logo after a delete.
func (l *logoStore) clear() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.meta, l.data, l.loaded = LogoMeta{}, nil, true
}

// brandingDir is <config>/branding, created on demand.
func brandingDir() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, "branding")
	return dir, os.MkdirAll(dir, 0700)
}

// SaveLogo validates, measures and stores data as the sign-in logo, returning
// the metadata it recorded. The caller is responsible for refreshing the
// server's cache (see handleBrandingLogoSet).
func SaveLogo(data []byte) (LogoMeta, error) {
	if len(data) == 0 {
		return LogoMeta{}, errUnsupportedLogo
	}
	if len(data) > maxLogoBytes {
		return LogoMeta{}, fmt.Errorf("logo is %d bytes, over the %d byte limit", len(data), maxLogoBytes)
	}
	ctype, ok := sniffLogoType(data)
	if !ok {
		return LogoMeta{}, errUnsupportedLogo
	}
	ext := logoTypes[ctype]

	sum := sha256.Sum256(data)
	w, h := logoDimensions(ctype, data)
	meta := LogoMeta{
		ContentType: ctype,
		Ext:         ext,
		SHA256:      hex.EncodeToString(sum[:]),
		Size:        int64(len(data)),
		Width:       w,
		Height:      h,
		UpdatedAt:   time.Now().UTC(),
	}

	dir, err := brandingDir()
	if err != nil {
		return LogoMeta{}, err
	}
	if err := atomicfile.WriteFile(filepath.Join(dir, "logo"+ext), data, 0600); err != nil {
		return LogoMeta{}, err
	}
	// A format change leaves the previous file orphaned (logo.png -> logo.svg);
	// drop it so the directory never holds a logo nothing points at.
	removeOtherLogos(dir, ext)

	if err := UpdateSettings(func(s *Settings) { s.Logo = &meta }); err != nil {
		return LogoMeta{}, err
	}
	return meta, nil
}

// DeleteLogo removes the stored logo and its metadata. Deleting when none is
// set is not an error.
func DeleteLogo() error {
	if err := UpdateSettings(func(s *Settings) { s.Logo = nil }); err != nil {
		return err
	}
	dir, err := brandingDir()
	if err != nil {
		return err
	}
	removeOtherLogos(dir, "")
	return nil
}

// removeOtherLogos deletes every stored logo file except the one with keepExt
// (pass "" to remove them all). Failures are ignored: an orphan file is
// unreferenced, and refusing the write over it would be the worse outcome.
func removeOtherLogos(dir, keepExt string) {
	for _, ext := range logoTypes {
		if ext == keepExt {
			continue
		}
		_ = os.Remove(filepath.Join(dir, "logo"+ext))
	}
}

// loadLogoFromDisk reads the configured logo. It returns a nil slice when none
// is configured, when the config dir is unreachable, or when the metadata
// points at a file that is gone — all of which mean the same thing to every
// caller: render the generic mark.
func loadLogoFromDisk() (LogoMeta, []byte) {
	set, err := LoadSettings()
	if err != nil || set.Logo == nil || set.Logo.Ext == "" {
		return LogoMeta{}, nil
	}
	dir, err := brandingDir()
	if err != nil {
		return LogoMeta{}, nil
	}
	data, err := os.ReadFile(filepath.Join(dir, "logo"+set.Logo.Ext))
	if err != nil {
		return LogoMeta{}, nil
	}
	return *set.Logo, data
}

// sniffLogoType identifies the image from its own bytes. http.DetectContentType
// covers the raster formats; SVG is XML, which it can only report as text, so
// it is confirmed by parsing far enough to see an <svg> root element.
func sniffLogoType(data []byte) (string, bool) {
	ctype, _, _ := strings.Cut(http.DetectContentType(data), ";")
	if _, ok := logoTypes[ctype]; ok && ctype != "image/svg+xml" {
		return ctype, true
	}
	if isSVG(data) {
		return "image/svg+xml", true
	}
	return "", false
}

// isSVG reports whether data's first element is an <svg> root. Parsing rather
// than substring-matching is what makes this a real check: an HTML page that
// merely mentions "<svg" somewhere inside must not be storable as an image and
// then served back from our own origin.
func isSVG(data []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(data))
	// Entity expansion is an attack surface on untrusted XML and we need none
	// of it; an unknown entity simply ends the scan.
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if start, ok := tok.(xml.StartElement); ok {
			return strings.EqualFold(start.Name.Local, "svg")
		}
	}
}

// logoDimensions recovers the intrinsic size, or 0,0 when it cannot.
func logoDimensions(ctype string, data []byte) (int, int) {
	if ctype == "image/svg+xml" {
		return svgDimensions(data)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}

// svgDimensions reads the root element's width/height, falling back to the
// viewBox. An SVG is resolution-independent, so this is only ever a hint: the
// sign-in screen uses it to reserve the right box before the image arrives.
func svgDimensions(data []byte) (int, int) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = false
	for {
		tok, err := dec.Token()
		if err != nil {
			return 0, 0
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if !strings.EqualFold(start.Name.Local, "svg") {
			return 0, 0
		}
		var width, height, viewBox string
		for _, a := range start.Attr {
			switch strings.ToLower(a.Name.Local) {
			case "width":
				width = a.Value
			case "height":
				height = a.Value
			case "viewbox":
				viewBox = a.Value
			}
		}
		if w, h := svgLength(width), svgLength(height); w > 0 && h > 0 {
			return w, h
		}
		// viewBox is "min-x min-y width height".
		if f := strings.FieldsFunc(viewBox, func(r rune) bool { return r == ' ' || r == ',' }); len(f) == 4 {
			if w, h := svgLength(f[2]), svgLength(f[3]); w > 0 && h > 0 {
				return w, h
			}
		}
		return 0, 0
	}
}

// svgLength parses an SVG length, accepting a bare number or one in px. Any
// other unit (em, %, mm) describes a size we cannot resolve without laying the
// document out, so it reads as unknown rather than as a wrong number.
func svgLength(v string) int {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 || f > 1<<20 {
		return 0
	}
	return int(f + 0.5)
}

// readLogoUpload reads at most maxLogoBytes+1 from r, so the caller can tell a
// file that fits from one that is merely truncated at the cap.
func readLogoUpload(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, maxLogoBytes+1))
}
