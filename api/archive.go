package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Archiving a Fusion design means asking APS to *generate* a native archive and
// then downloading what it produced. This is a different thing from api/files.go,
// which serves an uploaded file's own bytes: a Fusion-native design has no
// downloadable storage object of its own (see the note at the top of files.go),
// but the Data Management API will build one on request.
//
// The sequence, all on data/v1:
//
//	GET  /projects/{p}/versions/{v}/downloadFormats  -> which formats this version can produce
//	POST /projects/{p}/downloads                     -> 202, a job id
//	GET  /projects/{p}/jobs/{job}                    -> 200 while working, 303 + Location when done
//	GET  /projects/{p}/downloads/{download}          -> an OSS storage urn
//
// The storage urn is then handed to signedS3DownloadURL (files.go), so the last
// mile is the same code path every other download in this app uses.
//
// Generation is genuinely slow — minutes for a large assembly — which is why the
// server runs this as a background job rather than inside a request.

// Archive file types, lowercase as APS reports and expects them.
const (
	// ArchiveF3Z is the Fusion archive *with* its external references, and the
	// only format a design that has any can be produced in.
	ArchiveF3Z = "f3z"
	// ArchiveF3D is the single-document Fusion archive.
	ArchiveF3D = "f3d"
)

// archivePreference is the order we take native formats in when a version
// offers more than one. F3Z first: it is the lossless choice for anything with
// references, and Fusion opens it exactly like an F3D for anything without.
var archivePreference = []string{ArchiveF3Z, ArchiveF3D}

// PickArchiveFormat chooses the native archive format to request from the list a
// version actually reported. Returns false when the version offers neither —
// which does happen (a drawing, or a design still processing), and is a real
// answer rather than an error: the caller turns it into a specific message
// instead of letting a POST fail with an opaque 400.
func PickArchiveFormat(formats []string) (string, bool) {
	have := make(map[string]bool, len(formats))
	for _, f := range formats {
		have[strings.ToLower(strings.TrimSpace(f))] = true
	}
	for _, want := range archivePreference {
		if have[want] {
			return want, true
		}
	}
	return "", false
}

