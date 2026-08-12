package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPickArchiveFormat(t *testing.T) {
	cases := []struct {
		name    string
		formats []string
		want    string
		wantOK  bool
	}{
		{"f3z preferred over f3d", []string{"f3d", "step", "f3z"}, "f3z", true},
		{"f3d when f3z absent", []string{"step", "iges", "f3d"}, "f3d", true},
		{"case and space tolerant", []string{" F3Z "}, "f3z", true},
		{"neither native format", []string{"step", "stl", "pdf"}, "", false},
		{"empty list", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PickArchiveFormat(tc.formats)
			if got != tc.want || ok != tc.wantOK {
				t.Errorf("PickArchiveFormat(%v) = (%q, %v), want (%q, %v)",
					tc.formats, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestDownloadFormats(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
		}
		_, _ = io.WriteString(w, `{"data":{"attributes":{"formats":[
			{"fileType":"F3Z"},{"fileType":"step"},{"fileType":""}]}}}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	got, err := DownloadFormats(context.Background(), "tok", "b.proj", "urn:v1")
	if err != nil {
		t.Fatalf("DownloadFormats: %v", err)
	}
	// Lowercased on the way out so callers never have to case-fold.
	want := []string{"f3z", "step"}
	if len(got) != len(want) {
		t.Fatalf("formats = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("formats[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !strings.HasSuffix(gotPath, "/downloadFormats") {
		t.Errorf("path = %q, want it to end in /downloadFormats", gotPath)
	}
}

func TestDownloadFormats_RejectsEmptyIDs(t *testing.T) {
	if _, err := DownloadFormats(context.Background(), "tok", "", "urn:v1"); err == nil {
		t.Error("empty project: want error, got nil")
	}
	if _, err := DownloadFormats(context.Background(), "tok", "b.proj", ""); err == nil {
		t.Error("empty version: want error, got nil")
	}
}

func TestCreateDownload_PayloadAndJobID(t *testing.T) {
	var body []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"data":{"type":"jobs","id":"job-42"}}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	jobIDs, err := CreateDownload(context.Background(), "tok", "b.proj", "urn:v1", "f3z")
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	if len(jobIDs) != 1 || jobIDs[0] != "job-42" {
		t.Errorf("jobIDs = %v, want [job-42]", jobIDs)
	}

	// The JSON:API envelope is fussy; assert the fields APS actually keys off.
	var sent struct {
		Data struct {
			Type       string `json:"type"`
			Attributes struct {
				Format struct {
					FileType string `json:"fileType"`
				} `json:"format"`
			} `json:"attributes"`
			Relationships struct {
				Source struct {
					Data struct {
						Type string `json:"type"`
						ID   string `json:"id"`
					} `json:"data"`
				} `json:"source"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &sent); err != nil {
		t.Fatalf("decoding sent payload: %v (body %s)", err, body)
	}
	if sent.Data.Type != "downloads" {
		t.Errorf("data.type = %q, want %q", sent.Data.Type, "downloads")
	}
	if sent.Data.Attributes.Format.FileType != "f3z" {
		t.Errorf("format.fileType = %q, want %q", sent.Data.Attributes.Format.FileType, "f3z")
	}
	if sent.Data.Relationships.Source.Data.Type != "versions" {
		t.Errorf("source.type = %q, want %q", sent.Data.Relationships.Source.Data.Type, "versions")
	}
	if sent.Data.Relationships.Source.Data.ID != "urn:v1" {
		t.Errorf("source.id = %q, want %q", sent.Data.Relationships.Source.Data.ID, "urn:v1")
	}
}

// Live APS returns `data` as an ARRAY from POST /downloads — one entry per job
// it started. Decoding straight into a struct failed on real traffic with
// "cannot unmarshal array into Go struct field", so both shapes are pinned here.
func TestCreateDownload_AcceptsDataArray(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"jsonapi":{"version":"1.0"},"data":[
			{"type":"jobs","id":"job-42","attributes":{"status":"queued"}}]}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	jobIDs, err := CreateDownload(context.Background(), "tok", "b.proj", "urn:v1", "f3z")
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	if len(jobIDs) != 1 || jobIDs[0] != "job-42" {
		t.Errorf("jobIDs = %v, want [job-42]", jobIDs)
	}
}

func TestCreateDownload_PicksTheJobFromAMixedArray(t *testing.T) {
	// A design with external references starts several jobs, and the document
	// can carry other resource types alongside them.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"data":[
			{"type":"downloads","id":"dl-should-not-be-polled"},
			{"type":"jobs","id":"job-7"}]}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	jobIDs, err := CreateDownload(context.Background(), "tok", "b.proj", "urn:v1", "f3z")
	if err != nil {
		t.Fatalf("CreateDownload: %v", err)
	}
	// The "downloads" entry beside it must not be mistaken for something to poll.
	if len(jobIDs) != 1 || jobIDs[0] != "job-7" {
		t.Errorf("jobIDs = %v, want only the jobs resource [job-7]", jobIDs)
	}
}

func TestCreateDownload_EmptyDataIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, `{"data":[]}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	if _, err := CreateDownload(context.Background(), "tok", "b.proj", "urn:v1", "f3z"); err == nil {
		t.Error("empty data array: want error, got nil")
	}
}

func TestDataResources_BothShapes(t *testing.T) {
	cases := []struct {
		name    string
		body    string
		wantLen int
		wantID  string
	}{
		{"object", `{"data":{"type":"jobs","id":"j1"}}`, 1, "j1"},
		{"array", `{"data":[{"type":"jobs","id":"j1"}]}`, 1, "j1"},
		{"array of two", `{"data":[{"type":"jobs","id":"j1"},{"type":"jobs","id":"j2"}]}`, 2, "j1"},
		{"null data", `{"data":null}`, 0, ""},
		{"absent data", `{"jsonapi":{"version":"1.0"}}`, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := dataResources([]byte(tc.body))
			if err != nil {
				t.Fatalf("dataResources: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tc.wantLen)
			}
			if tc.wantID != "" && got[0].ID != tc.wantID {
				t.Errorf("first id = %q, want %q", got[0].ID, tc.wantID)
			}
		})
	}
}

func TestDataResources_DecodeErrorCarriesTheBody(t *testing.T) {
	// The original failure was undiagnosable because the error said only what
	// Go expected, never what APS sent.
	_, err := dataResources([]byte(`{"data":"a string"}`))
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "a string") {
		t.Errorf("error = %v, want it to quote the body", err)
	}
}

// The job id APS returns is a JOB id, not a download id: it base64-decodes to
// "business:<hub>#<project>#export:<n>", and asking /downloads/ for it answers
// 404 UNKNOWN_ENTITY. The download's own id is revealed only by the Location
// header of the /jobs/ redirect, which is why this polls /jobs/.
func TestPollDownloadJob_PollsTheJobsEndpoint(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = io.WriteString(w, `{"data":{"type":"jobs","id":"j","attributes":{"status":"processing"}}}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	if _, done, err := PollDownloadJob(context.Background(), "tok", "b.proj", "job-42"); err != nil || done {
		t.Fatalf("= (done %v, err %v), want not-done", done, err)
	}
	if !strings.Contains(gotPath, "/jobs/") {
		t.Errorf("path = %q, want the /jobs/{jobId} endpoint", gotPath)
	}
}

