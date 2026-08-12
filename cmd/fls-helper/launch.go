package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/fusionact"
	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
	"github.com/schneik80/fusionlocalserver/internal/fusionmcp"
)

// launchTimeout bounds one whole invocation: redeem the ticket, ask Fusion,
// report back. The user is staring at a browser tab waiting for something to
// happen, so this fails fast rather than hanging around.
const launchTimeout = 45 * time.Second

// serverTimeout caps one call to fusionlocalserver. These are small JSON
// round-trips on a LAN, and a slow server should not stall the Fusion call.
const serverTimeout = 15 * time.Second

// ticketPayload mirrors the server's FusionTicketDTO.
type ticketPayload struct {
	Action      string `json:"action"`
	FileID      string `json:"fileId"`
	DMProjectID string `json:"dmProjectId"`
	DocName     string `json:"docName"`
}

// runLaunch handles a fusionlocal:// invocation end to end and returns the
// process exit code. It reports every outcome it can back to the server, and
// shows a native message on failure — the browser tab that started this cannot
// be written to, so a silent failure would look like nothing happened at all.
func runLaunch(raw string) int {
	link, err := fusionlink.Parse(raw)
	if err != nil {
		// A malformed URL is not something the user did; do not alarm them
		// with a dialog, just record it.
		fmt.Fprintf(os.Stderr, "fls-helper: %v\n", err)
		logLaunch("launch rejected: %v", err)
		return 2
	}
	logLaunch("launch %s ticket=%s server=%s", link.Action, redactTicket(link.Ticket),
		displayOrigin(link.Server))

	server, ok := trusted(link.Server)
	if !ok {
		// The one refusal with no server to report to — by definition we do
		// not trust it, so we must not call it either.
		//
		// Log before notifying, here and below: notify() blocks on a modal
		// dialog, so a record written afterwards does not exist until someone
		// clicks OK — and an unattended failure is exactly the one worth having
		// a record of.
		fmt.Fprintf(os.Stderr, "fls-helper: refusing unpaired server %q\n", link.Server)
		logLaunch("  refused: %s is not paired with this computer", displayOrigin(link.Server))
		notify("Fusion Local Server helper",
			fmt.Sprintf("Refused a request from %s.\n\nThis server is not paired with this computer. "+
				"If you trust it, run:\n\n    fls-helper pair %s",
				displayOrigin(link.Server), displayOrigin(link.Server)))
		return 1
	}
	origin := server.Origin
	client := httpClientFor(server)

	ctx, cancel := context.WithTimeout(context.Background(), launchTimeout)
	defer cancel()

	payload, err := redeemTicket(ctx, client, origin, link.Ticket)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fls-helper: redeeming ticket: %v\n", err)
		logLaunch("  failed: could not redeem the ticket: %v", err)
		notify("Fusion Local Server helper",
			"Could not confirm this request with the server.\n\n"+err.Error()+
				"\n\nThe request may have expired — try again from the browser.")
		return 1
	}
	// The URL and the ticket must agree. They always will in practice; if they
	// ever don't, the ticket is the authority — it came from the server over
	// HTTPS, while the URL came through the OS.
	action := payload.Action
	if !fusionlink.ValidAction(action) {
		action = link.Action
	}

	mcp := fusionmcp.NewClient()

	// Check that Fusion is up and on the right hub before acting. Both answers
	// are things we can explain; "file not found" from Fusion is not.
	code := fusionact.CheckHub(ctx, mcp, payload.DMProjectID)
	if code == "" {
		code = fusionact.Perform(ctx, mcp, action, payload.FileID)
	}

	report(ctx, client, origin, link.Ticket, code)
	if code != "" {
		logLaunch("  failed: %s (%s)", code, payload.DocName)
		notify("Fusion Local Server helper", explain(code, action, payload.DocName))
		return 1
	}
	logLaunch("  ok: %s %s", action, payload.DocName)
	return 0
}

// redeemTicket collects the action payload. The ticket is single-use, so this
// is also what tells the server the helper actually ran.
func redeemTicket(ctx context.Context, client *http.Client, origin, ticket string) (ticketPayload, error) {
	u := origin + "/api/fusion/ticket?ticket=" + url.QueryEscape(ticket)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return ticketPayload{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return ticketPayload{}, fmt.Errorf("cannot reach %s", displayOrigin(origin))
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return ticketPayload{}, fmt.Errorf("the server declined the request (HTTP %d)", resp.StatusCode)
	}
	var p ticketPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return ticketPayload{}, fmt.Errorf("unexpected reply from the server")
	}
	if p.FileID == "" {
		return ticketPayload{}, fmt.Errorf("the server sent no document to act on")
	}
	return p, nil
}

// report posts the outcome back. Best effort: if it fails, the user still got a
// native message, and the ticket ages out on its own.
func report(ctx context.Context, client *http.Client, origin, ticket, code string) {
	body, err := json.Marshal(map[string]any{
		"ticket": ticket,
		"ok":     code == "",
		"code":   code,
	})
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		origin+"/api/fusion/callback", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// The server enforces same-origin on browser traffic; this is a native
	// client, so it says so rather than forging a browser's Origin.
	req.Header.Set("User-Agent", "fls-helper/"+version)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fls-helper: reporting outcome: %v\n", err)
		return
	}
	resp.Body.Close()
}

// explain turns an outcome code into the sentence shown natively. These are the
// helper's own strings, in English: this process runs outside the browser and
// has no access to the SPA's locale or its catalogs. The SPA shows the same
// outcomes localized — this is the fallback for when nobody is watching the tab.
func explain(code, action, docName string) string {
	what := "open"
	if action == fusionlink.ActionInsert {
		what = "insert"
	}
	subject := "the document"
	if docName != "" {
		subject = docName
	}
	switch code {
	case fusionlink.CodeNotRunning:
		return fmt.Sprintf("Cannot %s %s: Autodesk Fusion is not running on this computer.\n\n"+
			"Start Fusion, then try again.", what, subject)
	case fusionlink.CodeWrongHub:
		return fmt.Sprintf("Cannot %s %s: Fusion is signed in to a different hub, "+
			"so it cannot see this document.\n\nSwitch hubs in Fusion, then try again.", what, subject)
	case fusionlink.CodeNoActiveDesign:
		return fmt.Sprintf("Cannot insert %s: Fusion has no design open to insert it into.\n\n"+
			"Open or create a design, then try again.", subject)
	default:
		return fmt.Sprintf("Fusion could not %s %s.", what, subject)
	}
}

// displayOrigin is what we put in a message shown to a person. An unparseable
// server is rendered as a placeholder rather than echoed: the string came from
// a URL anyone could have constructed, and a dialog is a poor place to render
// attacker-chosen text.
func displayOrigin(raw string) string {
	if o := fusionlink.NormalizeOrigin(raw); o != "" {
		return o
	}
	return "an unrecognized address"
}