// DownloadFormats lists the file types a version can be generated in. This is
// what decides F3Z vs F3D — never the file extension, which describes how the
// design was stored, not what APS is willing to build from it.
func DownloadFormats(ctx context.Context, token, dmProjectID, versionURN string) ([]string, error) {
	if dmProjectID == "" || versionURN == "" {
		return nil, fmt.Errorf("download formats: empty project or version")
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/versions/%s/downloadFormats",
		dmBaseURL, dmEscape(dmProjectID), dmEscape(versionURN))
	body, err := dmGet(ctx, token, u)
	if err != nil {
		return nil, fmt.Errorf("download formats: %w", err)
	}
	var doc struct {
		Data struct {
			Attributes struct {
				Formats []struct {
					FileType string `json:"fileType"`
				} `json:"formats"`
			} `json:"attributes"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("download formats decode: %w", err)
	}
	out := make([]string, 0, len(doc.Data.Attributes.Formats))
	for _, f := range doc.Data.Attributes.Formats {
		if f.FileType != "" {
			out = append(out, strings.ToLower(f.FileType))
		}
	}
	return out, nil
}

// CreateDownload kicks off generation of one format of one version and returns
// the job ids to poll. APS answers 202 with a JSON:API document of type "jobs"
// — the download itself does not exist yet.
//
// The result is a SLICE because live APS returns `data` as an array. In
// practice it holds one job (an F3Z already bundles a design's references into
// a single file), but the caller is handed all of them rather than the first,
// so a multi-job response can be noticed instead of silently half-polled.
func CreateDownload(ctx context.Context, token, dmProjectID, versionURN, fileType string) ([]string, error) {
	if dmProjectID == "" || versionURN == "" || fileType == "" {
		return nil, fmt.Errorf("create download: empty project, version or format")
	}
	payload := map[string]any{
		"jsonapi": map[string]any{"version": "1.0"},
		"data": map[string]any{
			"type":       "downloads",
			"attributes": map[string]any{"format": map[string]any{"fileType": fileType}},
			"relationships": map[string]any{
				"source": map[string]any{
					"data": map[string]any{"type": "versions", "id": versionURN},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/downloads", dmBaseURL, dmEscape(dmProjectID))
	resp, err := dmPost(ctx, token, u, body)
	if err != nil {
		return nil, fmt.Errorf("create download: %w", err)
	}
	dbgLog("ARCHIVE create downloads (%s) -> %s", fileType, resp)
	// Live APS answers this one with `data` as an ARRAY; dataResources
	// normalizes both shapes.
	entries, err := dataResources(resp)
	if err != nil {
		return nil, fmt.Errorf("create download: %w", err)
	}
	ids := resourceIDs(entries, "jobs")
	if len(ids) == 0 && len(entries) == 1 && entries[0].ID != "" {
		// Fall back to the single resource that came back before giving up:
		// the id is the only thing we need, and a renamed type should not
		// break the feature.
		ids = []string{entries[0].ID}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("create download: no job id in response: %s", snippet(resp))
	}
	return ids, nil
}

// jsonAPIResource is the subset of a JSON:API resource object this file needs.
type jsonAPIResource struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	Attributes    json.RawMessage `json:"attributes"`
	Relationships json.RawMessage `json:"relationships"`
}

// dataResources reads a JSON:API document's `data` member as a list, whichever
// shape it arrived in.
//
// The spec allows `data` to be a single resource object OR an array of them,
// and the download endpoints use BOTH: the create call returns an array (one
// job per generated file), while the job and download reads return an object.
// Decoding straight into a struct therefore fails on real traffic with
// "cannot unmarshal array into Go struct field", which is exactly what it did.
func dataResources(body []byte) ([]jsonAPIResource, error) {
	var doc struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("decode: %w (body: %s)", err, snippet(body))
	}
	trimmed := bytes.TrimSpace(doc.Data)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if trimmed[0] == '[' {
		var many []jsonAPIResource
		if err := json.Unmarshal(trimmed, &many); err != nil {
			return nil, fmt.Errorf("decode data array: %w (body: %s)", err, snippet(body))
		}
		return many, nil
	}
	var one jsonAPIResource
	if err := json.Unmarshal(trimmed, &one); err != nil {
		return nil, fmt.Errorf("decode data: %w (body: %s)", err, snippet(body))
	}
	return []jsonAPIResource{one}, nil
}

// pickResource returns the first resource of the wanted type.
func pickResource(entries []jsonAPIResource, wantType string) (id string, found bool) {
	for _, e := range entries {
		if strings.EqualFold(e.Type, wantType) && e.ID != "" {
			return e.ID, true
		}
	}
	return "", false
}

// resourceIDs returns every id of the wanted type, in document order.
func resourceIDs(entries []jsonAPIResource, wantType string) []string {
	var out []string
	for _, e := range entries {
		if strings.EqualFold(e.Type, wantType) && e.ID != "" {
			out = append(out, e.ID)
		}
	}
	return out
}

// snippet bounds a response body for an error message. Bodies from these
// endpoints carry no credentials (the signed url comes from a different call),
// but they can be large, so this keeps a decode failure diagnosable without
// dumping a whole document into the log.
func snippet(body []byte) string { return snippetN(body, 512) }

// snippetN is snippet with an explicit cap, for the one error that needs the
// WHOLE document rather than its opening: a signing failure, where the part
// that was cut off is exactly the part nobody has been able to see.
func snippetN(body []byte, max int) string {
	s := strings.TrimSpace(string(body))
	if len(s) > max {
		return s[:max] + "…"
	}
	return s
}

// PollDownloadJob reports on a generation job. While the job runs APS answers
// 200 with a "jobs" document; when it finishes it answers 303 with the finished
// download's URI in Location. Returns done=false (and no error) for every
// in-progress state, so the caller's loop is a plain "until done".
//
// It polls /jobs/{jobId} and NOT /downloads/{jobId}. That distinction cost a
// round of debugging in both directions, so it is worth stating plainly: the id
// the create call returns is a JOB id (it base64-decodes to
// "business:<hub>#<project>#export:<n>"), and asking /downloads/ for it gets a
// 404 UNKNOWN_ENTITY "Download not found". The download has its own id, and the
// only place it is ever revealed is the Location header of this redirect.
//
// On completion it returns the download's URL rather than an id parsed out of
// it — APS has said exactly where the finished download lives, and rebuilding
// that address is a needless chance to get it wrong.
func PollDownloadJob(ctx context.Context, token, dmProjectID, jobID string) (downloadURL string, done bool, err error) {
	if dmProjectID == "" || jobID == "" {
		return "", false, fmt.Errorf("download job: empty project or job")
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/jobs/%s",
		dmBaseURL, dmEscape(dmProjectID), dmEscape(jobID))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.api+json")

	// The 303 is the completion signal, so it must not be followed: a followed
	// redirect would collapse "finished" and "still working" into two 200s that
	// differ only by payload shape.
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("download job: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	dbgLog("ARCHIVE poll job %s -> HTTP %d location=%q body=%s",
		jobID, resp.StatusCode, resp.Header.Get("Location"), body)

	if resp.StatusCode == http.StatusSeeOther || resp.StatusCode == http.StatusFound {
		loc := resp.Header.Get("Location")
		abs, ok := downloadURLFromLocation(loc)
		if !ok {
			return "", false, fmt.Errorf("download job: unusable completion location %q", loc)
		}
		return abs, true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, fmt.Errorf("download job %s -> HTTP %d: %s",
			trimURL(u), resp.StatusCode, strings.TrimSpace(string(body)))
	}

	entries, err := dataResources(body)
	if err != nil {
		return "", false, fmt.Errorf("download job: %w", err)
	}
	for _, e := range entries {
		var attrs struct {
			Status string `json:"status"`
		}
		if len(e.Attributes) > 0 {
			_ = json.Unmarshal(e.Attributes, &attrs)
		}
		switch strings.ToLower(attrs.Status) {
		case "failed", "cancelled", "canceled":
			return "", false, fmt.Errorf("download job: APS reported the generation %s", attrs.Status)
		}
	}
	return "", false, nil
}

// downloadURLFromLocation turns a job's completion Location header into an
// absolute URL to fetch, resolving a relative one against the DM base.
//
// It REFUSES a Location pointing at any other host. The returned URL is fetched
// with the user's bearer token attached, so following an off-host redirect
// would hand that token to whoever the redirect named — and a redirect target
// is upstream-controlled data, not something we chose.
func downloadURLFromLocation(loc string) (string, bool) {
	loc = strings.TrimSpace(loc)
	if loc == "" {
		return "", false
	}
	base, err := url.Parse(dmBaseURL)
	if err != nil {
		return "", false
	}
	ref, err := url.Parse(loc)
	if err != nil {
		return "", false
	}
	abs := base.ResolveReference(ref)
	if abs.Scheme != base.Scheme || abs.Host != base.Host {
		return "", false
	}
	if !strings.Contains(abs.Path, "/downloads/") {
		return "", false
	}
	return abs.String(), true
}

// ArchiveTarget is where a finished archive's bytes actually are.
//
// Link is the whole point. APS puts a READY-SIGNED url on the storage
// relationship's meta:
//
//	relationships.storage.meta.link.href =
//	  https://cdn.us.oss.api.autodesk.com/oss/v2/signedresources/<uuid>?region=US&…
//
// Nothing has to be signed, and the OSS object key must not be used: the
// "object id" alongside it is not an OSS key at all. It is the RFC 5987
// filename from the same response's content-disposition
// (filename*=UTF-8”<uuid>.f3d) leaking into the urn — which is why every
// spelling of it answered "Object not found". The storage urn is kept only as
// a fallback for a response that omits the link.
//
// FileType is what APS says it actually BUILT, which is not always what was
// asked for: a version whose downloadFormats offered f3z has been observed
// producing a download whose format.fileType is f3d. The caller names the saved
// file from this rather than from its own request, because an f3z is a zip
// container — handing a browser an f3d under an .f3z name produces a file
// Fusion may refuse to open.
type ArchiveTarget struct {
	Link       string // pre-signed url; use this when present
	StorageURN string // fallback: sign it ourselves
	Name       string
	FileType   string // the decided extension, lowercase, no dot; "" if unknown
	// The evidence FileType was decided from, kept so a disagreement can be
	// logged rather than silently resolved. All three agreed in the one
	// document ever captured; the live mismatch that prompted this had
	// Declared empty while the link said f3d.
	Declared   string // attributes.format.fileType
	LinkType   string // extension in the link's response-content-disposition
	ObjectType string // extension of the storage object key
}

// ResolveDownload reads a finished download and reports where its bytes are.
// downloadURL is what PollDownloadJob returned.
func ResolveDownload(ctx context.Context, token, downloadURL string) (ArchiveTarget, []byte, error) {
	if downloadURL == "" {
		return ArchiveTarget{}, nil, fmt.Errorf("download details: no download url")
	}
	body, err := dmGetJSONAPI(ctx, token, downloadURL)
	if err != nil {
		return ArchiveTarget{}, nil, fmt.Errorf("download details: %w", err)
	}
	// The WHOLE document. Every surprise in this chain has been a shape that
	// was not predicted — including the link this function now reads, which
	// sat unexamined behind a 512-character truncation for several rounds.
	dbgLog("ARCHIVE download details %s -> %s", trimURL(downloadURL), body)

	entries, err := dataResources(body)
	if err != nil {
		return ArchiveTarget{}, body, fmt.Errorf("download details: %w", err)
	}
	for _, e := range entries {
		var rel struct {
			Storage struct {
				Data struct {
					ID string `json:"id"`
				} `json:"data"`
				Meta struct {
					Link struct {
						Href string `json:"href"`
					} `json:"link"`
				} `json:"meta"`
			} `json:"storage"`
		}
		if len(e.Relationships) > 0 {
			_ = json.Unmarshal(e.Relationships, &rel)
		}
		link := strings.TrimSpace(rel.Storage.Meta.Link.Href)
		if link == "" && rel.Storage.Data.ID == "" {
			continue
		}
		if link != "" && !autodeskHost(link) {
			// The server fetches this url and streams the result to a browser.
			// It is upstream-controlled data, so it is confined to Autodesk's
			// own hosts rather than trusted because it arrived over TLS.
			return ArchiveTarget{}, body, fmt.Errorf("download details: refusing off-host link %q", trimURL(link))
		}
		var attrs struct {
			Name   string `json:"name"`
			Format struct {
				FileType string `json:"fileType"`
			} `json:"format"`
		}
		if len(e.Attributes) > 0 {
			_ = json.Unmarshal(e.Attributes, &attrs)
		}
		declared := strings.ToLower(strings.TrimSpace(attrs.Format.FileType))
		linkType := fileTypeFromLink(link)
		objectType := fileTypeFromObjectURN(rel.Storage.Data.ID)
		return ArchiveTarget{
			Link:       link,
			StorageURN: rel.Storage.Data.ID,
			Name:       attrs.Name,
			// The LINK wins. Its response-content-disposition is what APS
			// serves these bytes as — the same name Fusion Team's own web
			// download produces — whereas attributes.format.fileType has been
			// seen absent for a download the link called .f3d. Declared is
			// second because it is at least explicit; the object key is last
			// because it is the field that already turned out to be a
			// content-disposition filename in disguise.
			FileType:   firstNonEmpty(linkType, declared, objectType),
			Declared:   declared,
			LinkType:   linkType,
			ObjectType: objectType,
		}, body, nil
	}
	return ArchiveTarget{}, body, fmt.Errorf("download %s has no storage: %s", trimURL(downloadURL), snippetN(body, 4<<10))
}

// fileTypeFromLink pulls the extension out of a signed link's
// response-content-disposition, e.g.
//
//	?response-content-disposition=attachment%3Bfilename*%3DUTF-8%27%27<uuid>.f3d
//
// This is the strongest signal available for what APS considers the file to be:
// it is literally the name APS would serve it under.
func fileTypeFromLink(rawLink string) string {
	u, err := url.Parse(rawLink)
	if err != nil {
		return ""
	}
	disp := u.Query().Get("response-content-disposition")
	if disp == "" {
		return ""
	}
	// filename*= (RFC 5987) or a plain filename=; take whichever is present.
	for _, key := range []string{"filename*=", "filename="} {
		i := strings.Index(disp, key)
		if i < 0 {
			continue
		}
		v := disp[i+len(key):]
		if j := strings.IndexAny(v, ";"); j >= 0 {
			v = v[:j]
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		v = strings.TrimPrefix(v, "UTF-8''")
		return bareExt(v)
	}
	return ""
}

// fileTypeFromObjectURN reads the extension off a storage object urn.
func fileTypeFromObjectURN(urn string) string {
	_, object, ok := parseOSSObjectURN(urn)
	if !ok {
		return ""
	}
	if d, err := url.QueryUnescape(object); err == nil {
		object = d
	}
	return bareExt(object)
}

// bareExt is extOf (queries.go) normalized for comparison with an APS
// fileType: lowercase and without the leading dot.
func bareExt(name string) string {
	return strings.ToLower(strings.TrimPrefix(extOf(name), "."))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// autodeskHost reports whether a url points at Autodesk. Matches the apex and
// any subdomain, and nothing that merely ends in the same letters
// ("notautodesk.com", "autodesk.com.evil.net").
//
// A var, not a func, only so tests can point the check at an httptest server —
// the same reason dmBaseURL is one. Production never reassigns it.
var autodeskHost = isAutodeskHost

// autodeskHostForTest relaxes the host guard and returns a restore func.
// Test-only.
func autodeskHostForTest(fn func(string) bool) func() {
	old := autodeskHost
	autodeskHost = fn
	return func() { autodeskHost = old }
}

func isAutodeskHost(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "autodesk.com" || strings.HasSuffix(h, ".autodesk.com")
}

// dmGetJSONAPI is dmGet with the JSON:API Accept header the downloads
// endpoints are documented against.
func dmGetJSONAPI(ctx context.Context, token, fullURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.api+json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("DM GET %s -> HTTP %d: %s",
			trimURL(fullURL), resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// signArchiveObject resolves a generated archive's storage urn to a signed S3
// url, working around how DM names these objects.
//
// The object key of a generated archive comes back carrying an RFC 5987
// filename prefix, already percent-encoded — observed live as:
//
//	urn:adsk.objects:os.object:wip.dm.prod/UTF-8%27%27<uuid>.f3d
//
// There is no documentation for which spelling of that OSS actually stores, and
// the wrong one answers "Object not found" with no hint. So every plausible
// spelling is tried, most likely first, and the one that resolves is logged so
// the answer stops being a guess after the first real run.
//
// This is a deliberate up-to-four-request worst case. An archive download is a
// rare, user-initiated action, and spending three extra 404s beats a feature
// that does not work.
func signArchiveObject(ctx context.Context, token, storageURN string) (string, error) {
	bucket, object, ok := parseOSSObjectURN(storageURN)
	if !ok {
		return "", fmt.Errorf("unrecognised storage urn")
	}
	var lastErr error
	var tried []string
	for _, key := range archiveObjectKeys(object) {
		u := fmt.Sprintf("%s/oss/v2/buckets/%s/objects/%s/signeds3download",
			dmBaseURL, dmEscape(bucket), key.path)
		signed, err := signedURLFrom(ctx, token, u)
		if err == nil {
			dbgLog("ARCHIVE sign: %s resolved (%s)", key.why, key.path)
			return signed, nil
		}
		dbgLog("ARCHIVE sign: %s failed (%s): %v", key.why, key.path, err)
		tried = append(tried, key.why+"="+key.path)
		lastErr = err
	}
	// Name every spelling in the error, not just the last one. Under -v the
	// trace above says the same thing, but a download failure has to be
	// diagnosable from the ordinary log — the operator does not get to re-run
	// a user's action with tracing on.
	return "", fmt.Errorf("no object-key spelling resolved in bucket %s (tried %s): %w",
		bucket, strings.Join(tried, ", "), lastErr)
}

// objectKey is one spelling of the object's path segment, with a label so the
// trace says WHICH one worked rather than just that something did.
type objectKey struct {
	path string
	why  string
}

// archiveObjectKeys enumerates the spellings of an object key to try, most
// likely first and without duplicates. The path segment is produced directly —
// not via a urn round-trip — because the whole question is how the segment is
// escaped, and hiding that behind a urn is what made this hard to reason about.
func archiveObjectKeys(object string) []objectKey {
	decoded := object
	if d, err := url.QueryUnescape(object); err == nil {
		decoded = d
	}
	stripped, hadPrefix := strings.CutPrefix(decoded, "UTF-8''")

	var out []objectKey
	seen := map[string]bool{}
	add := func(path, why string) {
		if path != "" && !seen[path] {
			seen[path] = true
			out = append(out, objectKey{path: path, why: why})
		}
	}

	// Escaped exactly once. If the urn arrives pre-encoded (as observed), this
	// reproduces what DM itself would have sent.
	add(dmEscape(decoded), "decoded+escaped")
	// Verbatim. Apostrophes are legal in a path segment, so if the key really
	// contains them this is what OSS matches on — and it is the one spelling
	// the earlier attempts never tried.
	add(decoded, "decoded-verbatim")
	if hadPrefix && stripped != "" {
		// In case the UTF-8'' prefix is DM leaking a Content-Disposition
		// parameter into the urn rather than being part of the key.
		add(dmEscape(stripped), "prefix-stripped")
	}
	// The original behaviour: escape whatever arrived. Correct for a key that
	// was never encoded, and for the pathological one holding a literal '%'.
	add(dmEscape(object), "as-arrived")
	return out
}

// signedURLFrom performs one signeds3download request and returns the signed
// url it names.
func signedURLFrom(ctx context.Context, token, signURL string) (string, error) {
	body, err := dmGet(ctx, token, signURL)
	if err != nil {
		return "", fmt.Errorf("sign download: %w", err)
	}
	var doc struct {
		URL  string   `json:"url"`
		URLs []string `json:"urls"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("sign download decode: %w", err)
	}
	if doc.URL != "" {
		return doc.URL, nil
	}
	// Large objects are signed per part; the first url is the whole object for
	// a single-part download.
	if len(doc.URLs) > 0 && doc.URLs[0] != "" {
		return doc.URLs[0], nil
	}
	return "", fmt.Errorf("sign download: no url in response: %s", snippet(body))
}

// OpenArchive opens an HTTP response streaming a finished archive's bytes. The
// caller owns resp.Body and must Close it.
//
// The signed url is resolved fresh on every call rather than stored: the
// signature expires in minutes, so a cached one would break the second download
// of the same archive. It also never leaves this process — the signed url is a
// bearer credential for the object, and no other download path in this app
// hands one to a browser either (see OpenFile in files.go).
// It returns the resolved target alongside the response so the caller can name
// the file from what APS built rather than from what it requested.
func OpenArchive(ctx context.Context, token, downloadURL string) (resp *http.Response, target ArchiveTarget, err error) {
	// Re-read the download rather than caching its link: the signature in it
	// expires, so a stored one would break the second download.
	target, doc, err := ResolveDownload(ctx, token, downloadURL)
	if err != nil {
		return nil, ArchiveTarget{}, err
	}
	signedURL := target.Link
	if signedURL == "" {
		// No pre-signed link: fall back to signing the storage object
		// ourselves. Not observed in practice, but the storage urn is the only
		// other thing the document offers.
		signedURL, err = signArchiveObject(ctx, token, target.StorageURN)
		if err != nil {
			return nil, ArchiveTarget{}, fmt.Errorf("%w (storage urn %q; download document: %s)",
				err, target.StorageURN, snippetN(doc, 16<<10))
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signedURL, nil)
	if err != nil {
		return nil, ArchiveTarget{}, err
	}
	resp, err = httpClient.Do(req) // pre-signed url carries its own auth; no Bearer header
	if err != nil {
		return nil, ArchiveTarget{}, fmt.Errorf("download archive: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, ArchiveTarget{}, fmt.Errorf("download archive -> HTTP %d", resp.StatusCode)
	}
	return resp, target, nil
}