func TestPollDownloadJob_303YieldsTheDownloadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/data/v1/projects/b.proj/downloads/dl-7")
		w.WriteHeader(http.StatusSeeOther)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	got, done, err := PollDownloadJob(context.Background(), "tok", "b.proj", "job-42")
	if err != nil {
		t.Fatalf("PollDownloadJob: %v", err)
	}
	// The Location is used verbatim (resolved against the DM base): the
	// download's id appears nowhere else, so it must not be reconstructed.
	if !done || got != srv.URL+"/data/v1/projects/b.proj/downloads/dl-7" {
		t.Errorf("= (%q, %v), want the resolved download URL", got, done)
	}
}

func TestPollDownloadJob_TerminalFailures(t *testing.T) {
	for _, status := range []string{"failed", "cancelled", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, `{"data":{"type":"jobs","id":"j","attributes":{"status":"`+status+`"}}}`)
			}))
			defer srv.Close()
			defer dmBaseURLForTest(srv.URL)()

			if _, done, err := PollDownloadJob(context.Background(), "tok", "b.proj", "j"); err == nil || done {
				t.Errorf("status %q = (done %v, err %v), want an error", status, done, err)
			}
		})
	}
}

func TestDownloadURLFromLocation_RefusesOffHost(t *testing.T) {
	defer dmBaseURLForTest("https://developer.api.autodesk.com")()
	// The returned URL is fetched WITH the bearer token, and a redirect target
	// is upstream-controlled data — so another host must never be followed.
	for _, loc := range []string{
		"https://evil.example/data/v1/projects/b.p1/downloads/dl-1",
		"http://developer.api.autodesk.com/data/v1/projects/b.p1/downloads/dl-1",
		"/data/v1/projects/b.p1/jobs/j-9",
		"",
	} {
		if got, ok := downloadURLFromLocation(loc); ok {
			t.Errorf("downloadURLFromLocation(%q) = (%q, true), want refusal", loc, got)
		}
	}
	const good = "/data/v1/projects/b.p1/downloads/dl-1"
	if got, ok := downloadURLFromLocation(good); !ok ||
		got != "https://developer.api.autodesk.com"+good {
		t.Errorf("downloadURLFromLocation(%q) = (%q, %v), want it resolved", good, got, ok)
	}
}

