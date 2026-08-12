# Fusion document actions — status

Three actions on the details header of a **Fusion-native document**: **Open**,
**Insert** and **Archive**.

**The problem it solves.** Until now the details panel was read-only. You could
learn everything about a design — its history, its BOM, what references it — and
do nothing with it. Getting the design *into Fusion* meant finding it again by
hand in the desktop client; getting a *copy out* was not possible at all, because
a Fusion-native design has no downloadable storage object of its own.

## What each action is

| Action | Kinds | What happens |
|---|---|---|
| **Open** | all native | The user's running Fusion desktop client opens the document by lineage urn. |
| **Insert** | `design`, `configured` only | The document is inserted as an occurrence into the design Fusion currently has open. |
| **Archive** | all native | APS generates an **F3Z or F3D** of the tip version; the bell announces it and the row downloads it. |

Insert is narrower than the other two on purpose: Fusion inserts an *occurrence*
into an open design, which is meaningless for a drawing or a schematic. Offering
it there would produce a failure inside Fusion with an unhelpful message.

---

## Archive

### The API, and why it isn't Model Derivative

`api/files.go` has said for a long time that *"Fusion-native designs and drawings
have no downloadable OSS storage"*. That is true of the **version's own** storage
object — and it is why the Preview tab's download only ever worked for uploaded
files. It is **not** true that no archive can exist: the Data Management API will
*build* one on request.

```
GET  /data/v1/projects/{p}/versions/{v}/downloadFormats  -> which formats this version can produce
POST /data/v1/projects/{p}/downloads                     -> 202, a job id
GET  /data/v1/projects/{p}/jobs/{job}                    -> 200 while working, 303 + Location when done
GET  {the Location}                                      -> a pre-signed CDN url
GET  {that url}                                          -> the bytes
```

All of it lives in `api/archive.go`. Unlike every other download in this app,
the last mile does **not** go through `signedS3DownloadURL` — see below.

The details worth keeping, most of them learned the hard way:

