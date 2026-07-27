package docref

import "testing"

const item = "urn:adsk.wipprod:dm.lineage:hC6k4hndRWaeIVhIjvHu8w"

// encoded is how URLSearchParams stores the token: the urn's colons are
// percent-escaped, which is exactly why the prefilter keys off the last
// segment rather than the whole id.
const encoded = "fls:doc?hubId=b.1&itemId=urn%3Aadsk.wipprod%3Adm.lineage%3AhC6k4hndRWaeIVhIjvHu8w&name=bracket&kind=design"

func TestKeySurvivesEncoding(t *testing.T) {
	key := Key(item)
	if key != "hC6k4hndRWaeIVhIjvHu8w" {
		t.Fatalf("Key = %q", key)
	}
	if !MayContainString(encoded, item) {
		t.Fatal("prefilter rejected an encoded token that does reference the item")
	}
}

func TestPrefilterNeedsBothPrefixAndKey(t *testing.T) {
	if MayContain([]byte("a mention of hC6k4hndRWaeIVhIjvHu8w with no token"), item) {
		t.Error("prefilter passed a blob with no fls:doc token")
	}
	if MayContain([]byte("fls:doc?itemId=urn%3Aadsk%3Adm.lineage%3Aother"), item) {
		t.Error("prefilter passed a blob with a token for a different item")
	}
	if MayContain(nil, "") {
		t.Error("empty item id must never prefilter to a match")
	}
	// The weaker prefilter is for stores that hold the urn as a plain field.
	if !MayMention([]byte(`{"itemId":"`+item+`"}`), item) {
		t.Error("MayMention missed a plain-field urn")
	}
}

func TestCountMatchesOnlyRealTokens(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"plain urn is not a token", "see " + item, 0},
		{"one token", "look at " + encoded, 1},
		{"trailing punctuation is not swallowed", "see " + encoded + ". thanks", 1},
		{"two tokens", encoded + " and " + encoded, 2},
		{"markdown link form", "[bracket](" + encoded + ")", 1},
		{"different item", "fls:doc?hubId=b.1&itemId=urn%3Aadsk%3Adm.lineage%3Aother", 0},
		{"suffix collision", "fls:doc?itemId=urn%3Aadsk%3Adm.lineage%3AXXhC6k4hndRWaeIVhIjvHu8w", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Count(tc.body, item); got != tc.want {
				t.Fatalf("Count = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestMatchesToken(t *testing.T) {
	if !MatchesToken(encoded, item) {
		t.Error("stored token should match its own item")
	}
	if MatchesToken("fls:task?projectId=p&taskId=t1", item) {
		t.Error("a task token is not a doc reference")
	}
	if MatchesToken(encoded, "") {
		t.Error("empty item id must not match")
	}
}