// The observed live "object id" — the RFC 5987 filename leaking into the urn.
const observedKey = "UTF-8%27%27975b0591-2d3c-4271-99d7-eceb604253ff.f3d"

func TestArchiveObjectKeys_EnumeratesEverySpelling(t *testing.T) {
	got := archiveObjectKeys(observedKey)
	want := []string{
		// escaped exactly once — what DM itself would have sent
		"UTF-8%27%27975b0591-2d3c-4271-99d7-eceb604253ff.f3d",
		// verbatim: apostrophes are legal in a path segment, and this is the
		// spelling the earlier attempts never tried
		"UTF-8''975b0591-2d3c-4271-99d7-eceb604253ff.f3d",
		// without the RFC 5987 prefix
		"975b0591-2d3c-4271-99d7-eceb604253ff.f3d",
		// as arrived — double-escaped; the original behaviour, kept last
		"UTF-8%2527%2527975b0591-2d3c-4271-99d7-eceb604253ff.f3d",
	}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %d spellings", got, len(want))
	}
	for i := range want {
		if got[i].path != want[i] {
			t.Errorf("key[%d] = %q (%s), want %q", i, got[i].path, got[i].why, want[i])
		}
		if got[i].why == "" {
			t.Errorf("key[%d] has no label; the trace must say WHICH spelling worked", i)
		}
	}
}

func TestArchiveObjectKeys_PlainKeyIsTriedOnce(t *testing.T) {
	// An ordinary key has nothing to decode and no prefix to strip, so it must
	// not produce four identical requests.
	got := archiveObjectKeys("abc123.f3z")
	if len(got) != 1 || got[0].path != "abc123.f3z" {
		t.Errorf("keys = %v, want exactly one (abc123.f3z)", got)
	}
}