- **The 303 is the completion signal**, so it must not be followed. `api/archive.go`
  uses a dedicated `noRedirectClient` (sharing `httpClient`'s connection pool);
  following the redirect would collapse "finished" and "still working" into two
  200s that differ only by payload shape. APS has also been observed answering
  the finished document on a plain 200, so `PollDownloadJob` accepts both.
- **Poll `/jobs/{jobId}`, not `/downloads/{jobId}`.** This was got wrong in both
  directions during development, so it is worth stating plainly: the id the
  create call returns is a **job** id — it base64-decodes to
  `business:<hub>#<project>#export:<n>` — and `GET /downloads/{that}` answers
  `404 UNKNOWN_ENTITY "Download not found"`. The download has its own id, and
  the **only** place it is ever revealed is the `Location` header of the job's
  303. (A third-party implementation of this flow polls `/downloads/{id}`; that
  does not work against this API surface, and trying it was a regression.)
- **The `Location` URI is used verbatim, not mined for an id** — the download's
  id appears nowhere else, so it must not be reconstructed.
  `downloadURLFromLocation` resolves the header against the DM base and
  **refuses any other host**: the URL is later fetched with the user's bearer
  token, and a redirect target is upstream-controlled data.
- **The bytes come from `relationships.storage.meta.link.href` — nothing is
  signed.** The download document carries a ready-made CDN url:

  ```
  https://cdn.us.oss.api.autodesk.com/oss/v2/signedresources/<uuid>
      ?region=US&response-content-disposition=attachment%3Bfilename*%3DUTF-8''<uuid>.f3d
  ```

  **The storage object id beside it is not an OSS key.** It reads
  `urn:adsk.objects:os.object:wip.dm.prod/UTF-8%27%27<uuid>.f3d`, and that
  `UTF-8''<uuid>.f3d` is the RFC 5987 `filename*` from the very same response's
  content-disposition, leaking into the urn. Calling `signeds3download` on it
  answers `{"reason":"Object not found"}` for **every** spelling — escaped once,
  verbatim, prefix-stripped, escaped twice — because there is no such object.
  Signing is kept only as a fallback for a response that omits the link.

  This cost several rounds of wrong theories (double-encoding, wrong polling
  endpoint) because the field that answers it sat behind a 512-character
  truncation in the one error message that quoted the document. The lesson is
  in `snippetN`: when a chain keeps failing in a shape you did not predict, log
  the *whole* upstream document, not its opening.

  The link is fetched **without** the bearer token (it carries its own
  signature) and is confined to `*.autodesk.com` over https (`autodeskHost`) —
  the server fetches it and streams the result to a browser, so an
  upstream-controlled address is not trusted merely because it arrived over TLS.
- **`data` arrives as an object OR an array**, depending on the endpoint: the
  create call returns an array, the reads return an object. `dataResources`
  normalizes both. Decoding straight into a struct is what broke first contact
  with live APS (*"cannot unmarshal array into Go struct field"*), so decode
  failures now quote the response body — a decoder that reports only what Go
  expected, never what arrived, cannot be debugged from a log.
- **F3Z vs F3D is APS's call, never ours.** `PickArchiveFormat` reads
  `downloadFormats` and prefers F3Z, falling back to F3D. Guessing from the file
  extension would break on the common case: a design with external references
  can *only* be produced as F3Z, and asking for F3D fails with an opaque 400. A
  version that offers neither is a specific, explainable answer
  (`archive_format_unavailable`), not a generic upstream error.
- **`downloadFormats` is fetched when the job starts, never on selection.**
  Per the repo-wide rule, an APS call must not fire per row or per click-around.

### The job runner

`server/archives.go` is `server/uploads.go` with the nouns changed —
deliberately, so the two job systems age together: an in-memory manager, a
`sem chan struct{}` concurrency gate, session-scoped visibility, first-writer-
wins `finish`, retention pruning on read, and the same
`POST /cancel` / `POST /dismiss` / whole-list-in-every-response wire shape.

Where it differs:

| | uploads | archives |
|---|---|---|
| concurrency | 3 | **2** — the work happens inside APS against a per-minute cost quota |
| job timeout | 4 h | **30 min** |
| retention | 1 h | **2 h** — a ready archive is something you come back to |
| poll | n/a | 2 s → 15 s backoff |
| progress | `bytesSent` | **none** — APS reports queued/processing/done, never a percentage |

The job holds the `*Session`, not a token: a 30-minute poll outlives an APS
access token, so it re-reads through `s.sessionToken` on every iteration.

**One live job per document per session.** A second click would spend the cost
quota generating a byte-identical archive, so it is refused with
`archive_already_running` and the button disables itself.

### No bytes on this server

A finished job stores APS's **download URL**, not the archive. Nothing lands on
disk, there is no retention policy to get wrong, and there is no new backup
source. `GET /api/archives/file?id=` re-reads that document on every request —
the CDN link inside it is signed and expires, so a stored one would break the
second download.

It **streams** rather than redirecting, for two reasons: the signed url is a
bearer credential for the object and no other download path in this app hands one
to a browser (`api.OpenFile` proxies for exactly the same reason), and the
`Content-Disposition` has to carry a real file name rather than whatever S3 calls
the object.

**The notification can outlive the archive, and the UI has to say so.** The
"archive ready" entry is persisted per user; the job holding the APS download
URL is in memory, so it is lost on a restart and pruned after two hours. The
bell therefore checks the polled job list before rendering the download link,
and shows a disabled "no longer available — generate it again" button when the
job is gone.

That check is not cosmetic. The link is an `<a download>`, which hands whatever
comes back to the browser's download manager — so without it, a server error
envelope is silently saved as `file.json` instead of being shown as a message.
Any endpoint reached by a download link has this property; it is the reason the
client, not the server, has to decide whether the link is live.

---

## Open / Insert

### Why there is a helper app at all

Fusion exposes a local MCP server at `http://127.0.0.1:27182/mcp`. A browser
cannot reach *the user's* loopback interface when the SPA is served from another
machine — and this server is explicitly built to be one (`-public-url`, LAN TLS,
per-hub branding). So the browser hands the OS a URL, and a small native program
makes the call.

**Except when it doesn't have to.** `POST /api/fusion/action` answers one of two
ways:

- **`mode: "proxy"`** — the request arrived from loopback, so browser, server and
  Fusion are all one machine. The server makes the MCP call itself, synchronously,
  and returns the outcome. Nothing to install, no ticket, no notification.
- **`mode: "launch"`** — everything else. The SPA navigates to a `fusionlocal://`
  URL and the helper takes over.

Getting that backwards would be a real bug, not a cosmetic one: proxying for a
remote browser would drive the **server operator's** Fusion on a stranger's
behalf. So `isLoopbackRequest` reads `RemoteAddr` and deliberately **ignores
`X-Forwarded-For`** — a forwarded header is a client assertion, and believing one
here would let any remote browser claim the fast path. A request carrying any
forwarding header is treated as remote.

### The security model

A URL scheme is a **public entry point**. Any web page in any browser can
navigate to `fusionlocal://…`, and the OS will launch the handler with whatever
that page chose. Two independent things stop that from being a way to drive
someone's Fusion:

**1. Tickets (`server/fusiontickets.go`).** The URL carries no document, no
project and nothing about the user — only a ticket id and the server origin. The
ticket is minted by the *authenticated, hub-locked* SPA, bound to that session,
expires in **two minutes**, and is redeemable **once**. A leaked URL is worth one
action, briefly, on a document its owner could already open. A fabricated one
redeems to nothing.

The two helper-facing endpoints are session-less by necessity (a native app has
no cookie) and safe because of it:

- `GET /api/fusion/ticket` — one answer for unknown, expired and already-redeemed
  alike, so probing ids learns nothing. Metered per IP like the auth routes.
- `POST /api/fusion/callback` — can only report on a ticket that was already
  redeemed, exactly once, and the code must be one this build defines
  (`fusionlink.ValidCode`); anything else is flattened to the generic failure.
  So it cannot be used to write arbitrary outcomes or to spam an inbox.

**2. Pairing (`cmd/fls-helper/pairing.go`).** The helper refuses any server the
user has not explicitly paired with, from a terminal, on that machine. Nothing in
a browser can add a pairing and nothing in a launch URL can bypass one.

Pairing also solves a practical problem: this server serves HTTPS from a
**self-signed certificate** by default, which no trust store accepts. Rather than
disabling verification — which would make the pairing meaningless, since anyone
on the network could then impersonate the server — `fls-helper pair` records the
certificate's SHA-256 at the moment the user vouches for it, and later
connections must present **that exact certificate**. `InsecureSkipVerify` is set
but verification is *replaced*, not skipped: the pin is stricter than the default
check. A server whose certificate the system already trusts is not pinned at all,
because normal verification survives renewal and a pin does not.

### The scheme is `fusionlocal://`, not `fls://`

`fls:` is already this app's internal card-token namespace (`fls:doc`, `fls:task`,
…), and those tokens appear **inside chat and wiki bodies**. Registering `fls:`
with the OS would mean a stray token in a message could launch a native app.

