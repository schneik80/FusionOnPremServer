package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
)

// Publishing a wiki page (and uploading its images) writes to Fusion Team via the
// Data Management API. The upload sequence for any file is: create a storage
// object → push the bytes to OSS through a signed S3 URL → create the item (first
// version) or a new version pointing at that storage. Folders (the project-root
// "Wiki", a per-page "<slug>", and its "images") are created on demand. These
// calls need the data:write + data:create scopes (see auth.authScope).

// ErrWikiConflict signals that a page changed upstream since the draft was opened
// (or a same-named page already exists). Handlers map it to HTTP 409 so the UI
// can offer to overwrite.
var ErrWikiConflict = errors.New("wiki page changed since it was opened")

// Data Management extension types for generic uploaded (non-native) files and
// plain folders, as used by Fusion Team.
const (
	fileItemExtType    = "items:autodesk.core:File"
	fileVersionExtType = "versions:autodesk.core:File"
	folderExtType      = "folders:autodesk.core:Folder"
)

// PublishWikiPage uploads a page's markdown to the project's Wiki folder as
// "<slug>.md", creating the item (first publish) or a new version. itemID is the
// page's known lineage urn (from a linked draft) or empty. baseVersion is the tip
// the draft was based on; if the live tip has moved past it — or, for a new page,
// a same-named page already exists — it returns ErrWikiConflict unless force is
// set. Returns the resulting page (with its new tip version).
func PublishWikiPage(ctx context.Context, token, dmHubID, dmProjectID, itemID, slug, markdown, baseVersion string, force bool) (WikiPage, error) {
	wikiFolderID, err := ensureWikiFolder(ctx, token, dmHubID, dmProjectID)
	if err != nil {
		return WikiPage{}, err
	}
	filename := slug + wikiExt

	if itemID == "" {
		existingID, err := findItemByName(ctx, token, dmProjectID, wikiFolderID, filename)
		if err != nil {
			return WikiPage{}, err
		}
		if existingID != "" {
			// A page with this name already exists (e.g. published from another
			// device). Don't silently fork it — surface a conflict unless forced.
			if !force {
				return WikiPage{}, ErrWikiConflict
			}
			itemID = existingID
		}
	} else if !force && baseVersion != "" {
		tip, err := GetItemTipVersion(ctx, token, dmProjectID, itemID)
		if err != nil {
			return WikiPage{}, err
		}
		if tip != baseVersion {
			return WikiPage{}, ErrWikiConflict
		}
	}

	newItemID, versionID, err := uploadFile(ctx, token, dmProjectID, wikiFolderID, filename, []byte(markdown), itemID)
	if err != nil {
		return WikiPage{}, err
	}
	return WikiPage{ItemID: newItemID, Name: filename, TipVersion: versionID}, nil
}

// UploadWikiImage stores an image alongside a page under Wiki/<slug>/images/,
// creating those folders on demand, and returns the image item's lineage urn (so
// the caller can reference it via the image endpoint). Re-uploading a same-named
// image adds a new version rather than a duplicate.
func UploadWikiImage(ctx context.Context, token, dmHubID, dmProjectID, pageSlug, filename string, data []byte) (string, error) {
	wikiFolderID, err := ensureWikiFolder(ctx, token, dmHubID, dmProjectID)
	if err != nil {
		return "", err
	}
	pageFolderID, err := ensureSubfolder(ctx, token, dmProjectID, wikiFolderID, pageSlug)
	if err != nil {
		return "", err
	}
	imagesFolderID, err := ensureSubfolder(ctx, token, dmProjectID, pageFolderID, "images")
	if err != nil {
		return "", err
	}
	existingID, err := findItemByName(ctx, token, dmProjectID, imagesFolderID, filename)
	if err != nil {
		return "", err
	}
	itemID, _, err := uploadFile(ctx, token, dmProjectID, imagesFolderID, filename, data, existingID)
	return itemID, err
}

// RenameWikiPage renames a published page's file to "<newSlug>.md" (the item
// lineage id is unchanged, so links/versions survive) and, best effort, renames
// its images subfolder to match. Image references are by item id, so a stale
// folder name is only cosmetic and must not fail the rename.
func RenameWikiPage(ctx context.Context, token, dmHubID, dmProjectID, itemID, oldSlug, newSlug string) error {
	if err := patchItemName(ctx, token, dmProjectID, itemID, newSlug+wikiExt); err != nil {
		return err
	}
	if oldSlug != "" && !strings.EqualFold(oldSlug, newSlug) {
		if _, wikiID, err := resolveWikiFolder(ctx, token, dmHubID, dmProjectID); err == nil && wikiID != "" {
			if fid, _ := findSubfolderID(ctx, token, dmProjectID, wikiID, oldSlug); fid != "" {
				_ = patchFolderName(ctx, token, dmProjectID, fid, newSlug)
			}
		}
	}
	return nil
}