func TestSignArchiveObject_TriesUntilOneResolves(t *testing.T) {
	var tried []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// EscapedPath, not Path: the escaping is the whole point, and Path has
		// already been decoded by the time a handler sees it.
		esc := r.URL.EscapedPath()
		tried = append(tried, esc)
		// Only the verbatim spelling resolves here.
		if strings.Contains(esc, "UTF-8''") {
			_, _ = io.WriteString(w, `{"url":"https://s3.example/signed?sig=abc"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"reason":"Object not found"}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	got, err := signArchiveObject(context.Background(), "tok",
		"urn:adsk.objects:os.object:wip.dm.prod/"+observedKey)
	if err != nil {
		t.Fatalf("signArchiveObject: %v", err)
	}
	if got != "https://s3.example/signed?sig=abc" {
		t.Errorf("signed url = %q", got)
	}
	if len(tried) != 2 {
		t.Errorf("made %d attempts (%v), want 2 (stops at the first that resolves)", len(tried), tried)
	}
}

func TestSignArchiveObject_AllSpellingsFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"reason":"Object not found"}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	_, err := signArchiveObject(context.Background(), "tok",
		"urn:adsk.objects:os.object:wip.dm.prod/"+observedKey)
	if err == nil {
		t.Fatal("every spelling 404d: want an error, got nil")
	}
	// The upstream status must survive, so handleArchiveFile can classify it.
	if !strings.Contains(err.Error(), "HTTP 404") {
		t.Errorf("error = %v, want it to carry the upstream status", err)
	}
	// And every spelling tried must be named — a download failure has to be
	// diagnosable from the ordinary log, without re-running it under -v.
	for _, why := range []string{"decoded+escaped", "decoded-verbatim", "prefix-stripped", "as-arrived"} {
		if !strings.Contains(err.Error(), why) {
			t.Errorf("error = %v, want it to name the %q attempt", err, why)
		}
	}
}

func TestSignedURLFrom_AcceptsUrlOrUrls(t *testing.T) {
	// OSS returns a single `url`, or `urls` for a part-signed object.
	for _, body := range []string{
		`{"url":"https://s3.example/one"}`,
		`{"urls":["https://s3.example/one","https://s3.example/two"]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body)
		}))
		got, err := signedURLFrom(context.Background(), "tok", srv.URL+"/sign")
		srv.Close()
		if err != nil {
			t.Fatalf("signedURLFrom(%s): %v", body, err)
		}
		if got != "https://s3.example/one" {
			t.Errorf("signedURLFrom(%s) = %q, want the first url", body, got)
		}
	}
}

// The document APS actually returns. The bytes are behind
// relationships.storage.meta.link.href — a pre-signed CDN url — and the
// "object id" beside it is NOT an OSS key: it is the RFC 5987 filename from the
// same response's content-disposition leaking into the urn, which is why every
// spelling of it answered "Object not found".
const liveDownloadDoc = `{"jsonapi":{"version":"1.0"},"data":[{
	"type":"downloads",
	"id":"YnVzaW5lc3M6aW1hbGxj",
	"attributes":{"format":{"fileType":"f3d"},"name":"Widget.f3d"},
	"links":{"self":{"href":"https://developer.api.autodesk.com/data/v1/projects/p/downloads/d"}},
	"relationships":{"storage":{
		"data":{"type":"objects","id":"urn:adsk.objects:os.object:wip.dm.prod/UTF-8%27%27479f6974.f3d"},
		"meta":{"link":{"href":"https://cdn.us.oss.api.autodesk.com/oss/v2/signedresources/dc82f5fe?region=US"}}}}}]}`

func TestResolveDownload_PrefersThePreSignedLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, liveDownloadDoc)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	got, raw, err := ResolveDownload(context.Background(), "tok", srv.URL+"/data/v1/projects/p/downloads/d")
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if got.Link != "https://cdn.us.oss.api.autodesk.com/oss/v2/signedresources/dc82f5fe?region=US" {
		t.Errorf("Link = %q, want the pre-signed CDN url", got.Link)
	}
	// The storage urn is still carried, but only as a fallback.
	if got.StorageURN == "" {
		t.Error("StorageURN was dropped; it is the fallback when no link is offered")
	}
	if got.Name != "Widget.f3d" {
		t.Errorf("Name = %q, want %q", got.Name, "Widget.f3d")
	}
	// What APS BUILT, which the caller names the saved file from — this
	// document reports f3d even though the request that produced it asked for
	// f3z, which is exactly the case the field exists for.
	if got.FileType != "f3d" {
		t.Errorf("FileType = %q, want %q", got.FileType, "f3d")
	}
	if len(raw) == 0 {
		t.Error("raw document not returned; it is what makes a later failure diagnosable")
	}
}

