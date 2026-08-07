package api

import (
	"context"
	"fmt"
)

// chatFolderName is the project-root folder that holds chat attachments
// (Chat/images/). A sibling of the wiki's "Wiki" folder, same posture: the
// images are ordinary Fusion Team items, inheriting the project's access
// control, storage and retention.
const chatFolderName = "Chat"

// UploadChatImage stores a chat attachment image under Chat/images/ at the
// project root, creating those folders on demand, and returns the image
// item's lineage urn (referenced via the same tip-download endpoint wiki
// images use). Re-uploading a same-named image adds a new version rather
// than a duplicate — callers that want distinct items use distinct names.
func UploadChatImage(ctx context.Context, token, dmHubID, dmProjectID, filename string, data []byte) (string, error) {
	tops, err := dmTopFolders(ctx, token, dmHubID, dmProjectID)
	if err != nil {
		return "", err
	}
	if len(tops) == 0 {
		return "", fmt.Errorf("project has no root folder")
	}
	chatID, err := ensureSubfolder(ctx, token, dmProjectID, tops[0].ID, chatFolderName)
	if err != nil {
		return "", err
	}
	imagesID, err := ensureSubfolder(ctx, token, dmProjectID, chatID, "images")
	if err != nil {
		return "", err
	}
	existingID, err := findItemByName(ctx, token, dmProjectID, imagesID, filename)
	if err != nil {
		return "", err
	}
	itemID, _, err := uploadFile(ctx, token, dmProjectID, imagesID, filename, data, existingID)
	return itemID, err
}
