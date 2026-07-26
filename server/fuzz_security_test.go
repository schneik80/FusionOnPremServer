package server

// QA security: fuzzing + boundary suites for the JSON-body endpoints.
//
// These call handlers directly (bypassing requireAuth) so they exercise the
// parse/validate path the way an authenticated attacker would reach it. The
// invariant under fuzzing is resilience: the handler must never panic and must
// never emit a 5xx for client-supplied garbage — only the defined 4xx (or, for
// a well-formed-but-unauthenticated request, 401). Run the fuzzers with:
//
//	go test ./server/ -run xxx -fuzz FuzzPinsAddBody   -fuzztime 30s
//	go test ./server/ -run xxx -fuzz FuzzRollupBody     -fuzztime 30s
//
// Without -fuzz they execute as ordinary seed-corpus tests.

import (
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/api"
	"github.com/schneik80/fusionlocalserver/auth"
	"github.com/schneik80/fusionlocalserver/chat"
)

// setTempHomeFuzz redirects the pins on-disk store at a throwaway dir for the
// lifetime of a fuzz run. Uses os.Setenv (not t.Setenv) because fuzz targets
// run with parallelism, where t.Setenv panics.
func setTempHomeFuzz(f *testing.F) {
	f.Helper()
	home := f.TempDir()
	prev, had := os.LookupEnv("HOME")
	os.Setenv("HOME", home)
	os.Setenv("USERPROFILE", home)
	f.Cleanup(func() {
		if had {
			os.Setenv("HOME", prev)
		} else {
			os.Unsetenv("HOME")
		}
	})
	if err := os.MkdirAll(filepath.Join(home, ".config", "fusionlocalserver"), 0700); err != nil {
		f.Fatalf("setup config dir: %v", err)
	}
}

// assertResilient is the shared fuzz invariant: a real HTTP status, never 5xx.
func assertResilient(t *testing.T, code int, body, input string) {
	t.Helper()
	if code >= 500 {
		t.Fatalf("5xx (%d) on client input %q -> %q", code, input, body)
	}
	if code < 100 || code > 599 {
		t.Fatalf("nonsense status %d on input %q", code, input)
	}
}

// FuzzPinsAddBody throws arbitrary bytes at the pin-create body. Malformed JSON
// must 400; a valid pin must 200; nothing may panic or 5xx. The 64 KiB
// MaxBytesReader cap means even a multi-MB blob is bounded, not buffered whole.
func FuzzPinsAddBody(f *testing.F) {
	setTempHomeFuzz(f)
	seeds := []string{
		`{"id":"x","kind":"design"}`,              // valid
		`{"id":"x","kind":`,                       // missing bracket / truncated
		`{"id":123,"kind":true}`,                  // type mismatches
		"{\"id\":\"x\x00y\",\"kind\":\"design\"}", // embedded null byte (invalid JSON control char)
		`[]`, `null`, `"`, `{}`, ``, // structural edge cases
		`{"id":"` + strings.Repeat("A", 70<<10) + `"}`, // over the cap
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		s := &Server{logger: quietLogger()}
		rec := httptest.NewRecorder()
		req := withHubCtx(httptest.NewRequest(http.MethodPost, "/api/pins", strings.NewReader(body)), &storeSet{hubID: "h1"})
		s.handlePinsAdd(rec, req)
		assertResilient(t, rec.Code, rec.Body.String(), body)
		// Only the contract statuses are acceptable here.
		switch rec.Code {
		case http.StatusOK, http.StatusBadRequest:
		default:
			t.Fatalf("unexpected status %d for body %q", rec.Code, body)
		}
	})
}

// FuzzRollupBody fuzzes the activity-rollup body. Validation (size cap, required
// fields, child-count cap) runs before the token lookup, so a well-formed body
// reaches the 401 gate and garbage is rejected at 400 — never 5xx.
func FuzzRollupBody(f *testing.F) {
	seeds := []string{
		`{"hubId":"h","itemId":"i","childItemIds":["a","b"]}`,
		`{"hubId":"h","itemId":"i"`, // truncated
		`{"hubId":42}`,              // type mismatch
		`{"childItemIds":"not-an-array"}`,
		`{}`, `null`, ``,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		s := &Server{logger: quietLogger()}
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/activity/rollup", strings.NewReader(body))
		s.handleActivityRollup(rec, req)
		assertResilient(t, rec.Code, rec.Body.String(), body)
		switch rec.Code {
		case http.StatusBadRequest, http.StatusUnauthorized:
		default:
			t.Fatalf("unexpected status %d for body %q", rec.Code, body)
		}
	})
}

