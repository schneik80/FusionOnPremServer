package api

import "testing"

func TestVersionNumberFromURN(t *testing.T) {
	cases := []struct {
		urn  string
		want int
	}{
		{"urn:adsk.wipprod:fs.file:vf.AbC-123?version=3", 3},
		{"urn:adsk.wipprod:fs.file:vf.AbC-123?foo=1&version=12", 12},
		{"urn:adsk.wipprod:fs.file:vf.AbC-123", 0},
		{"urn:adsk.wipprod:fs.file:vf.AbC-123?version=", 0},
		{"urn:adsk.wipprod:fs.file:vf.AbC-123?version=-2", 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := VersionNumberFromURN(c.urn); got != c.want {
			t.Errorf("VersionNumberFromURN(%q) = %d, want %d", c.urn, got, c.want)
		}
	}
}

func TestVersionBelongsToItem(t *testing.T) {
	lineage := "urn:adsk.wipprod:dm.lineage:AbC-123"
	cases := []struct {
		version string
		want    bool
	}{
		{"urn:adsk.wipprod:fs.file:vf.AbC-123?version=3", true},
		{"urn:adsk.wipprod:fs.file:vf.AbC-123", true},
		{"urn:adsk.wipprod:fs.file:vf.Other-999?version=1", false},
		{"", false},
	}
	for _, c := range cases {
		if got := VersionBelongsToItem(c.version, lineage); got != c.want {
			t.Errorf("VersionBelongsToItem(%q) = %v, want %v", c.version, got, c.want)
		}
	}
	if VersionBelongsToItem("urn:adsk.wipprod:fs.file:vf.AbC-123?version=1", "") {
		t.Errorf("empty lineage must not match")
	}
}
