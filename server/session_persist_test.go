package server

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/schneik80/fusionlocalserver/auth"
)

func newPersistentStore(t *testing.T, dir string) *SessionStore {
	t.Helper()
	st := NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger())
	if err := st.EnablePersistence(dir); err != nil {
		t.Fatalf("EnablePersistence: %v", err)
	}
	return st
}

func TestSessionPersistence_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	a := newPersistentStore(t, dir)
	sess, err := a.Create(
		&auth.TokenData{AccessToken: "AT", RefreshToken: "RT", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{Name: "Ada", Email: "ada@x.io"},
	)
	if err != nil {
		t.Fatal(err)
	}

	// A fresh store over the same dir simulates a process restart.
	b := newPersistentStore(t, dir)
	got, ok := b.Get(sess.ID)
	if !ok {
		t.Fatal("session was not restored after restart")
	}
	if got.Profile.Email != "ada@x.io" {
		t.Errorf("restored profile = %+v", got.Profile)
	}
	tok := got.token.Load()
	if tok == nil || tok.AccessToken != "AT" || tok.RefreshToken != "RT" {
		t.Errorf("restored token = %+v, want AT/RT", tok)
	}
}

func TestSessionPersistence_DropsExpired(t *testing.T) {
	dir := t.TempDir()

	a := NewSessionStore(time.Hour, 7*24*time.Hour, quietLogger())
	if err := a.EnablePersistence(dir); err != nil {
		t.Fatal(err)
	}
	sess, _ := a.Create(&auth.TokenData{AccessToken: "AT"}, auth.UserProfile{})

	// Age the session past the idle TTL and re-persist.
	a.mu.Lock()
	a.byID[sess.ID].lastSeen = time.Now().Add(-2 * time.Hour)
	a.mu.Unlock()
	a.persist()

	b := NewSessionStore(time.Hour, 7*24*time.Hour, quietLogger())
	if err := b.EnablePersistence(dir); err != nil {
		t.Fatal(err)
	}
	if _, ok := b.Get(sess.ID); ok {
		t.Error("expired session was restored from disk")
	}
}

// TestSessionPersistence_DropsNotAllowed: the restore path never re-enters the
// OAuth callback, so the whitelist predicate must filter on load — otherwise a
// user removed from admin_users keeps a working session across every restart.
func TestSessionPersistence_DropsNotAllowed(t *testing.T) {
	dir := t.TempDir()

	a := newPersistentStore(t, dir)
	ada, _ := a.Create(
		&auth.TokenData{AccessToken: "AT", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{Sub: "sub-ada", Email: "ada@x.io"},
	)
	bob, _ := a.Create(
		&auth.TokenData{AccessToken: "AT", ExpiresAt: time.Now().Add(time.Hour)},
		auth.UserProfile{Sub: "sub-bob", Email: "bob@x.io"},
	)

	// Restart with a whitelist that no longer includes bob.
	b := NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger())
	b.allowed = func(p auth.UserProfile) bool { return p.Email == "ada@x.io" }
	if err := b.EnablePersistence(dir); err != nil {
		t.Fatalf("EnablePersistence: %v", err)
	}
	if _, ok := b.Get(ada.ID); !ok {
		t.Error("whitelisted session was not restored")
	}
	if _, ok := b.Get(bob.ID); ok {
		t.Error("removed user's session was restored from disk")
	}
}

func TestSessionPersistence_DisabledIsNoOp(t *testing.T) {
	// A store without EnablePersistence must not touch the filesystem.
	st := NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger())
	sess, _ := st.Create(&auth.TokenData{AccessToken: "AT"}, auth.UserProfile{})
	st.Delete(sess.ID)
	// No panic, no file — nothing to assert beyond reaching here.
}

func TestSessionPersistence_UndecryptableStartsFresh(t *testing.T) {
	dir := t.TempDir()

	a := newPersistentStore(t, dir)
	a.Create(&auth.TokenData{AccessToken: "AT", ExpiresAt: time.Now().Add(time.Hour)}, auth.UserProfile{})

	// Rotate the key out from under the file.
	newKey := make([]byte, 32)
	if _, err := rand.Read(newKey); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionKeyFile), newKey, 0600); err != nil {
		t.Fatal(err)
	}

	b := NewSessionStore(sessionIdleTTL, sessionAbsTTL, quietLogger())
	if err := b.EnablePersistence(dir); err != nil {
		t.Fatalf("EnablePersistence must not error on an undecryptable file: %v", err)
	}
	b.mu.Lock()
	n := len(b.byID)
	b.mu.Unlock()
	if n != 0 {
		t.Errorf("restored %d sessions from an undecryptable file, want 0", n)
	}
}

func TestEncryptDecryptGCM(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	ct, err := encryptGCM(key, []byte("hello sessions"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	pt, err := decryptGCM(key, ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(pt) != "hello sessions" {
		t.Errorf("roundtrip = %q", pt)
	}

	wrong := make([]byte, 32)
	if _, err := rand.Read(wrong); err != nil {
		t.Fatal(err)
	}
	if _, err := decryptGCM(wrong, ct); err == nil {
		t.Error("decrypt with the wrong key should fail")
	}
}