// --- Boundary tables (deterministic; run under plain `go test`) -------------

// TestSetPortBoundaries drives the integer boundary values the port endpoint
// must reject. It deliberately avoids any in-range *different* port so the
// handler never performs a real bind/save/restart — only the validation branch
// (out-of-range -> 400) and the unchanged no-op (-> 200) are exercised.
func TestSetPortBoundaries(t *testing.T) {
	cases := []struct {
		name string
		port string
		want int
	}{
		{"negative", "-1", http.StatusBadRequest},
		{"zero", "0", http.StatusBadRequest},
		{"privileged_80", "80", http.StatusBadRequest},
		{"just_below_min", "1023", http.StatusBadRequest},
		{"just_above_max", "65536", http.StatusBadRequest},
		{"int64_max", "9223372036854775807", http.StatusBadRequest},
		{"overflow_beyond_int64", "99999999999999999999", http.StatusBadRequest}, // decode error
		{"unchanged_noop", "8080", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			s := &Server{logger: quietLogger(), portConfigurable: true, restartCh: make(chan struct{}, 1)}
			s.setAddr("0.0.0.0:8080")
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/settings/port",
				strings.NewReader(`{"port":`+tc.port+`}`))
			s.handleSetPort(rec, req)
			if rec.Code != tc.want {
				t.Errorf("port=%s: status = %d, want %d (body %q)", tc.port, rec.Code, tc.want, rec.Body.String())
			}
		})
	}
	_ = math.MaxInt64 // documents the intent of the int64_max case
}

// TestNullBytesAndLongStringsArePinData feeds null bytes and an oversized string
// through the pin path. Null bytes must be handled as opaque data (the hub id is
// sanitised for the on-disk filename), and an over-cap body must 400 rather than
// allocate unbounded memory.
func TestNullBytesAndLongStringsArePinData(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), ".config", "fusionlocalserver"), 0700); err != nil {
		t.Fatal(err)
	}
	s := &Server{logger: quietLogger()}

	t.Run("null_byte_in_hub_query", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := withHubCtx(httptest.NewRequest(http.MethodPost, "/api/pins",
			strings.NewReader(`{"id":"x","kind":"design"}`)), &storeSet{hubID: "h\x001"})
		s.handlePinsAdd(rec, req)
		if rec.Code >= 500 {
			t.Fatalf("null byte in hubId caused %d: %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("oversized_body_rejected", func(t *testing.T) {
		huge := `{"id":"x","kind":"design","name":"` + strings.Repeat("A", 1<<20) + `"}`
		rec := httptest.NewRecorder()
		req := withHubCtx(httptest.NewRequest(http.MethodPost, "/api/pins", strings.NewReader(huge)), &storeSet{hubID: "h1"})
		s.handlePinsAdd(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("oversized body: status = %d, want 400", rec.Code)
		}
	})
}

// TestRollupChildCountBoundary confirms the fan-out cap holds exactly at the
// boundary: maxRollupChildren entries pass validation (reaching the 401 gate),
// one more is rejected at 400 before any work is scheduled.
func TestRollupChildCountBoundary(t *testing.T) {
	mkBody := func(n int) string {
		ids := make([]string, n)
		for i := range ids {
			ids[i] = `"c"`
		}
		return `{"hubId":"h","itemId":"i","childItemIds":[` + strings.Join(ids, ",") + `]}`
	}
	s := &Server{logger: quietLogger()}

	// At the cap: passes validation, falls through to the (unauthenticated) 401.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/activity/rollup", strings.NewReader(mkBody(maxRollupChildren)))
	s.handleActivityRollup(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("at cap: status = %d, want 401 (validation should pass)", rec.Code)
	}

	// One over the cap: rejected at 400 before the token lookup.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/activity/rollup", strings.NewReader(mkBody(maxRollupChildren+1)))
	s.handleActivityRollup(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("over cap: status = %d, want 400", rec.Code)
	}
}

