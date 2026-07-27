package notifications

import (
	"net/url"
	"regexp"
	"strings"
)

// MentionToken is the inline card-token scheme for an @mention, mirroring the
// repo's other fls: tokens (fls:doc / fls:task / fls:job / fls:batch). It is
// authored by the composer (which knows the roster) and stored verbatim in
// the plain-text body, so the SERVER can resolve mention targets by parsing
// the body alone — no APS roster fetch, which matters under the per-minute
// cost quota. The client unfurls it to a highlighted "@Name" at render time.
const MentionToken = "fls:user"

// mentionRe matches a well-formed fls:user token. The character class is the
// same one web/src/components/reftokens.ts uses for every fls: token, so both
// ends split identically; malformed tokens simply don't match and stay text.
var mentionRe = regexp.MustCompile(`fls:user\?[A-Za-z0-9*\-._%&=+]+`)

// Mention is a resolved @mention: the target's stable id and the display name
// captured in the token.
type Mention struct {
	UserID string
	Name   string
}

// ParseMentions extracts the unique @mention targets from a message body,
// preserving first-seen order and dropping tokens without an id. Duplicate
// mentions of the same user collapse to one (you get one notification even if
// named twice in a message).
func ParseMentions(body string) []Mention {
	matches := mentionRe.FindAllString(body, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]Mention, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	for _, tok := range matches {
		q := tok[len(MentionToken)+1:] // drop "fls:user?"
		vals, err := url.ParseQuery(q)
		if err != nil {
			continue
		}
		id := strings.TrimSpace(vals.Get("id"))
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, Mention{UserID: id, Name: strings.TrimSpace(vals.Get("name"))})
	}
	return out
}
