package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Thumbnail status values returned by the Manufacturing Data Model API.
// Generation is asynchronous, mirroring STEP derivatives: the first call on a
// freshly-saved version may report PENDING with an empty URL.
const (
	ThumbnailStatusPending = "PENDING"
	ThumbnailStatusSuccess = "SUCCESS"
	ThumbnailStatusFailed  = "FAILED"
)

// GetThumbnail asks the Manufacturing Data Model API for the thumbnail of a
// component version. Returns the generation status and, once status is
// SUCCESS, a signed download URL (empty otherwise). Callers should poll until
// status is SUCCESS or FAILED.
//
// The signed URL is self-authenticated (the signature is embedded in the URL),
// so it can be loaded directly as an <img> src without the bearer token — the
// same reasoning that keeps DownloadFile from attaching the token.
func GetThumbnail(ctx context.Context, token, componentVersionID string) (status, signedURL string, err error) {
	if componentVersionID == "" {
		return "", "", fmt.Errorf("thumbnail: empty componentVersionID")
	}

	const q = `
		query GetThumbnail($componentVersionId: ID!) {
			componentVersion(componentVersionId: $componentVersionId) {
				thumbnail {
					status
					signedUrl
				}
			}
		}`

	data, err := gqlQuery(ctx, token, q, map[string]any{"componentVersionId": componentVersionID})
	if err != nil {
		return "", "", fmt.Errorf("thumbnail: %w", err)
	}

	var raw struct {
		ComponentVersion struct {
			Thumbnail struct {
				Status    string `json:"status"`
				SignedURL string `json:"signedUrl"`
			} `json:"thumbnail"`
		} `json:"componentVersion"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return "", "", fmt.Errorf("thumbnail decode: %w", err)
	}
	t := raw.ComponentVersion.Thumbnail
	return normalizeThumbStatus(t.Status), t.SignedURL, nil
}

// normalizeThumbStatus upper-cases the status and maps the empty/absent case
// (a nullable componentVersion/thumbnail with no GraphQL error) to FAILED, so
// a poller never waits forever for a SUCCESS that will never arrive.
func normalizeThumbStatus(s string) string {
	s = strings.ToUpper(s)
	if s == "" {
		return ThumbnailStatusFailed
	}
	return s
}

// ClassifyAndThumbnail issues a single componentVersion query that both
// classifies the design (assembly vs part, via a one-result occurrences probe)
// and fetches its thumbnail status/URL. The server fires one of these per
// design row as a folder loads, so combining them halves the per-row APS round
// trips and lets the thumbnail cache warm in the background off the same call.
//
// Shares classifySem with ClassifyAssembly so the combined fan-out stays within
// the same concurrency budget.
func ClassifyAndThumbnail(ctx context.Context, token, componentVersionID string) (isAssembly bool, status, signedURL string, err error) {
	if componentVersionID == "" {
		return false, "", "", fmt.Errorf("classify+thumbnail: empty componentVersionID")
	}
	const q = `
		query ClassifyAndThumbnail($cv: ID!) {
			componentVersion(componentVersionId: $cv) {
				occurrences(pagination: { limit: 1 }) {
					results { id }
				}
				thumbnail {
					status
					signedUrl
				}
			}
		}`
	data, err := gqlQuery(ctx, token, q, map[string]any{"cv": componentVersionID})
	if err != nil {
		return false, "", "", err
	}
	var raw struct {
		ComponentVersion struct {
			Occurrences struct {
				Results []struct {
					ID string `json:"id"`
				} `json:"results"`
			} `json:"occurrences"`
			Thumbnail struct {
				Status    string `json:"status"`
				SignedURL string `json:"signedUrl"`
			} `json:"thumbnail"`
		} `json:"componentVersion"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false, "", "", fmt.Errorf("classify+thumbnail decode: %w", err)
	}
	cv := raw.ComponentVersion
	return len(cv.Occurrences.Results) > 0,
		normalizeThumbStatus(cv.Thumbnail.Status),
		cv.Thumbnail.SignedURL,
		nil
}

// maxThumbnailBytes caps a thumbnail download so a hostile/oversized URL can't
// exhaust memory. APS thumbnails are small PNGs (tens of KB).
const maxThumbnailBytes = 8 << 20 // 8 MiB

// FetchThumbnailImage downloads the bytes at a thumbnail's signed URL. Like
// DownloadFile, it sends no bearer token — APS signed URLs are
// self-authenticated, so attaching the token would leak it to the URL's host.
func FetchThumbnailImage(ctx context.Context, url string) (data []byte, contentType string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, "", fmt.Errorf("thumbnail image HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, maxThumbnailBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxThumbnailBytes {
		return nil, "", fmt.Errorf("thumbnail image exceeds %d bytes", maxThumbnailBytes)
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/png"
	}
	return data, ct, nil
}