// --- Chat fuzzing (docs/chat/PLAN.md phase 5 security pass) ------------------
//
// Chat validation sits behind session auth and the project authorizer, so
// unlike the pins targets these go through the real mux with a live session
// and a fake (static) APS roster — the parse/validate path an authenticated
// attacker reaches. Rate limits are opened wide so fuzz inputs hit the
// decoder, not the limiter. Run with:
//
//	go test ./server/ -run xxx -fuzz FuzzChatMessageCreateBody -fuzztime 30s
//	go test ./server/ -run xxx -fuzz FuzzChatChannelCreateBody -fuzztime 30s
//	go test ./server/ -run xxx -fuzz FuzzChatMarkReadBody      -fuzztime 30s

// newChatFuzzHarness builds an in-process chat server plus an authenticated
// session cookie and the root channel id. The GraphQL fake serves one ACTIVE
// EDITOR ("u-fuzz") forever.
func newChatFuzzHarness(f *testing.F) (http.Handler, *http.Cookie, string) {
	f.Helper()
	const rosterJSON = `{"data":{"project":{"folderLevelProjectMembers":{"pagination":{"cursor":""},` +
		`"results":[{"role":"EDITOR","status":"ACTIVE","user":{"id":"u-fuzz","userName":"u-fuzz",` +
		`"firstName":"","lastName":"","email":"fuzz@x.io"}}]}}}}`
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(rosterJSON))
	}))
	f.Cleanup(gql.Close)
	f.Cleanup(api.SetGraphqlEndpointForTesting(gql.URL))

	authz := chat.NewAuthorizer()
	s := &Server{
		logger:      quietLogger(),
		clientID:    "fuzz-client",
		sessions:    NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger()),
		pending:     NewPendingStore(pendingTTL),
		hubs:        testHubStores(f, authz),
		chatAuthz:   authz,
		chatMsgLim:  chat.NewLimiter(1e9, 1e9), // out of the way: fuzz the decoder, not the limiter
		chatOpLim:   chat.NewLimiter(1e9, 1e9),
		chatSyncLim: chat.NewLimiter(1e9, 1e9),
	}
	sess, err := s.sessions.Create(
		&auth.TokenData{AccessToken: "tok-fuzz", ExpiresAt: time.Now().Add(24 * time.Hour)},
		auth.UserProfile{Sub: "u-fuzz", Name: "Fuzz", Email: "fuzz@x.io"},
	)
	if err != nil {
		f.Fatal(err)
	}
	s.sessions.SetSelectedHub(sess.ID, testHubID, testHubName)
	set, err := s.hubs.get(testHubID, testHubName)
	if err != nil {
		f.Fatal(err)
	}
	root, err := set.chat.EnsureRoot("urn:fuzz:project")
	if err != nil {
		f.Fatal(err)
	}
	return s.routes(), &http.Cookie{Name: sessionCookieName, Value: sess.ID}, root.ID
}

func chatFuzzDo(h http.Handler, cookie *http.Cookie, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(cookie)
	h.ServeHTTP(rec, req)
	return rec
}

