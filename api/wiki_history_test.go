package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestListWikiPageVersions walks a two-page version listing and checks the
// result is newest-first regardless of the order DM answered in.
func TestListWikiPageVersions(t *testing.T) {
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/versions") && r.URL.Query().Get("page[number]") == "":
			w.Write([]byte(`{"data":[
				{"type":"versions","id":"urn:adsk.wipprod:fs.file:vf.abc?version=1","attributes":{"versionNumber":1,"createTime":"2026-01-01T10:00:00Z","createUserName":"Ada"}},
				{"type":"versions","id":"urn:adsk.wipprod:fs.file:vf.abc?version=2","attributes":{"versionNumber":2,"lastModifiedTime":"2026-01-02T10:00:00Z","lastModifiedUserName":"Bob"}}
			],"links":{"next":{"href":"` + base + r.URL.Path + `?page[number]=2"}}}`))
		case strings.HasSuffix(r.URL.Path, "/versions"):
			w.Write([]byte(`{"data":[
				{"type":"versions","id":"urn:adsk.wipprod:fs.file:vf.abc?version=3","attributes":{"versionNumber":3,"createTime":"2026-01-03T10:00:00Z","createUserName":"Cy"}}
			]}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	base = srv.URL
	defer dmBaseURLForTest(srv.URL)()

	vs, err := ListWikiPageVersions(context.Background(), "tok", "b.proj", "urn:adsk.wipprod:dm.lineage:abc")
	if err != nil {
		t.Fatalf("ListWikiPageVersions: %v", err)
	}
	if len(vs) != 3 {
		t.Fatalf("got %d versions, want 3", len(vs))
	}
	for i, want := range []int{3, 2, 1} {
		if vs[i].Number != want {
			t.Errorf("vs[%d].Number = %d, want %d (newest first)", i, vs[i].Number, want)
		}
	}
	// The second version carried only lastModified* fields; they fill in.
	if vs[1].CreatedBy != "Bob" || vs[1].CreatedOn != "2026-01-02T10:00:00Z" {
		t.Errorf("v2 fallback fields = %q / %q", vs[1].CreatedBy, vs[1].CreatedOn)
	}
	if vs[0].CreatedBy != "Cy" {
		t.Errorf("v3 CreatedBy = %q, want Cy", vs[0].CreatedBy)
	}
}

// TestRestoreWikiPageVersion walks the copy-forward: read the item (name + tip),
// download the old version's bytes, and upload them as a new version under the
// item's *current* name — never touching the versions in between.
func TestRestoreWikiPageVersion(t *testing.T) {
	const (
		rootID     = "urn:adsk.wipprod:fs.folder:co.root"
		wikiID     = "urn:adsk.wipprod:fs.folder:co.wiki"
		itemID     = "urn:adsk.wipprod:dm.lineage:abc"
		oldVer     = "urn:adsk.wipprod:fs.file:vf.abc?version=1"
		tipVer     = "urn:adsk.wipprod:fs.file:vf.abc?version=3"
		newVer     = "urn:adsk.wipprod:fs.file:vf.abc?version=4"
		oldStorage = "urn:adsk.objects:os.object:wip.dm.prod/old.md"
		newStorage = "urn:adsk.objects:os.object:wip.dm.prod/new.md"
		oldBody    = "# The good version\n"
	)
	var base string
	var uploaded string
	var createdVersionBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path
		switch {
		// GET items/{id} → current name (renamed since v1) + tip
		case strings.HasSuffix(p, "/items/"+dmEscape(itemID)) || strings.HasSuffix(p, "/items/"+itemID):
			w.Write([]byte(`{"data":{"type":"items","id":"` + itemID + `","attributes":{"displayName":"renamed-page.md"},"relationships":{"tip":{"data":{"id":"` + tipVer + `"}}}}}`))
		// GET versions/{old} → storage of the version being restored
		case strings.Contains(p, "/versions/") && r.Method == http.MethodGet:
			w.Write([]byte(`{"data":{"attributes":{"name":"old-name.md"},"relationships":{"storage":{"data":{"id":"` + oldStorage + `"}}}}}`))
		case strings.Contains(p, "signeds3download"):
			w.Write([]byte(`{"url":"` + base + `/s3get"}`))
		case p == "/s3get":
			w.Write([]byte(oldBody))
		case strings.Contains(p, "topFolders"):
			w.Write([]byte(`{"data":[{"type":"folders","id":"` + rootID + `","attributes":{"name":"Root"}}]}`))
		case strings.Contains(p, "co.root"):
			w.Write([]byte(`{"data":[{"type":"folders","id":"` + wikiID + `","attributes":{"displayName":"Wiki"}}]}`))
		case strings.HasSuffix(p, "/storage"):
			w.Write([]byte(`{"data":{"id":"` + newStorage + `"}}`))
		case strings.Contains(p, "signeds3upload"):
			if r.Method == http.MethodGet {
				w.Write([]byte(`{"uploadKey":"k","urls":["` + base + `/s3put"]}`))
			} else {
				w.Write([]byte(`{}`))
			}
		case p == "/s3put" && r.Method == http.MethodPut:
			b, _ := io.ReadAll(r.Body)
			uploaded = string(b)
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(p, "/versions") && r.Method == http.MethodPost:
			b, _ := io.ReadAll(r.Body)
			createdVersionBody = string(b)
			w.Write([]byte(`{"data":{"id":"` + newVer + `"}}`))
		default:
			t.Errorf("unexpected %s %s", r.Method, p)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	defer srv.Close()
	base = srv.URL
	defer dmBaseURLForTest(srv.URL)()

	page, md, err := RestoreWikiPageVersion(context.Background(), "tok", "b.hub", "b.proj", itemID, oldVer, tipVer, false)
	if err != nil {
		t.Fatalf("RestoreWikiPageVersion: %v", err)
	}
	if page.TipVersion != newVer {
		t.Errorf("tipVersion = %q, want %q", page.TipVersion, newVer)
	}
	if page.ItemID != itemID {
		t.Errorf("itemId = %q, want %q", page.ItemID, itemID)
	}
	if md != oldBody {
		t.Errorf("returned markdown = %q, want the old version's body", md)
	}
	if uploaded != oldBody {
		t.Errorf("uploaded bytes = %q, want the old version's body", uploaded)
	}
	// The new version carries the item's current name, not the old version's.
	if page.Name != "renamed-page.md" || !strings.Contains(createdVersionBody, `"name":"renamed-page.md"`) {
		t.Errorf("new version name: page=%q body=%s", page.Name, createdVersionBody)
	}
	if !strings.Contains(createdVersionBody, itemID) {
		t.Errorf("new version not attached to the item: %s", createdVersionBody)
	}
}

// TestRestoreWikiPageVersionGuards covers the three refusals that cost no
// upload: a version of another item, the tip itself, and a stale base.
func TestRestoreWikiPageVersionGuards(t *testing.T) {
	const (
		itemID = "urn:adsk.wipprod:dm.lineage:abc"
		tipVer = "urn:adsk.wipprod:fs.file:vf.abc?version=3"
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "/items/") {
			w.Write([]byte(`{"data":{"type":"items","id":"` + itemID + `","attributes":{"displayName":"page.md"},"relationships":{"tip":{"data":{"id":"` + tipVer + `"}}}}}`))
			return
		}
		t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		http.Error(w, "unexpected", http.StatusNotFound)
	}))
	defer srv.Close()
	defer dmBaseURLForTest(srv.URL)()
	ctx := context.Background()

	_, _, err := RestoreWikiPageVersion(ctx, "tok", "b.hub", "b.proj", itemID, "urn:adsk.wipprod:fs.file:vf.other?version=1", "", false)
	if !errors.Is(err, ErrWikiVersionMismatch) {
		t.Errorf("foreign version: err = %v, want ErrWikiVersionMismatch", err)
	}
	_, _, err = RestoreWikiPageVersion(ctx, "tok", "b.hub", "b.proj", itemID, tipVer, tipVer, false)
	if !errors.Is(err, ErrWikiVersionIsTip) {
		t.Errorf("tip: err = %v, want ErrWikiVersionIsTip", err)
	}
	// The caller saw v2 as the tip; it is v3 now — someone published meanwhile.
	_, _, err = RestoreWikiPageVersion(ctx, "tok", "b.hub", "b.proj", itemID,
		"urn:adsk.wipprod:fs.file:vf.abc?version=1", "urn:adsk.wipprod:fs.file:vf.abc?version=2", false)
	if !errors.Is(err, ErrWikiConflict) {
		t.Errorf("stale base: err = %v, want ErrWikiConflict", err)
	}
}
