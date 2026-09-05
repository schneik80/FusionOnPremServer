package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// A wiki page's history is Data Management's version list for its item: every
// publish is a new version, so restoring an older one means uploading that
// version's bytes as the *newest* version. DM has no "move the tip back" —
// and we would not want one: a restore that erased the versions after it
// would lose the very history the user is browsing. Copy-forward keeps every
// version and makes the restore itself an auditable step in the list.

// ErrWikiVersionIsTip is returned when asked to restore the version that is
// already the tip — there is nothing to do, and a no-op version would only
// clutter the history.
var ErrWikiVersionIsTip = errors.New("wiki version is already the current version")

// ErrWikiVersionMismatch is returned when a version urn does not belong to the
// item it was addressed under. Checked before any APS call is spent on it.
var ErrWikiVersionMismatch = errors.New("wiki version does not belong to this page")

// WikiVersion is one entry in a page's history, newest first.
type WikiVersion struct {
	VersionID string // DM version urn (…?version=N)
	Number    int    // DM versionNumber
	CreatedOn string // RFC3339 as returned by DM (passed through untouched)
	CreatedBy string
}

// dmVersionEntity is the JSON:API shape of one version in an item's version
// list (only the fields the history needs).
type dmVersionEntity struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	Attributes struct {
		VersionNumber        int    `json:"versionNumber"`
		CreateTime           string `json:"createTime"`
		CreateUserName       string `json:"createUserName"`
		LastModifiedTime     string `json:"lastModifiedTime"`
		LastModifiedUserName string `json:"lastModifiedUserName"`
	} `json:"attributes"`
}

// ListWikiPageVersions returns every version of a page, newest first. It walks
// data/v1 items/{id}/versions following pagination links (100/page, like the
// folder listing) so a long-lived page is fully enumerated.
func ListWikiPageVersions(ctx context.Context, token, dmProjectID, itemID string) ([]WikiVersion, error) {
	if dmProjectID == "" || itemID == "" {
		return nil, fmt.Errorf("item versions: empty project or item")
	}
	next := fmt.Sprintf("%s/data/v1/projects/%s/items/%s/versions?page%%5Blimit%%5D=100",
		dmBaseURL, dmEscape(dmProjectID), dmEscape(itemID))
	out := []WikiVersion{}
	for next != "" {
		body, err := dmGet(ctx, token, next)
		if err != nil {
			return nil, fmt.Errorf("item versions: %w", err)
		}
		var doc struct {
			Data  []dmVersionEntity `json:"data"`
			Links struct {
				Next struct {
					Href string `json:"href"`
				} `json:"next"`
			} `json:"links"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return nil, fmt.Errorf("item versions decode: %w", err)
		}
		for _, v := range doc.Data {
			if v.Type != "versions" || v.ID == "" {
				continue
			}
			created, by := v.Attributes.CreateTime, v.Attributes.CreateUserName
			if created == "" {
				created = v.Attributes.LastModifiedTime
			}
			if by == "" {
				by = v.Attributes.LastModifiedUserName
			}
			out = append(out, WikiVersion{
				VersionID: v.ID,
				Number:    v.Attributes.VersionNumber,
				CreatedOn: created,
				CreatedBy: by,
			})
		}
		next = doc.Links.Next.Href
	}
	// DM answers oldest-first; the history reads newest-first. Sort rather than
	// reverse so a page whose order the API changes still lands right.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Number > out[j].Number })
	return out, nil
}

// DownloadWikiPageVersion fetches the markdown of one specific version of a
// page. The caller must already have proved versionURN belongs to the page
// (VersionBelongsToItem); this only resolves storage and downloads.
func DownloadWikiPageVersion(ctx context.Context, token, dmProjectID, versionURN string) (string, error) {
	storageURN, err := versionStorageURN(ctx, token, dmProjectID, versionURN)
	if err != nil {
		return "", err
	}
	return downloadOSSObject(ctx, token, storageURN)
}

// dmItem is the subset of GET data/v1 items/{id} the restore needs: the item's
// current display name (a renamed page's versions must keep the new name) and
// its tip version — both in one request.
type dmItem struct {
	Name string
	Tip  string
}

func getDMItem(ctx context.Context, token, dmProjectID, itemID string) (dmItem, error) {
	u := fmt.Sprintf("%s/data/v1/projects/%s/items/%s", dmBaseURL, dmEscape(dmProjectID), dmEscape(itemID))
	body, err := dmGet(ctx, token, u)
	if err != nil {
		return dmItem{}, fmt.Errorf("item: %w", err)
	}
	var doc struct {
		Data dmEntity `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return dmItem{}, fmt.Errorf("item decode: %w", err)
	}
	it := dmItem{Name: doc.Data.name(), Tip: doc.Data.Relationships.Tip.Data.ID}
	if it.Tip == "" {
		return dmItem{}, fmt.Errorf("item %s has no tip version", trimURL(itemID))
	}
	return it, nil
}

// RestoreWikiPageVersion makes an older version of a page its newest one by
// re-publishing that version's bytes as a new version (copy-forward — see the
// file comment). baseVersion is the tip the caller saw when it chose to
// restore; if the live tip has moved past it another user published in the
// meantime, and the restore returns ErrWikiConflict unless force is set, so the
// UI can ask before overwriting their work. Restoring the tip itself is
// ErrWikiVersionIsTip. Returns the page with its new tip version and the
// restored markdown, so the caller can update a linked draft without a second
// download.
func RestoreWikiPageVersion(ctx context.Context, token, dmHubID, dmProjectID, itemID, versionID, baseVersion string, force bool) (WikiPage, string, error) {
	if !VersionBelongsToItem(versionID, itemID) {
		return WikiPage{}, "", ErrWikiVersionMismatch
	}
	item, err := getDMItem(ctx, token, dmProjectID, itemID)
	if err != nil {
		return WikiPage{}, "", err
	}
	if item.Tip == versionID {
		return WikiPage{}, "", ErrWikiVersionIsTip
	}
	if !force && baseVersion != "" && item.Tip != baseVersion {
		return WikiPage{}, "", ErrWikiConflict
	}
	markdown, err := DownloadWikiPageVersion(ctx, token, dmProjectID, versionID)
	if err != nil {
		return WikiPage{}, "", err
	}
	wikiFolderID, err := ensureWikiFolder(ctx, token, dmHubID, dmProjectID)
	if err != nil {
		return WikiPage{}, "", err
	}
	filename := item.Name
	if filename == "" {
		filename = "page" + wikiExt
	}
	_, newTip, err := uploadFile(ctx, token, dmProjectID, wikiFolderID, filename, []byte(markdown), itemID)
	if err != nil {
		return WikiPage{}, "", err
	}
	return WikiPage{ItemID: itemID, Name: filename, TipVersion: newTip}, markdown, nil
}