func patchItemName(ctx context.Context, token, dmProjectID, itemID, newName string) error {
	payload := map[string]any{
		"jsonapi": map[string]any{"version": "1.0"},
		"data": map[string]any{
			"type":       "items",
			"id":         itemID,
			"attributes": map[string]any{"displayName": newName},
		},
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/items/%s", dmBaseURL, dmEscape(dmProjectID), dmEscape(itemID))
	if _, err := dmPatch(ctx, token, u, mustJSON(payload)); err != nil {
		return fmt.Errorf("rename item: %w", err)
	}
	return nil
}

func patchFolderName(ctx context.Context, token, dmProjectID, folderID, newName string) error {
	payload := map[string]any{
		"jsonapi": map[string]any{"version": "1.0"},
		"data": map[string]any{
			"type":       "folders",
			"id":         folderID,
			"attributes": map[string]any{"name": newName},
		},
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/folders/%s", dmBaseURL, dmEscape(dmProjectID), dmEscape(folderID))
	_, err := dmPatch(ctx, token, u, mustJSON(payload))
	return err
}

func findSubfolderID(ctx context.Context, token, dmProjectID, parentID, name string) (string, error) {
	children, err := dmFolderContents(ctx, token, dmProjectID, parentID)
	if err != nil {
		return "", err
	}
	for _, c := range children {
		if c.Type == "folders" && strings.EqualFold(c.name(), name) {
			return c.ID, nil
		}
	}
	return "", nil
}

// DownloadWikiAsset fetches an image (or any wiki file) item's tip bytes plus the
// content type S3 reports, for streaming back to the browser.
func DownloadWikiAsset(ctx context.Context, token, dmProjectID, itemID string) ([]byte, string, error) {
	versionURN, err := GetItemTipVersion(ctx, token, dmProjectID, itemID)
	if err != nil {
		return nil, "", err
	}
	storageURN, err := versionStorageURN(ctx, token, dmProjectID, versionURN)
	if err != nil {
		return nil, "", err
	}
	return downloadOSSObjectBytes(ctx, token, storageURN)
}

// ── folder helpers ─────────────────────────────────────────────────

// ensureWikiFolder returns the project-root Wiki folder id, creating it if this
// is the first publish for the project.
func ensureWikiFolder(ctx context.Context, token, dmHubID, dmProjectID string) (string, error) {
	rootID, wikiID, err := resolveWikiFolder(ctx, token, dmHubID, dmProjectID)
	if err != nil {
		return "", err
	}
	if wikiID != "" {
		return wikiID, nil
	}
	return createFolder(ctx, token, dmProjectID, rootID, WikiFolderName)
}

// ensureSubfolder finds a child folder by name under parentID, creating it if
// absent.
func ensureSubfolder(ctx context.Context, token, dmProjectID, parentID, name string) (string, error) {
	children, err := dmFolderContents(ctx, token, dmProjectID, parentID)
	if err != nil {
		return "", err
	}
	for _, c := range children {
		if c.Type == "folders" && strings.EqualFold(c.name(), name) {
			return c.ID, nil
		}
	}
	return createFolder(ctx, token, dmProjectID, parentID, name)
}

// findItemByName returns the lineage urn of an item named name in folderID, or ""
// if none exists.
func findItemByName(ctx context.Context, token, dmProjectID, folderID, name string) (string, error) {
	entries, err := dmFolderContents(ctx, token, dmProjectID, folderID)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.Type == "items" && strings.EqualFold(e.name(), name) {
			return e.ID, nil
		}
	}
	return "", nil
}

func createFolder(ctx context.Context, token, dmProjectID, parentID, name string) (string, error) {
	payload := map[string]any{
		"jsonapi": map[string]any{"version": "1.0"},
		"data": map[string]any{
			"type": "folders",
			"attributes": map[string]any{
				"name":      name,
				"extension": map[string]any{"type": folderExtType, "version": "1.0"},
			},
			"relationships": map[string]any{
				"parent": map[string]any{"data": map[string]any{"type": "folders", "id": parentID}},
			},
		},
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/folders", dmBaseURL, dmEscape(dmProjectID))
	body, err := dmPost(ctx, token, u, mustJSON(payload))
	if err != nil {
		return "", fmt.Errorf("create folder %q: %w", name, err)
	}
	return dataID(body)
}

// ── file upload ────────────────────────────────────────────────────

// uploadFile runs the storage → OSS → item|version sequence and returns the
// item lineage urn and the new version urn. Pass itemID="" to create a new item.
func uploadFile(ctx context.Context, token, dmProjectID, folderID, filename string, data []byte, itemID string) (string, string, error) {
	storageURN, err := createStorage(ctx, token, dmProjectID, folderID, filename)
	if err != nil {
		return "", "", err
	}
	if err := uploadToOSS(ctx, token, storageURN, filename, data); err != nil {
		return "", "", err
	}
	if itemID == "" {
		return createItem(ctx, token, dmProjectID, folderID, filename, storageURN)
	}
	versionID, err := createVersion(ctx, token, dmProjectID, itemID, filename, storageURN)
	return itemID, versionID, err
}

func createStorage(ctx context.Context, token, dmProjectID, folderID, filename string) (string, error) {
	payload := map[string]any{
		"jsonapi": map[string]any{"version": "1.0"},
		"data": map[string]any{
			"type":       "objects",
			"attributes": map[string]any{"name": filename},
			"relationships": map[string]any{
				"target": map[string]any{"data": map[string]any{"type": "folders", "id": folderID}},
			},
		},
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/storage", dmBaseURL, dmEscape(dmProjectID))
	body, err := dmPost(ctx, token, u, mustJSON(payload))
	if err != nil {
		return "", fmt.Errorf("create storage: %w", err)
	}
	return dataID(body)
}

// uploadToOSS pushes bytes to the object's OSS storage via a signed S3 upload
// (GET signed url → PUT bytes → POST finalize).
func uploadToOSS(ctx context.Context, token, storageURN, filename string, data []byte) error {
	bucket, object, ok := parseOSSObjectURN(storageURN)
	if !ok {
		return fmt.Errorf("unrecognised storage urn")
	}
	signURL := fmt.Sprintf("%s/oss/v2/buckets/%s/objects/%s/signeds3upload",
		dmBaseURL, dmEscape(bucket), dmEscape(object))
	body, err := dmGet(ctx, token, signURL)
	if err != nil {
		return fmt.Errorf("sign upload: %w", err)
	}
	var s struct {
		UploadKey string   `json:"uploadKey"`
		URLs      []string `json:"urls"`
	}
	if err := json.Unmarshal(body, &s); err != nil {
		return fmt.Errorf("sign upload decode: %w", err)
	}
	if len(s.URLs) == 0 || s.UploadKey == "" {
		return fmt.Errorf("sign upload: empty response")
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, s.URLs[0], bytes.NewReader(data))
	if err != nil {
		return err
	}
	put.Header.Set("Content-Type", contentTypeForName(filename))
	presp, err := httpClient.Do(put) // signed URL carries its own auth; no Bearer header
	if err != nil {
		return fmt.Errorf("upload bytes: %w", err)
	}
	io.Copy(io.Discard, io.LimitReader(presp.Body, 1<<16))
	presp.Body.Close()
	if presp.StatusCode < 200 || presp.StatusCode >= 300 {
		return fmt.Errorf("upload bytes -> HTTP %d", presp.StatusCode)
	}
	if _, err := dmPost(ctx, token, signURL, mustJSON(map[string]any{"uploadKey": s.UploadKey})); err != nil {
		return fmt.Errorf("finalize upload: %w", err)
	}
	return nil
}

func createItem(ctx context.Context, token, dmProjectID, folderID, filename, storageURN string) (string, string, error) {
	payload := map[string]any{
		"jsonapi": map[string]any{"version": "1.0"},
		"data": map[string]any{
			"type": "items",
			"attributes": map[string]any{
				"displayName": filename,
				"extension":   map[string]any{"type": fileItemExtType, "version": "1.0"},
			},
			"relationships": map[string]any{
				"tip":    map[string]any{"data": map[string]any{"type": "versions", "id": "1"}},
				"parent": map[string]any{"data": map[string]any{"type": "folders", "id": folderID}},
			},
		},
		"included": []any{
			map[string]any{
				"type": "versions",
				"id":   "1",
				"attributes": map[string]any{
					"name":      filename,
					"extension": map[string]any{"type": fileVersionExtType, "version": "1.0"},
				},
				"relationships": map[string]any{
					"storage": map[string]any{"data": map[string]any{"type": "objects", "id": storageURN}},
				},
			},
		},
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/items", dmBaseURL, dmEscape(dmProjectID))
	body, err := dmPost(ctx, token, u, mustJSON(payload))
	if err != nil {
		return "", "", fmt.Errorf("create item: %w", err)
	}
	var doc struct {
		Data struct {
			ID            string `json:"id"`
			Relationships struct {
				Tip struct {
					Data struct {
						ID string `json:"id"`
					} `json:"data"`
				} `json:"tip"`
			} `json:"relationships"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", "", fmt.Errorf("create item decode: %w", err)
	}
	if doc.Data.ID == "" {
		return "", "", fmt.Errorf("create item: no item id in response")
	}
	return doc.Data.ID, doc.Data.Relationships.Tip.Data.ID, nil
}

// createVersion adds a new version to an existing item. The Data Management API
// exposes this as POST projects/{pid}/versions (there is no items/{id}/versions
// create endpoint — posting there 404s); the item is named via relationships.item.
func createVersion(ctx context.Context, token, dmProjectID, itemID, filename, storageURN string) (string, error) {
	payload := map[string]any{
		"jsonapi": map[string]any{"version": "1.0"},
		"data": map[string]any{
			"type": "versions",
			"attributes": map[string]any{
				"name":      filename,
				"extension": map[string]any{"type": fileVersionExtType, "version": "1.0"},
			},
			"relationships": map[string]any{
				"item":    map[string]any{"data": map[string]any{"type": "items", "id": itemID}},
				"storage": map[string]any{"data": map[string]any{"type": "objects", "id": storageURN}},
			},
		},
	}
	u := fmt.Sprintf("%s/data/v1/projects/%s/versions", dmBaseURL, dmEscape(dmProjectID))
	body, err := dmPost(ctx, token, u, mustJSON(payload))
	if err != nil {
		return "", fmt.Errorf("create version: %w", err)
	}
	return dataID(body)
}

// ── low-level helpers ──────────────────────────────────────────────

func dmPost(ctx context.Context, token, url string, body []byte) ([]byte, error) {
	return dmDo(ctx, token, http.MethodPost, url, "application/json", body, 1<<20)
}

// dmPatch performs an authenticated JSON:API PATCH (rename item/folder).
func dmPatch(ctx context.Context, token, url string, body []byte) ([]byte, error) {
	return dmDo(ctx, token, http.MethodPatch, url, "application/vnd.api+json", body, 1<<20)
}

// dataID pulls data.id out of a JSON:API response body.
func dataID(body []byte) (string, error) {
	var doc struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return "", fmt.Errorf("decode id: %w", err)
	}
	if doc.Data.ID == "" {
		return "", fmt.Errorf("no id in response")
	}
	return doc.Data.ID, nil
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// contentTypeForName maps a filename to a content type for upload/serving. It
// covers the extensions the wiki uploads (markdown, images) plus the file types
// the preview viewers serve (pdf, video, text, NC g-code), so the file handler
// can label streamed bytes correctly regardless of what OSS stored — the
// mime.TypeByExtension fallback is unreliable across platforms for these.
func contentTypeForName(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".md":
		return "text/markdown; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".ico":
		return "image/x-icon"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".heic", ".heif":
		return "image/heic"
	case ".pdf":
		return "application/pdf"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mov":
		return "video/quicktime"
	case ".ogv", ".ogg":
		return "video/ogg"
	case ".avi":
		return "video/x-msvideo"
	case ".mkv":
		return "video/x-matroska"
	case ".txt", ".log", ".ini", ".cfg":
		return "text/plain; charset=utf-8"
	case ".csv":
		return "text/csv; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".xml":
		return "application/xml; charset=utf-8"
	// NC g-code and CNC program dialects — serve as plain text so the browser
	// (and our text viewer) treats them as readable source, not a download.
	case ".nc", ".cnc", ".ngc", ".gc", ".gcode", ".tap", ".ncp", ".mpf", ".spf", ".eia", ".min", ".h":
		return "text/plain; charset=utf-8"
	default:
		if ct := mime.TypeByExtension(path.Ext(name)); ct != "" {
			return ct
		}
		return "application/octet-stream"
	}
}