func TestResolveDownload_RefusesAnOffHostLink(t *testing.T) {
	// The server fetches this url and streams the result to a browser, so an
	// upstream-controlled address is confined to Autodesk's own hosts.
	for _, href := range []string{
		"https://evil.example/oss/v2/signedresources/x",
		"https://notautodesk.com/x",
		"https://autodesk.com.evil.net/x",
		"http://cdn.us.oss.api.autodesk.com/x",
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"data":[{"type":"downloads","relationships":{"storage":{
				"data":{"id":"urn:adsk.objects:os.object:b/o"},
				"meta":{"link":{"href":"`+href+`"}}}}}]}`)
		}))
		defer dmBaseURLForTest(srv.URL)()
		_, _, err := ResolveDownload(context.Background(), "tok", srv.URL+"/d")
		srv.Close()
		if err == nil {
			t.Errorf("link %q was accepted, want refusal", href)
		}
	}
}

func TestAutodeskHost(t *testing.T) {
	for _, ok := range []string{
		"https://cdn.us.oss.api.autodesk.com/x",
		"https://developer.api.autodesk.com/x",
		"https://autodesk.com/x",
	} {
		if !isAutodeskHost(ok) {
			t.Errorf("autodeskHost(%q) = false, want true", ok)
		}
	}
	for _, bad := range []string{
		"https://notautodesk.com/x",
		"https://autodesk.com.evil.net/x",
		"http://autodesk.com/x", // plain http
		"ftp://autodesk.com/x",
		"not a url at all::",
		"",
	} {
		if isAutodeskHost(bad) {
			t.Errorf("autodeskHost(%q) = true, want false", bad)
		}
	}
}

func TestOpenArchive_StreamsFromThePreSignedLink(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/signed"):
			// Pre-signed: it carries its own auth, so attaching ours would
			// leak the user's APS token to whoever the link named.
			if auth := r.Header.Get("Authorization"); auth != "" {
				t.Errorf("pre-signed fetch sent Authorization %q, want none", auth)
			}
			_, _ = io.WriteString(w, "F3D-BYTES")
		case strings.Contains(r.URL.Path, "signeds3download"):
			t.Error("signed the OSS object even though a pre-signed link was offered")
			w.WriteHeader(http.StatusNotFound)
		default:
			// The download document, pointing at this same server's /signed.
			_, _ = io.WriteString(w, `{"data":[{"type":"downloads",
				"attributes":{"name":"Widget.f3d"},
				"relationships":{"storage":{
					"data":{"id":"urn:adsk.objects:os.object:wip.dm.prod/UTF-8%27%27x.f3d"},
					"meta":{"link":{"href":"`+srv.URL+`/signed/abc"}}}}}]}`)
		}
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()
	// The host guard is the production rule; point it at the test server.
	defer autodeskHostForTest(func(string) bool { return true })()

	resp, target, err := OpenArchive(context.Background(), "tok", srv.URL+"/data/v1/projects/p/downloads/d")
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "F3D-BYTES" {
		t.Errorf("body = %q, want %q", body, "F3D-BYTES")
	}
	if target.Name != "Widget.f3d" {
		t.Errorf("name = %q, want %q", target.Name, "Widget.f3d")
	}
}

func TestOpenArchive_FallsBackToSigningWhenNoLink(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/object"):
			_, _ = io.WriteString(w, "F3Z-BYTES")
		case strings.Contains(r.URL.Path, "signeds3download"):
			_, _ = io.WriteString(w, `{"url":"`+srv.URL+`/object?sig=abc"}`)
		default:
			// No meta.link — only the storage urn.
			_, _ = io.WriteString(w, `{"data":[{"type":"downloads",
				"attributes":{"name":"Widget.f3z"},
				"relationships":{"storage":{"data":{"id":"urn:adsk.objects:os.object:bkt/obj.f3z"}}}}]}`)
		}
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	resp, _, err := OpenArchive(context.Background(), "tok", srv.URL+"/data/v1/projects/p/downloads/d")
	if err != nil {
		t.Fatalf("OpenArchive: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "F3Z-BYTES" {
		t.Errorf("body = %q, want the signed-object fallback to have run", body)
	}
}

// The extension is decided from the LINK, because attributes.format.fileType
// has been seen absent for a download whose link said .f3d — and comparing
// only the attribute meant the rename silently did not fire, so the file went
// out named .f3z while Autodesk's own web download of the same design gave
// .f3d.
func TestFileTypeFromLink(t *testing.T) {
	cases := []struct {
		name, link, want string
	}{
		{
			"rfc5987 filename*, percent-encoded (the live shape)",
			"https://cdn.us.oss.api.autodesk.com/oss/v2/signedresources/abc" +
				"?region=US&response-content-disposition=attachment%3Bfilename%2A%3DUTF-8%27%27479f6974.f3d",
			"f3d",
		},
		{
			"plain filename=",
			"https://cdn.us.oss.api.autodesk.com/x?response-content-disposition=" +
				url.QueryEscape(`attachment;filename="Widget.f3z"`),
			"f3z",
		},
		{"no disposition", "https://cdn.us.oss.api.autodesk.com/x?region=US", ""},
		{"no extension in the name", "https://x/y?response-content-disposition=" +
			url.QueryEscape("attachment;filename*=UTF-8''noext"), ""},
		{"not a url", "://nope", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := fileTypeFromLink(tc.link); got != tc.want {
				t.Errorf("fileTypeFromLink = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFileTypeFromObjectURN(t *testing.T) {
	// The key is percent-encoded and carries the RFC 5987 prefix; the
	// extension still has to come out.
	got := fileTypeFromObjectURN(
		"urn:adsk.objects:os.object:wip.dm.prod/UTF-8%27%27479f6974.f3d")
	if got != "f3d" {
		t.Errorf("= %q, want f3d", got)
	}
	if got := fileTypeFromObjectURN("not-a-urn"); got != "" {
		t.Errorf("unparseable urn = %q, want empty", got)
	}
}

func TestResolveDownload_LinkBeatsAnAbsentDeclaredType(t *testing.T) {
	// Exactly the live case: no attributes.format.fileType at all, and a link
	// that says f3d. The old code compared only the attribute, found nothing,
	// and left the file named after the REQUESTED format.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"data":[{"type":"downloads",
			"attributes":{},
			"relationships":{"storage":{
				"data":{"id":"urn:adsk.objects:os.object:wip.dm.prod/UTF-8%27%27abc.f3d"},
				"meta":{"link":{"href":"https://cdn.us.oss.api.autodesk.com/oss/v2/signedresources/x?region=US&response-content-disposition=attachment%3Bfilename%2A%3DUTF-8%27%27abc.f3d"}}}}}]}`)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()

	got, _, err := ResolveDownload(context.Background(), "tok", srv.URL+"/d")
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if got.FileType != "f3d" {
		t.Errorf("FileType = %q, want f3d (from the link)", got.FileType)
	}
	if got.Declared != "" {
		t.Errorf("Declared = %q, want empty — the attribute was absent", got.Declared)
	}
	// The evidence is kept so a disagreement can be logged rather than resolved
	// silently.
	if got.LinkType != "f3d" || got.ObjectType != "f3d" {
		t.Errorf("sources = link:%q object:%q, want both f3d", got.LinkType, got.ObjectType)
	}
}
