// Package schemameta defines the shared per-file schema stamp: when a data
// file was born, which app version created it, and when/what last rewrote
// it. Stores embed it as "schema" beside their existing top-level version
// int — the version stays where every loader's future-version guard already
// reads it; the stamp adds provenance ("data birthdate") for diagnostics,
// backups, and migrations.
package schemameta

import (
	"time"

	"github.com/schneik80/fusionlocalserver/internal/appver"
)

// Stamp is the provenance block persisted in every store file.
type Stamp struct {
	CreatedAt        time.Time `json:"createdAt"`
	CreatedByVersion string    `json:"createdByVersion"`
	UpdatedAt        time.Time `json:"updatedAt"`
	UpdatedByVersion string    `json:"updatedByVersion"`
}

// New returns a stamp for a file born now.
func New() Stamp {
	now := time.Now().UTC()
	v := appver.Get()
	return Stamp{CreatedAt: now, CreatedByVersion: v, UpdatedAt: now, UpdatedByVersion: v}
}

// Backfill returns a stamp for a pre-stamp file whose birth we can only
// approximate (typically the file's ModTime); createdBy records that the
// origin predates stamping.
func Backfill(createdAt time.Time) Stamp {
	v := appver.Get()
	return Stamp{
		CreatedAt:        createdAt.UTC(),
		CreatedByVersion: "pre-schema",
		UpdatedAt:        time.Now().UTC(),
		UpdatedByVersion: v,
	}
}

// Touch updates the Updated* half on every save. Zero Created* fields
// (files stamped by hand, or decode quirks) are repaired to now.
func (s *Stamp) Touch() {
	now := time.Now().UTC()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = now
	}
	if s.CreatedByVersion == "" {
		s.CreatedByVersion = "pre-schema"
	}
	s.UpdatedAt = now
	s.UpdatedByVersion = appver.Get()
}
