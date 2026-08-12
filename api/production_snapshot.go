package api

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// DocVersionSnapshot is the version pin for a Fusion Team document: the exact
// version urn plus the numbers the UI needs to render that specific version
// (its badge and its thumbnail). It is resolved server-side so the client never
// invents version urns.
type DocVersionSnapshot struct {
	VersionID              string // DM version urn (urn:…:fs.file:vf.…?version=N)
	VersionNumber          int    // human version number
	RootComponentVersionID string // this version's cvId, for the thumbnail
}

// SnapshotDocVersion resolves a document into a version pin.
//
// preferredVersionID, when non-empty, is a DM version urn the caller asserts
// was just created (the upload path: UploadFileToFolder returns it) — it is
// accepted only if it belongs to itemID's lineage, so a client can pin a
// *version of the right document* but never a foreign one. Empty → the current
// Data Management tip is pinned (the hub-browse path).
//
// The human version number comes from the version urn itself (?version=N) —
// the DM API is the source of truth for every item kind. The MDM GraphQL
// details call is best-effort decoration: it supplies the per-version
// thumbnail cvId for designs, but plain files (BasicItem) carry no tipVersion
// there, and DM-created items may not have propagated to the MDM graph at all
// (a known gap) — neither may fail the pin. The cvId is used only when the
// details tip matches the pinned version, so a pin never borrows another
// version's thumbnail.
func SnapshotDocVersion(ctx context.Context, token, hubID, dmProjectID, itemID, preferredVersionID string) (DocVersionSnapshot, error) {
	versionID := preferredVersionID
	if versionID != "" {
		if !VersionBelongsToItem(versionID, itemID) {
			return DocVersionSnapshot{}, fmt.Errorf("version pin: versionId does not belong to the document")
		}
	} else {
		tip, err := GetItemTipVersion(ctx, token, dmProjectID, itemID)
		if err != nil {
			return DocVersionSnapshot{}, err
		}
		versionID = tip
	}

	snap := DocVersionSnapshot{
		VersionID:     versionID,
		VersionNumber: VersionNumberFromURN(versionID),
	}

	// Best-effort thumbnail decoration; see the function comment.
	if det, err := GetItemDetails(ctx, token, hubID, itemID); err == nil {
		if det.VersionNumber == snap.VersionNumber {
			snap.RootComponentVersionID = det.RootComponentVersionID
		}
		if snap.VersionNumber == 0 {
			// Version urn without a ?version query (shouldn't happen for DM
			// file versions, but don't pin "v0" if details knows better).
			snap.VersionNumber = det.VersionNumber
		}
	}
	return snap, nil
}

// VersionNumberFromURN extracts N from a DM version urn's "?version=N" query.
// Returns 0 when absent or malformed. Exported for the same reason as
// VersionBelongsToItem: anything handed a pinned version urn — the archive job
// naming its file — needs the human number without a second APS call.
func VersionNumberFromURN(urn string) int {
	_, query, ok := strings.Cut(urn, "?")
	if !ok {
		return 0
	}
	for _, kv := range strings.Split(query, "&") {
		if v, found := strings.CutPrefix(kv, "version="); found {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				return n
			}
		}
	}
	return 0
}

// VersionBelongsToItem reports whether a DM version urn is a version of the
// given item lineage. The two id forms share a core id:
//
//	lineage: urn:adsk.wipprod:dm.lineage:<core>
//	version: urn:adsk.wipprod:fs.file:vf.<core>?version=N
//
// Exported because every path that lets a client name a version — pinning one
// in production, archiving a pinned one — has to prove it belongs to the
// document being addressed before spending an APS call on it.
func VersionBelongsToItem(versionURN, lineageURN string) bool {
	vCore := versionURN
	if i := strings.IndexByte(vCore, '?'); i >= 0 {
		vCore = vCore[:i]
	}
	if i := strings.LastIndexByte(vCore, ':'); i >= 0 {
		vCore = vCore[i+1:]
	}
	vCore = strings.TrimPrefix(vCore, "vf.")

	lCore := lineageURN
	if i := strings.LastIndexByte(lCore, ':'); i >= 0 {
		lCore = lCore[i+1:]
	}
	return vCore != "" && vCore == lCore
}
