package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/schneik80/fusionlocalserver/chat"
)

// stubChatImageUpload replaces the APS seams and records the upload args.
type chatImageUploadCall struct {
	dmHubID, dmProjectID, name string
	size                       int
}

func stubChatImageUpload(t *testing.T) *chatImageUploadCall {
	t.Helper()
	call := &chatImageUploadCall{}
	prevDM, prevUp := chatImageHubDMID, chatImageUpload
	chatImageHubDMID = func(ctx context.Context, token, hubID string) (string, error) {
		return "b.dm-" + hubID, nil
	}
	chatImageUpload = func(ctx context.Context, token, dmHubID, dmProjectID, name string, data []byte) (string, error) {
		*call = chatImageUploadCall{dmHubID: dmHubID, dmProjectID: dmProjectID, name: name, size: len(data)}
		return "urn:adsk.wipprod:dm.lineage:img1", nil
	}
	t.Cleanup(func() { chatImageHubDMID, chatImageUpload = prevDM, prevUp })
	return call
}

// postChatImage sends a multipart image upload and decodes the reply.
func postChatImage(t *testing.T, base string, cookie *http.Cookie, filename string, payload []byte, out any) int {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if filename != "" {
		fw, err := mw.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	mw.Close()
	url := base + "/api/chat/image?projectId=" + chatTestProject + "&hubId=" + testHubID + "&dmProjectId=b.p1"
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && len(data) > 0 && resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("decoding %q: %v", data, err)
		}
	}
	return resp.StatusCode
}

func TestChatImageUpload(t *testing.T) {
	call := stubChatImageUpload(t)
	s, _ := newChatTestServer(t)
	s.chatImgLim = chat.NewLimiter(50, 100) // roomy: this test uploads rapidly
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)

	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")
	viewer := login(t, s, "u-viewer", "Vi Viewer", "viewer@x.io")

	// Editor uploads → 200 with the lineage urn; APS gets the DM-space ids.
	var out ChatImageDTO
	if code := postChatImage(t, ts.URL, editor, "shot.png", []byte("png-bytes"), &out); code != http.StatusOK {
		t.Fatalf("editor upload = %d, want 200", code)
	}
	if out.ItemID != "urn:adsk.wipprod:dm.lineage:img1" || out.Name != "shot.png" {
		t.Errorf("upload result = %+v", out)
	}
	if call.dmHubID != "b.dm-"+testHubID || call.dmProjectID != "b.p1" || call.name != "shot.png" || call.size != 9 {
		t.Errorf("upload call = %+v", call)
	}

	// A read-only member cannot attach (attaching is posting).
	if code := postChatImage(t, ts.URL, viewer, "shot.png", []byte("x"), nil); code != http.StatusForbidden {
		t.Errorf("viewer upload = %d, want 403", code)
	}

	// No file part → 400.
	if code := postChatImage(t, ts.URL, editor, "", nil, nil); code != http.StatusBadRequest {
		t.Errorf("missing file = %d, want 400", code)
	}

	// Unauthenticated → 401.
	if code := postChatImage(t, ts.URL, nil, "shot.png", []byte("x"), nil); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated = %d, want 401", code)
	}
}

func TestChatImageUpload_OversizeRefused(t *testing.T) {
	call := stubChatImageUpload(t)
	s, _ := newChatTestServer(t)
	s.chatImgLim = chat.NewLimiter(50, 100)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	big := make([]byte, chatImageMaxBytes+1)
	code := postChatImage(t, ts.URL, editor, "huge.png", big, nil)
	// MaxBytesReader may abort the multipart parse (400) before the explicit
	// length check (413) depending on where the boundary lands; both refuse.
	if code != http.StatusRequestEntityTooLarge && code != http.StatusBadRequest {
		t.Fatalf("oversize upload = %d, want 413 or 400", code)
	}
	if call.size != 0 {
		t.Error("oversize upload still reached APS")
	}
}

func TestChatImageUpload_RateLimited(t *testing.T) {
	stubChatImageUpload(t)
	s, _ := newChatTestServer(t)
	s.chatImgLim = chat.NewLimiter(0.5, 3)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed Editor", "editor@x.io")

	for i := 0; i < 3; i++ {
		if code := postChatImage(t, ts.URL, editor, "s.png", []byte("x"), nil); code != http.StatusOK {
			t.Fatalf("upload %d = %d, want 200", i, code)
		}
	}
	if code := postChatImage(t, ts.URL, editor, "s.png", []byte("x"), nil); code != http.StatusTooManyRequests {
		t.Errorf("burst-exhausted upload = %d, want 429", code)
	}
}
