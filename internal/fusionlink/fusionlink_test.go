package fusionlink

import (
	"errors"
	"testing"
)

func TestBuildAndParseRoundTrip(t *testing.T) {
	raw := BuildURL(ActionOpen, "tick-123", "https://fusion.example:8080")
	link, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse(%q): %v", raw, err)
	}
	if link.Action != ActionOpen {
		t.Errorf("Action = %q, want %q", link.Action, ActionOpen)
	}
	if link.Ticket != "tick-123" {
		t.Errorf("Ticket = %q, want %q", link.Ticket, "tick-123")
	}
	if link.Server != "https://fusion.example:8080" {
		t.Errorf("Server = %q, want %q", link.Server, "https://fusion.example:8080")
	}
}

func TestBuildURL_EscapesServer(t *testing.T) {
	// The origin contains ':' and '/', which must not run into the query.
	raw := BuildURL(ActionInsert, "t", "https://host:8080")
	if want := "fusionlocal://v1/insert?ticket=t&server=https%3A%2F%2Fhost%3A8080"; raw != want {
		t.Errorf("BuildURL = %q, want %q", raw, want)
	}
}

func TestParse_Rejects(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"wrong scheme", "https://v1/open?ticket=t"},
		{"the app's own card-token scheme", "fls:doc?id=x"},
		{"unknown version", "fusionlocal://v9/open?ticket=t"},
		{"unknown action", "fusionlocal://v1/delete?ticket=t"},
		{"no action", "fusionlocal://v1/?ticket=t"},
		{"no ticket", "fusionlocal://v1/open"},
		{"empty ticket", "fusionlocal://v1/open?ticket="},
		{"empty string", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Parse(tc.raw); err == nil {
				t.Errorf("Parse(%q): want error, got nil", tc.raw)
			} else if !errors.Is(err, ErrNotFusionLink) {
				t.Errorf("Parse(%q) error = %v, want it to wrap ErrNotFusionLink", tc.raw, err)
			}
		})
	}
}

func TestParse_AcceptsOpaqueAndUppercaseScheme(t *testing.T) {
	// Some OS handlers hand back the authority-less spelling, or normalize the
	// scheme's case. Both must still resolve to the same action.
	for _, raw := range []string{
		"fusionlocal:v1/open?ticket=t",
		"FusionLocal://v1/open?ticket=t",
	} {
		link, err := Parse(raw)
		if err != nil {
			t.Errorf("Parse(%q): %v", raw, err)
			continue
		}
		if link.Action != ActionOpen || link.Ticket != "t" {
			t.Errorf("Parse(%q) = %+v, want open/t", raw, link)
		}
	}
}

func TestParse_ServerIsNotValidatedHere(t *testing.T) {
	// Parsing is shape only. Whether the server may be talked to is an
	// authorization decision the helper's pairing check owns — if Parse
	// silently dropped a hostile server, the refusal would never happen.
	link, err := Parse("fusionlocal://v1/open?ticket=t&server=https%3A%2F%2Fevil.example")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if link.Server != "https://evil.example" {
		t.Errorf("Server = %q, want the raw value preserved for the caller to judge", link.Server)
	}
}

func TestNormalizeOrigin(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://Host:8080/", "https://host:8080"},
		{"https://host:8080/some/path?q=1", "https://host:8080"},
		{"HTTP://host", "http://host"},
		{"  https://host  ", "https://host"},
		// Not absolute http(s): must be refused, never treated as a wildcard.
		{"host:8080", ""},
		{"file:///etc/passwd", ""},
		{"fusionlocal://v1/open", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := NormalizeOrigin(tc.in); got != tc.want {
			t.Errorf("NormalizeOrigin(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNormalizeOrigin_PortAndSchemeAreSignificant(t *testing.T) {
	// Pairing compares normalized origins, so these must not collapse: an
	// http downgrade or a different port is a different server.
	a := NormalizeOrigin("https://host:8080")
	for _, other := range []string{"http://host:8080", "https://host:9090", "https://other:8080"} {
		if b := NormalizeOrigin(other); a == b {
			t.Errorf("NormalizeOrigin(%q) collapsed into %q", other, a)
		}
	}
}

func TestValidCode(t *testing.T) {
	for _, c := range []string{CodeNotRunning, CodeWrongHub, CodeNoActiveDesign, CodeFailed} {
		if !ValidCode(c) {
			t.Errorf("ValidCode(%q) = false, want true", c)
		}
	}
	for _, c := range []string{"", "whatever", "fusion_ok", "FUSION_NOT_RUNNING"} {
		if ValidCode(c) {
			t.Errorf("ValidCode(%q) = true, want false", c)
		}
	}
}
