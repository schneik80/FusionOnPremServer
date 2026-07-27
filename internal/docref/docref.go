// Package docref reads the fls:doc reference tokens the web app stores inline
// in chat message bodies, task doc-ref lists, batch refs and whiteboard cards
// (web/src/components/doccard/docref.ts is the writer). It answers the one
// question a reverse lookup — "which of our own records mention this
// document?" — needs to ask of an arbitrary blob.
//
// The lookup is designed around a cheap prefilter. A stored token carries the
// item's lineage urn percent-encoded, so the raw urn is NOT a substring of the
// bytes on disk; Key extracts the trailing segment, which survives
// form-urlencoding untouched and is unique enough to reject whole files (a
// 24 MiB whiteboard document, a channel's whole message log) without parsing
// them. Only blobs that survive the prefilter get decoded and compared in
// full, so a coincidental suffix can never produce a false hit.
package docref

import (
	"bytes"
	"net/url"
	"strings"
)

// Prefix is the token scheme. Mirrors DOC_REF_PREFIX on the web side.
const Prefix = "fls:doc?"

// Key returns the substring of itemID that is guaranteed to appear verbatim
// inside an encoded token — the last colon-separated segment of the lineage
// urn ("urn:adsk.wipprod:dm.lineage:AbC-123" → "AbC-123"). Lineage ids are
// base64url-ish (alphanumerics plus '-' and '_'), none of which
// form-urlencoding rewrites. Returns "" for an itemID with no usable segment,
// which callers must read as "cannot prefilter" (MayContain says no).
func Key(itemID string) string {
	if i := strings.LastIndexByte(itemID, ':'); i >= 0 {
		return itemID[i+1:]
	}
	return itemID
}

// MayContain reports whether blob could hold a token for itemID. False is
// definitive (the bytes cannot reference it); true means "parse it properly"
// — use Count for the answer.
func MayContain(blob []byte, itemID string) bool {
	key := Key(itemID)
	if key == "" {
		return false
	}
	return bytes.Contains(blob, []byte(Prefix)) && bytes.Contains(blob, []byte(key))
}

// MayMention is the weaker prefilter for stores that hold the lineage urn as a
// plain field rather than an encoded token (production's version-pinned
// DocSnapshot): it looks for the key alone, so it also passes blobs that
// reference the document without ever spelling out an fls:doc token.
func MayMention(blob []byte, itemID string) bool {
	key := Key(itemID)
	if key == "" {
		return false
	}
	return bytes.Contains(blob, []byte(key))
}

// MayContainString is MayContain for text already in memory as a string.
func MayContainString(s, itemID string) bool {
	key := Key(itemID)
	if key == "" {
		return false
	}
	return strings.Contains(s, Prefix) && strings.Contains(s, key)
}

// Count returns how many fls:doc tokens in s reference itemID exactly. Text
// that merely contains the lineage id (a pasted urn, a coincidental suffix)
// counts zero — only a decoded token's itemId parameter matches.
func Count(s, itemID string) int {
	if itemID == "" {
		return 0
	}
	n := 0
	for i := 0; i < len(s); {
		j := strings.Index(s[i:], Prefix)
		if j < 0 {
			break
		}
		start := i + j + len(Prefix)
		end := start
		for end < len(s) && isTokenByte(s[end]) {
			end++
		}
		if tokenItemID(s[start:end]) == itemID {
			n++
		}
		i = start // always advances: j >= 0 and Prefix is non-empty
	}
	return n
}

// Matches reports whether s references itemID at least once.
func Matches(s, itemID string) bool { return Count(s, itemID) > 0 }

// MatchesToken reports whether a single stored token (a task's docRefs entry,
// a batch's refs entry — the whole string is the token) references itemID.
func MatchesToken(token, itemID string) bool {
	if itemID == "" || !strings.HasPrefix(token, Prefix) {
		return false
	}
	return tokenItemID(token[len(Prefix):]) == itemID
}

// tokenItemID decodes a token's query string and returns its itemId. A partial
// decode is used as-is: url.ParseQuery reports an error but still returns
// every pair it could read, and a malformed neighbour must not hide a valid
// itemId.
func tokenItemID(q string) string {
	v, _ := url.ParseQuery(q)
	return v.Get("itemId")
}

// isTokenByte is exactly the application/x-www-form-urlencoded alphabet
// URLSearchParams emits (same character class as splitDocRefs on the web
// side), so trailing punctuation — "…token." or "…token)" in a chat body —
// never gets absorbed into the token.
func isTokenByte(b byte) bool {
	switch {
	case b >= 'A' && b <= 'Z', b >= 'a' && b <= 'z', b >= '0' && b <= '9':
		return true
	}
	switch b {
	case '*', '-', '.', '_', '%', '&', '=', '+':
		return true
	}
	return false
}
