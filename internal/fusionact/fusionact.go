// Package fusionact performs one Fusion desktop action and reduces whatever
// happened to a stable outcome code.
//
// It sits between fusionmcp (how to talk to Fusion) and fusionlink (what the
// outcome is called) so that the two callers — the server's same-machine fast
// path and the helper app — cannot classify the same failure differently. A
// user who moves from a local server to a hosted one must get the same
// explanation for the same problem.
package fusionact

import (
	"context"
	"errors"

	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
	"github.com/schneik80/fusionlocalserver/internal/fusionmcp"
)

// Perform runs action against fileID and returns "" on success, or one of the
// fusionlink.Code* tokens. It never returns a raw error: the caller's job is to
// report a code, and an unclassifiable failure is deliberately flattened to
// CodeFailed rather than leaking a Python traceback or a dial error to a UI.
func Perform(ctx context.Context, c *fusionmcp.Client, action, fileID string) string {
	var err error
	switch action {
	case fusionlink.ActionOpen:
		err = c.OpenDocument(ctx, fileID)
	case fusionlink.ActionInsert:
		err = c.InsertDocument(ctx, fileID)
	default:
		return fusionlink.CodeFailed
	}
	return Classify(err)
}

// Classify reduces an error from the MCP client to an outcome code.
func Classify(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, fusionmcp.ErrUnreachable):
		return fusionlink.CodeNotRunning
	case errors.Is(err, fusionmcp.ErrNoActiveDesign):
		return fusionlink.CodeNoActiveDesign
	default:
		return fusionlink.CodeFailed
	}
}

// CheckHub verifies that the Fusion this client talks to is signed in to the
// same hub as the document being acted on, by looking for the document's
// project in Fusion's active-hub project list. Returns "" when it matches.
//
// This is worth a round-trip because the failure it prevents is confusing
// rather than loud: Fusion would simply report that it cannot find the file,
// with no hint that the real problem is which account it is signed in to.
//
// A dmProjectID we cannot normalize is NOT treated as a mismatch — the id
// format is APS's, not ours, and refusing to act because we failed to parse it
// would break the feature the next time that format changes.
func CheckHub(ctx context.Context, c *fusionmcp.Client, dmProjectID string) string {
	projects, err := c.ActiveHubProjects(ctx)
	if err != nil {
		return Classify(err)
	}
	want := fusionmcp.NormalizeProjectID(dmProjectID)
	if want == "" {
		return ""
	}
	for _, p := range projects {
		if p.ID == want {
			return ""
		}
	}
	return fusionlink.CodeWrongHub
}
