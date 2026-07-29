package server

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes encodes a w×h PNG — a real one, so the sniffing and the header
// decode are exercised rather than stubbed.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.RGBA{R: 1, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestSaveLogo_RoundTripAndReplace(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	// Nothing configured yet.
	if _, data := loadLogoFromDisk(); data != nil {
		t.Fatalf("loadLogoFromDisk with no logo returned %d bytes, want none", len(data))
	}

	src := pngBytes(t, 800, 200)
	meta, err := SaveLogo(src)
	if err != nil {
		t.Fatalf("SaveLogo: %v", err)
	}
	if meta.ContentType != "image/png" || meta.Ext != ".png" {
		t.Errorf("type/ext = %q/%q, want image/png/.png", meta.ContentType, meta.Ext)
	}
	if meta.Width != 800 || meta.Height != 200 {
		t.Errorf("dimensions = %dx%d, want 800x200", meta.Width, meta.Height)
	}
	if meta.Size != int64(len(src)) {
		t.Errorf("size = %d, want %d", meta.Size, len(src))
	}
	if len(meta.Version()) != logoVersionLen {
		t.Errorf("version = %q, want %d hex chars", meta.Version(), logoVersionLen)
	}

	gotMeta, gotData := loadLogoFromDisk()
	if !bytes.Equal(gotData, src) {
		t.Error("reloaded bytes differ from what was stored — the logo must be served verbatim")
	}
	if gotMeta.SHA256 != meta.SHA256 {
		t.Errorf("reloaded hash = %q, want %q", gotMeta.SHA256, meta.SHA256)
	}

	// The port setting must survive a logo write: both live in server.json and
	// the load-modify-save cycle is what keeps them from clobbering each other.
	if err := SaveSettings(Settings{Port: 9123, Logo: &meta}); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveLogo(pngBytes(t, 10, 10)); err != nil {
		t.Fatalf("SaveLogo (replace): %v", err)
	}
	set, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if set.Port != 9123 {
		t.Errorf("Port = %d after a logo write, want 9123 — the logo clobbered another setting", set.Port)
	}
	if set.Logo == nil || set.Logo.Width != 10 {
		t.Errorf("Logo = %+v, want the replacement's 10px width", set.Logo)
	}
}

// Replacing a logo with a different format must not leave the old file behind:
// it would be unreferenced bytes that nothing ever serves or cleans up.
func TestSaveLogo_ReplacingFormatRemovesTheOldFile(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if _, err := SaveLogo(pngBytes(t, 40, 40)); err != nil {
		t.Fatal(err)
	}
	dir, err := brandingDir()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "logo.png")); err != nil {
		t.Fatalf("logo.png missing after save: %v", err)
	}

	if _, err := SaveLogo([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 300 100"></svg>`)); err != nil {
		t.Fatalf("SaveLogo (svg): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "logo.png")); !os.IsNotExist(err) {
		t.Error("logo.png survived a replacement with an SVG")
	}
	if _, err := os.Stat(filepath.Join(dir, "logo.svg")); err != nil {
		t.Errorf("logo.svg missing after save: %v", err)
	}

	if err := DeleteLogo(); err != nil {
		t.Fatalf("DeleteLogo: %v", err)
	}
	if _, data := loadLogoFromDisk(); data != nil {
		t.Error("logo still loads after DeleteLogo")
	}
	if _, err := os.Stat(filepath.Join(dir, "logo.svg")); !os.IsNotExist(err) {
		t.Error("logo.svg survived DeleteLogo")
	}
	// Deleting again is a no-op, not an error.
	if err := DeleteLogo(); err != nil {
		t.Errorf("second DeleteLogo: %v", err)
	}
}

func TestSaveLogo_RejectsNonImages(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	cases := map[string][]byte{
		"empty":      {},
		"plain text": []byte("this is not an image"),
		// The dangerous near-miss: HTML that merely mentions <svg. Storing it
		// would let someone serve a document from our own origin.
		"html mentioning svg": []byte(`<!doctype html><html><body>look: &lt;svg&gt;<script>alert(1)</script></body></html>`),
		"pdf":                 []byte("%PDF-1.4\n%âãÏÓ\n"),
		"zip":                 {0x50, 0x4b, 0x03, 0x04, 0, 0, 0, 0},
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := SaveLogo(data); err == nil {
				t.Fatal("SaveLogo accepted a non-image")
			}
		})
	}
	if _, data := loadLogoFromDisk(); data != nil {
		t.Error("a rejected upload still left a stored logo")
	}
}