### The contract

`internal/fusionlink` holds the scheme, the URL build/parse, and the outcome
codes — one definition, imported by both the server and the helper, so the two
cannot drift. `internal/fusionact` sits above it and maps an MCP error to an
outcome code, so a failure is explained identically whether it went through the
proxy path or the helper.

`internal/fusionmcp` is **vendored from the sibling
[FusionDataCLI](https://github.com/schneik80/FusionDataCLI) repo** (`fusion/mcp.go`),
where it was written and proven against a live Fusion: the JSON-RPC handshake
with stateless fallback, SSE-or-JSON response handling, session-id caching with
one re-handshake on 404/410, `OpenDocument`, `InsertDocument`, `ActiveHubProjects`
and `NormalizeProjectID`. Its tests came with it. Two things were added here:
`ErrUnreachable` and `ErrNoActiveDesign`, so callers can name those two failures
instead of showing a dial error or a Python traceback.

### What the helper does

1. Parse the URL; refuse anything that isn't a known scheme/version/action.
2. **Refuse an unpaired server** — the one refusal with no server to report to,
   since by definition we don't trust it enough to call it.
3. Redeem the ticket over the pinned connection.
4. `ActiveHubProjects` — this is both the *is Fusion running* probe and the
   *same hub* check (`NormalizeProjectID` of the DM project id against Fusion's
   active-hub project list, exactly as FusionDataCLI does). Wrong hub is worth
   detecting because the failure it prevents is quiet rather than loud: Fusion
   would just say it cannot find the file.
5. Perform the action.
6. `POST /api/fusion/callback` with the outcome.
7. On failure, **also** show a native message — there is no browser tab it can
   write into, so a silent failure would look like nothing happened at all.

Subcommands: `pair` / `unpair`, `register` / `unregister`, `status`.
`status` reports all three preconditions (scheme registered, servers paired,
Fusion reachable) because a user who clicked a button and saw nothing needs to
find out which one is missing without guessing.

Install, usage, troubleshooting and the security model are written up for users
in **[HELPER.md](HELPER.md)**.

Scheme registration is **per-user, never elevated**:

| | how |
|---|---|
| macOS | a minimal `.app` in `~/Applications` with `CFBundleURLTypes`, then `lsregister -f`. Its executable is a two-line `sh` launcher rather than a copy of the binary, so an upgraded helper doesn't keep running the old code until someone re-registers. |
| Windows | `HKCU\Software\Classes\fusionlocal` with `URL Protocol` and a quoted `shell\open\command`. |
| Linux | `~/.local/share/applications/fls-helper.desktop` with `MimeType=x-scheme-handler/fusionlocal;`, then `update-desktop-database` and `xdg-mime default` (both best-effort). |

### Shipping it

The helper releases **independently of the server**, on `helper/vX.Y.Z` tags
(`.github/workflows/helper-release.yml`). That workflow is `make helper` plus
checksums — deliberately not GoReleaser, which the server uses: GoReleaser OSS
cannot derive a version from a prefixed tag (monorepo prefixes are Pro), and the
helper needs nothing it provides. `dist/` stays git-ignored; the binaries are
release artifacts, not repository content.

The two can drift because the only thing they share on the wire is the
`fusionlocal://` URL, which carries a version segment. An older helper meeting a
newer server is told the version is unsupported rather than misreading the
request — so a helper release is only *required* when that segment changes.

The Windows binaries are linked `-H windowsgui`: the OS starts this program on
every Open and Insert, and a console-subsystem binary would flash a black window
each time. `attachConsole` reattaches to the launching terminal so the CLI
commands still print. A shell does not *wait* for a GUI-subsystem process, so
setup-command output arrives after the prompt returns — inherent to that
trade-off, and the reason the python/pythonw two-binary convention exists.

### The gap a URL scheme cannot close

A scheme navigation tells the page **nothing** — not whether a handler is
registered, not whether it ran. So `web/src/state/fusionActions.tsx` polls
`GET /api/fusion/outcome?ticket=` for 25 seconds after launching. Three results,
three different answers:

| | shown as |
|---|---|
| helper reports success | nothing — Fusion coming to the front *is* the confirmation |
| helper reports failure | a dialog with the specific reason, localized from the outcome code |
| nothing ever arrives | "no helper app responded", with the two commands to fix it |

The 25 s timeout has to comfortably exceed a user reading and accepting the
browser's *"open this application?"* prompt, or a slow-but-working launch would
be reported as a missing helper.

There is a `fusion_failed` notification but **no success twin**: a bell badge per
click would be noise, and the failure entry exists only so the answer isn't lost
when the user navigated away mid-launch.

---

## Wire surface

| Route | Auth |
|---|---|
| `POST/GET /api/archives`, `POST /api/archives/{cancel,dismiss}`, `GET /api/archives/file?id=` | `protHub` |
| `POST /api/fusion/action`, `GET /api/fusion/outcome?ticket=` | `protHub` |
| `GET /api/fusion/ticket?ticket=`, `POST /api/fusion/callback` | **none** — ticket-authorized, per-IP metered |

## Error codes

`fusion_not_running`, `fusion_wrong_hub`, `fusion_no_active_design`,
`fusion_failed`, `archive_format_unavailable`, `archive_expired`,
`archive_not_ready`, `archive_already_running` — all in the six `errors`
catalogs. The helper carries its own **English** copies (`explain()` in
`cmd/fls-helper/launch.go`): it runs outside the browser with no access to the
SPA's locale, and its message is the fallback for when nobody is watching the tab.

## Known gaps

- **Code signing.** The macOS and Windows binaries are unsigned, so first run
  is quarantined / SmartScreen-flagged. This is the thing most likely to block
  adoption in practice, and it is not addressed here.
- **The helper is untested on macOS.** Scheme registration (the `.app` bundle
  and `lsregister`) and the `osascript` dialog have only ever run on Linux.
  **Windows is verified end to end** — registry registration, pairing with
  certificate pinning, Open, Insert, every failure path, the unpaired-server
  refusal, and both of the things that could only be reasoned about before:
  `-H windowsgui` plus `AttachConsole` does let the CLI print, and a protocol
  launch flashes no console window.
- **The Fusion palette already knows Fusion is running.** Inside `embed.html`
  the add-in bridge (`web/src/embed/bridge.ts`) is a strictly better route than
  the helper — it is in-process. Out of scope here; the obvious follow-up.
- **Archive is tip-version only.** The History tab pins older versions, but the
  action always archives the tip.
- **No archive progress.** APS reports queued/processing/done and nothing finer,
  so the UI shows an indeterminate state rather than inventing a percentage.
