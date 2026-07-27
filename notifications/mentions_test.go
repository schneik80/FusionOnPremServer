package notifications

import "testing"

func TestParseMentions(t *testing.T) {
	body := "hey fls:user?id=sub-abc&name=Ada%20Lovelace can you review? also fls:user?id=sub-xyz&name=Bob"
	got := ParseMentions(body)
	if len(got) != 2 {
		t.Fatalf("got %d mentions, want 2: %+v", len(got), got)
	}
	if got[0].UserID != "sub-abc" || got[0].Name != "Ada Lovelace" {
		t.Fatalf("first mention = %+v", got[0])
	}
	if got[1].UserID != "sub-xyz" || got[1].Name != "Bob" {
		t.Fatalf("second mention = %+v", got[1])
	}
}

func TestParseMentionsDedupes(t *testing.T) {
	body := "fls:user?id=x&name=X and again fls:user?id=x&name=X"
	got := ParseMentions(body)
	if len(got) != 1 {
		t.Fatalf("duplicate mention should collapse to 1, got %d", len(got))
	}
}

func TestParseMentionsIgnoresMalformed(t *testing.T) {
	for _, body := range []string{
		"no mentions here",
		"fls:user?name=NoId", // missing id → dropped
		"fls:userbroken",     // not a token
		"fls:doc?id=urn:foo", // different scheme
	} {
		if got := ParseMentions(body); len(got) != 0 {
			t.Fatalf("body %q yielded %+v, want none", body, got)
		}
	}
}

func TestParseMentionsNoneReturnsNil(t *testing.T) {
	if got := ParseMentions(""); got != nil {
		t.Fatalf("empty body should return nil, got %+v", got)
	}
}