func TestSaveLogo_RejectsOversize(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	// A valid PNG, just too big to store.
	big := append(pngBytes(t, 4, 4), bytes.Repeat([]byte{0}, maxLogoBytes)...)
	if _, err := SaveLogo(big); err == nil {
		t.Fatal("SaveLogo accepted a file over the size cap")
	}
}

func TestSvgDimensions(t *testing.T) {
	cases := []struct {
		name string
		svg  string
		w, h int
	}{
		{"explicit", `<svg width="300" height="120"></svg>`, 300, 120},
		{"px units", `<svg width="300px" height="120px"></svg>`, 300, 120},
		{"viewBox fallback", `<svg viewBox="0 0 640 480"></svg>`, 640, 480},
		{"comma viewBox", `<svg viewBox="0,0,64,48"></svg>`, 64, 48},
		// A percentage size cannot be resolved without laying the document
		// out, so it must read as unknown rather than as a wrong number.
		{"percent falls through to viewBox", `<svg width="100%" height="100%" viewBox="0 0 10 20"></svg>`, 10, 20},
		{"unknown", `<svg width="100%" height="100%"></svg>`, 0, 0},
		{"rounds", `<svg width="99.6" height="10.2"></svg>`, 100, 10},
		{"xml declaration first", "<?xml version=\"1.0\"?><svg width=\"8\" height=\"9\"></svg>", 8, 9},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, h := svgDimensions([]byte(c.svg))
			if w != c.w || h != c.h {
				t.Errorf("svgDimensions = %dx%d, want %dx%d", w, h, c.w, c.h)
			}
		})
	}
}

// postLogo uploads data as the multipart "file" field.
func postLogo(t *testing.T, base string, data []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, base+"/api/branding/logo", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

// The sign-in screen renders before there is a session, so the logo GET has to
// answer an unauthenticated caller — while the write side still refuses one.
func TestBrandingLogo_ReadIsPublicWriteIsNot(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newIntegrationServer()
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/branding/logo")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET with no logo = %d, want 404", res.StatusCode)
	}

	res = postLogo(t, srv.URL, pngBytes(t, 20, 20))
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated POST = %d, want 401", res.StatusCode)
	}
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/branding/logo", nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("unauthenticated DELETE = %d, want 401", res.StatusCode)
	}
	if _, data := loadLogoFromDisk(); data != nil {
		t.Error("an unauthenticated POST stored a logo")
	}
}

// The authenticated write path, end to end through the real routes: upload,
// serve, reject, delete. This is what the Settings screen drives.
func TestBrandingLogo_UploadRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newIntegrationServer()
	s.hubs = testHubStores(t, nil)
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	cookie := login(t, s, "user-1", "User One", "one@example.com")
	do := func(method string, data []byte) (int, string) {
		t.Helper()
		var req *http.Request
		if data == nil {
			req, _ = http.NewRequest(method, srv.URL+"/api/branding/logo", nil)
		} else {
			var body bytes.Buffer
			mw := multipart.NewWriter(&body)
			part, err := mw.CreateFormFile("file", "logo.bin")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write(data); err != nil {
				t.Fatal(err)
			}
			if err := mw.Close(); err != nil {
				t.Fatal(err)
			}
			req, _ = http.NewRequest(method, srv.URL+"/api/branding/logo", &body)
			req.Header.Set("Content-Type", mw.FormDataContentType())
		}
		req.AddCookie(cookie)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(out)
	}

	src := pngBytes(t, 1600, 400)
	code, body := do(http.MethodPost, src)
	if code != http.StatusOK {
		t.Fatalf("POST = %d (%s), want 200", code, body)
	}
	var dto LogoDTO
	if err := json.Unmarshal([]byte(body), &dto); err != nil {
		t.Fatal(err)
	}
	if dto.Width != 1600 || dto.Height != 400 {
		t.Errorf("response size = %dx%d, want 1600x400", dto.Width, dto.Height)
	}
	if dto.Version == "" {
		t.Error("response carried no version — the client needs it to build the image URL")
	}

	// Uploaded through the handler, so the cache must already hold it: the
	// public GET must answer without a restart.
	res, err := http.Get(srv.URL + "/api/branding/logo?v=" + dto.Version)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK || !bytes.Equal(got, src) {
		t.Errorf("GET after upload = %d with %d bytes, want 200 with the %d uploaded",
			res.StatusCode, len(got), len(src))
	}

	// A rejected upload must leave the previous logo intact, not blank it.
	code, body = do(http.MethodPost, []byte("this is not an image at all"))
	if code != http.StatusBadRequest {
		t.Errorf("POST (junk) = %d, want 400", code)
	}
	if !strings.Contains(body, "logo_unsupported") {
		t.Errorf("POST (junk) body = %s, want the logo_unsupported code", body)
	}
	code, _ = do(http.MethodPost, append(pngBytes(t, 4, 4), bytes.Repeat([]byte{0}, maxLogoBytes)...))
	if code != http.StatusRequestEntityTooLarge {
		t.Errorf("POST (oversize) = %d, want 413", code)
	}
	if _, data, ok := s.logo.get(); !ok || !bytes.Equal(data, src) {
		t.Error("a rejected upload replaced the stored logo")
	}

	if code, body = do(http.MethodDelete, nil); code != http.StatusNoContent {
		t.Fatalf("DELETE = %d (%s), want 204", code, body)
	}
	res, err = http.Get(srv.URL + "/api/branding/logo")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("GET after delete = %d, want 404", res.StatusCode)
	}
}

