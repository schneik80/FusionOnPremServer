package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/schneik80/fusionlocalserver/auth"
)

// Session persistence: the in-memory store is mirrored to an AES-256-GCM
// encrypted file so a process restart doesn't log everyone out. The file holds
// APS refresh tokens, so it is encrypted at rest with a key kept beside it
// (mode 0600). This is "better than plaintext", not a substitute for OS
// keychain storage — the threat it addresses is a casual read of the file, not
// an attacker who already has the user's home directory.

const (
	sessionsFile   = "sessions.enc"
	sessionKeyFile = "session.key"
)

// persistedSession is the serialisable form of a Session (the mutex is dropped
// and recreated on load; the token is read from its atomic holder).
type persistedSession struct {
	ID        string           `json:"id"`
	Profile   auth.UserProfile `json:"profile"`
	CreatedAt time.Time        `json:"created_at"`
	LastSeen  time.Time        `json:"last_seen"`
	Token     *auth.TokenData  `json:"token"`
	// Additive (older files simply load these as empty): the session's hub
	// lock, so a restart doesn't force everyone back through the hub gate.
	SelectedHubID   string `json:"selected_hub_id,omitempty"`
	SelectedHubName string `json:"selected_hub_name,omitempty"`
}

// EnablePersistence points the store at <dir> for its encrypted session file
// and key, then loads any sessions already on disk (dropping expired ones).
// Without this call the store is in-memory only (the default for tests).
func (s *SessionStore) EnablePersistence(dir string) error {
	s.persistPath = filepath.Join(dir, sessionsFile)
	s.keyPath = filepath.Join(dir, sessionKeyFile)
	return s.load()
}

// persist writes the current sessions to disk, encrypted. It is best-effort:
// failures are logged, never surfaced to a request. No-op when persistence is
// disabled.
func (s *SessionStore) persist() {
	if s.persistPath == "" {
		return
	}
	s.saveMu.Lock()
	defer s.saveMu.Unlock()

	snap := s.snapshot()
	plaintext, err := json.Marshal(snap)
	if err != nil {
		s.logger.Warn("sessions: marshal failed; not persisted", "err", err)
		return
	}
	key, err := s.loadOrCreateKey()
	if err != nil {
		s.logger.Warn("sessions: key unavailable; not persisted", "err", err)
		return
	}
	blob, err := encryptGCM(key, plaintext)
	if err != nil {
		s.logger.Warn("sessions: encrypt failed; not persisted", "err", err)
		return
	}
	if err := writeFileAtomic(s.persistPath, blob, 0600); err != nil {
		s.logger.Warn("sessions: write failed; not persisted", "err", err)
	}
}

// snapshot copies the live sessions into their serialisable form under the map
// lock. Tokens are read atomically, so a concurrent refresh can't tear them.
func (s *SessionStore) snapshot() []persistedSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]persistedSession, 0, len(s.byID))
	for _, sess := range s.byID {
		hubID, hubName := sess.SelectedHub()
		out = append(out, persistedSession{
			ID:              sess.ID,
			Profile:         sess.Profile,
			CreatedAt:       sess.CreatedAt,
			LastSeen:        sess.lastSeen,
			Token:           sess.token.Load(),
			SelectedHubID:   hubID,
			SelectedHubName: hubName,
		})
	}
	return out
}

// load reads the encrypted session file and repopulates the store, skipping
// any session already past a deadline. A missing file is not an error; a
// decrypt/parse failure is logged and treated as "start empty" (e.g. the key
// was rotated) rather than blocking startup.
func (s *SessionStore) load() error {
	blob, err := os.ReadFile(s.persistPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	key, err := s.loadOrCreateKey()
	if err != nil {
		return err
	}
	plaintext, err := decryptGCM(key, blob)
	if err != nil {
		s.logger.Warn("sessions: could not decrypt session file; starting fresh", "err", err)
		return nil
	}
	var snap []persistedSession
	if err := json.Unmarshal(plaintext, &snap); err != nil {
		s.logger.Warn("sessions: could not parse session file; starting fresh", "err", err)
		return nil
	}

	now := time.Now()
	s.mu.Lock()
	for _, ps := range snap {
		sess := &Session{ID: ps.ID, Profile: ps.Profile, CreatedAt: ps.CreatedAt, lastSeen: ps.LastSeen}
		sess.setSelectedHub(ps.SelectedHubID, ps.SelectedHubName)
		sess.token.Store(ps.Token)
		if s.expired(sess, now) {
			continue
		}
		s.byID[ps.ID] = sess
	}
	n := len(s.byID)
	s.mu.Unlock()
	if n > 0 {
		s.logger.Info("sessions: restored from disk", "count", n)
	}
	return nil
}

// loadOrCreateKey returns the 32-byte AES key, generating and persisting it
// (mode 0600) on first use.
func (s *SessionStore) loadOrCreateKey() ([]byte, error) {
	key, err := os.ReadFile(s.keyPath)
	if err == nil {
		if len(key) != 32 {
			return nil, fmt.Errorf("session key is %d bytes, want 32", len(key))
		}
		return key, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	key = make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(s.keyPath, key, 0600); err != nil {
		return nil, err
	}
	return key, nil
}

// encryptGCM seals plaintext with AES-256-GCM, returning nonce||ciphertext.
func encryptGCM(key, plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// decryptGCM reverses encryptGCM.
func decryptGCM(key, blob []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(blob) < gcm.NonceSize() {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ct := blob[:gcm.NonceSize()], blob[gcm.NonceSize():]
	return gcm.Open(nil, nonce, ct, nil)
}

// writeFileAtomic writes data to a temp file in the same directory and renames
// it over path, so a crash mid-write never leaves a half-written file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