// FuzzChatMessageCreateBody throws arbitrary bytes at the message-create
// body. Valid sends 201 (or 200 on a clientMsgId replay); everything else
// 400. Never a panic or 5xx; bodies are never echoed into logs.
func FuzzChatMessageCreateBody(f *testing.F) {
	h, cookie, root := newChatFuzzHarness(f)
	target := "/api/chat/messages?projectId=" + "urn%3Afuzz%3Aproject" + "&channelId=" + root
	seeds := []string{
		`{"body":"hi","clientMsgId":"cm-1"}`,
		`{"body":"reply","clientMsgId":"cm-2","threadRootSeq":1}`,
		`{"body":"","clientMsgId":"cm-3"}`,                                    // empty body
		`{"body":"x","clientMsgId":""}`,                                       // missing idempotency key
		`{"body":"x","clientMsgId":"c","threadRootSeq":-9}`,                   // bogus root
		`{"body":"` + strings.Repeat("A", 5000) + `","clientMsgId":"cm-big"}`, // over the rune cap
		"{\"body\":\"x\x00y\",\"clientMsgId\":\"cm-nul\"}",
		`{"body":`, `[]`, `null`, ``, `{}`,
		`{"body":"x","clientMsgId":"cm-huge","threadRootSeq":9223372036854775807}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		rec := chatFuzzDo(h, cookie, http.MethodPost, target, body)
		assertResilient(t, rec.Code, rec.Body.String(), body)
		switch rec.Code {
		case http.StatusCreated, http.StatusOK, http.StatusBadRequest:
		default:
			t.Fatalf("unexpected status %d for body %q -> %q", rec.Code, body, rec.Body.String())
		}
	})
}

// FuzzChatChannelCreateBody fuzzes channel creation: name/topic caps,
// duplicate names, private-channel member lists.
func FuzzChatChannelCreateBody(f *testing.F) {
	h, cookie, _ := newChatFuzzHarness(f)
	target := "/api/chat/channels?projectId=urn%3Afuzz%3Aproject"
	seeds := []string{
		`{"name":"a-channel"}`,
		`{"name":"a-channel"}`, // duplicate on the next run
		`{"name":"p","isPrivate":true,"memberIds":["u-2","","u-2"]}`,
		`{"name":""}`,
		`{"name":"` + strings.Repeat("N", 200) + `"}`,             // over the name cap
		`{"name":"t","topic":"` + strings.Repeat("T", 600) + `"}`, // over the topic cap
		`{"name":"x","isPrivate":"yes"}`,                          // type mismatch
		`{"name":`, `null`, ``, `{}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		rec := chatFuzzDo(h, cookie, http.MethodPost, target, body)
		assertResilient(t, rec.Code, rec.Body.String(), body)
		switch rec.Code {
		case http.StatusCreated, http.StatusBadRequest:
		default:
			t.Fatalf("unexpected status %d for body %q -> %q", rec.Code, body, rec.Body.String())
		}
	})
}

// FuzzChatMarkReadBody fuzzes the phase-4 read-cursor body: negative seqs
// 400, absurdly large ones clamp to the channel's newest seq, garbage 400.
func FuzzChatMarkReadBody(f *testing.F) {
	h, cookie, root := newChatFuzzHarness(f)
	target := "/api/chat/read?projectId=urn%3Afuzz%3Aproject&channelId=" + root
	seeds := []string{
		`{"lastReadSeq":0}`,
		`{"lastReadSeq":1}`,
		`{"lastReadSeq":-1}`,
		`{"lastReadSeq":9223372036854775807}`,
		`{"lastReadSeq":"nope"}`,
		`{"lastReadSeq":1.5}`,
		`{}`, `null`, ``, `[`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		rec := chatFuzzDo(h, cookie, http.MethodPatch, target, body)
		assertResilient(t, rec.Code, rec.Body.String(), body)
		switch rec.Code {
		case http.StatusOK, http.StatusBadRequest:
		default:
			t.Fatalf("unexpected status %d for body %q -> %q", rec.Code, body, rec.Body.String())
		}
	})
}

// TestChatNullByteParams feeds null bytes through the chat query params that
// end up in on-disk paths (project id → directory slug, channel id → log
// filename). The sanitizer must treat them as opaque data — no panic, no
// 5xx, and certainly no path escape.
func TestChatNullByteParams(t *testing.T) {
	s, _ := newChatTestServer(t)
	ts := httptest.NewServer(s.routes())
	t.Cleanup(ts.Close)
	editor := login(t, s, "u-editor", "Ed", "editor@x.io")

	for _, target := range []string{
		"/api/chat/channels?projectId=p%00q",
		"/api/chat/messages?projectId=" + chatTestProject + "&channelId=c%00x",
		"/api/chat/unreads?projectId=..%2F..%2Fetc",
	} {
		code := chatDo(t, ts.URL, http.MethodGet, target, editor, nil, nil)
		if code >= 500 {
			t.Errorf("%s -> %d", target, code)
		}
	}
}