// Serving: verbatim bytes, the right content type, a content-hash ETag that
// answers a conditional request with 304, and immutable caching only for the
// URL that names the current version.
func TestBrandingLogo_ServesAndCaches(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	src := pngBytes(t, 640, 160)
	meta, err := SaveLogo(src)
	if err != nil {
		t.Fatal(err)
	}
	s := newIntegrationServer()
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	res, err := http.Get(srv.URL + "/api/branding/logo?v=" + meta.Version())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET = %d, want 200", res.StatusCode)
	}
	if !bytes.Equal(body, src) {
		t.Error("served bytes differ from the stored logo")
	}
	if ct := res.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type = %q, want image/png", ct)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable for a version-pinned URL", cc)
	}
	// The response must not be able to load or run anything: an uploaded SVG
	// is a document served from our own origin.
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'none'") {
		t.Errorf("CSP = %q, want default-src 'none'", csp)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff on the logo response")
	}

	etag := res.Header.Get("ETag")
	if etag != `"`+meta.Version()+`"` {
		t.Fatalf("ETag = %q, want the content hash", etag)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/branding/logo", nil)
	req.Header.Set("If-None-Match", etag)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", res.StatusCode)
	}

	// An unversioned URL must revalidate rather than be cached forever — it
	// would otherwise pin the first logo the browser ever saw.
	res, err = http.Get(srv.URL + "/api/branding/logo")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if cc := res.Header.Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want revalidation for an unversioned URL", cc)
	}
}

// The sign-in screen learns about the logo from /api/meta, which it can reach
// before it has a session.
func TestMeta_CarriesTheLogo(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newIntegrationServer()
	srv := httptest.NewServer(s.routes())
	defer srv.Close()

	fetchMeta := func() MetaDTO {
		t.Helper()
		res, err := http.Get(srv.URL + "/api/meta")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var m MetaDTO
		if err := json.NewDecoder(res.Body).Decode(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}

	if got := fetchMeta(); got.Logo != nil {
		t.Errorf("meta.Logo = %+v with no logo configured, want absent", got.Logo)
	}

	meta, err := SaveLogo(pngBytes(t, 300, 100))
	if err != nil {
		t.Fatal(err)
	}
	// The server caches the bytes, so a write outside the handler has to be
	// published to it — the same call the handler makes.
	s.logo.set(meta, pngBytes(t, 300, 100))

	got := fetchMeta()
	if got.Logo == nil {
		t.Fatal("meta.Logo absent after a logo was stored")
	}
	if got.Logo.Version != meta.Version() {
		t.Errorf("meta.Logo.Version = %q, want %q", got.Logo.Version, meta.Version())
	}
	if got.Logo.Width != 300 || got.Logo.Height != 100 {
		t.Errorf("meta.Logo size = %dx%d, want 300x100", got.Logo.Width, got.Logo.Height)
	}
}

// The cache must not outlive the file it caches.
func TestLogoStore_CacheFollowsWrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var store logoStore

	if _, _, ok := store.get(); ok {
		t.Fatal("empty store reported a logo")
	}
	src := pngBytes(t, 50, 25)
	meta, err := SaveLogo(src)
	if err != nil {
		t.Fatal(err)
	}
	// Still cached as absent until the writer publishes — the cache is
	// deliberately not a disk poller.
	if _, _, ok := store.get(); ok {
		t.Error("store saw a logo it was never told about")
	}
	store.set(meta, src)
	gotMeta, gotData, ok := store.get()
	if !ok || !bytes.Equal(gotData, src) || gotMeta.SHA256 != meta.SHA256 {
		t.Error("store.get did not return what was published to it")
	}
	store.clear()
	if _, _, ok := store.get(); ok {
		t.Error("store still reported a logo after clear")
	}
}
