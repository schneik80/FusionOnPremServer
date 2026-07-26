# Debugging & Reporting Bugs

This page is for users who hit a problem with fusionlocalserver and want to report it. It explains how to capture enough diagnostic information that the maintainers can reproduce and fix the issue without a long back-and-forth.

fusionlocalserver is a single HTTP server (a JSON API plus an embedded React/MUI web UI). Everything below is about its logging and how to file a useful defect.

If you're a contributor looking for the test-suite architecture, see [`testing.md`](testing.md). If you want internals on the GraphQL client and its retry behaviour, see [`api.md`](api.md).

---

## Where logs go

The server uses a single `log/slog` logger that writes to **two** places at once:

| Sink | Contents |
|---|---|
| The **console** (stdout) | The same lines you see in the terminal that launched the server. |
| `~/.config/fusionlocalserver/server.log` | A persistent copy of every log line. Mode `0600`, rotated by lumberjack: capped at 10 MB with up to 3 compressed `.gz` siblings kept alongside it (see `server/logging.go`). |

On Linux and macOS the path is exactly `~/.config/fusionlocalserver/`. On Windows it's `%USERPROFILE%\.config\fusionlocalserver\` (the same shape — the app uses `os.UserHomeDir()` directly rather than the OS-specific config dir).

The log file is local to your machine; nothing is sent anywhere automatically. You decide what (if anything) to share when you file a defect. The same rotated logs are also viewable in-app via **Settings → Logs**.

---

## Log levels

The default level is **info** — essential lines only: startup URLs, warnings, errors, and auth events (login complete, session expired, callback errors). That keeps day-to-day output quiet.

Run with `-v` to raise the level to **debug**. This applies to both sinks (console and `server.log`) and adds:

- **One line per HTTP request** — method, path, query, status, byte count, duration, and remote IP.
- **GraphQL request/response traces** from the `api` package — the full request body and response for each upstream APS call, plus retry decisions.

```sh
fusionlocalserver -v
```

You can tail the file from another terminal while reproducing:

```sh
tail -f ~/.config/fusionlocalserver/server.log
```

---

## What's safe to share

The logger never records secrets:

- **Access tokens and `Authorization` headers are never logged.** No bearer token appears in any log line, at any level.
- **`signedUrl` values are redacted** from GraphQL traces (replaced with `[redacted]`). A signed URL is itself a bearer credential for the derivative it points at, so its value never reaches the log even under `-v`.
- **GraphQL response bodies** are logged verbatim under `-v`. They contain item names, project names, hub names, file sizes, and other metadata. If your project names themselves are confidential, redact those before sharing.

Sessions (including refresh tokens) are persisted **encrypted** to `~/.config/fusionlocalserver/sessions.enc` (AES-256-GCM; key in `session.key`, mode 0600) — see `server/session_persist.go`. Neither file is a log; never attach `sessions.enc` or `session.key` to a bug report.

---

## When a request fails or the server misbehaves

A panic in any request handler is **recovered**: the server logs the panic message and a full stack trace at error level, returns a JSON `500 internal server error` to the browser, and keeps running. So a single bad request can't take the process down — but the stack in `server.log` is exactly what we need to fix it.

If a data request returns an unexpected error in the UI, re-run with `-v`, reproduce, and the failing request and its GraphQL trace will be in `server.log`.

---

## Filing a bug — checklist

When you open an issue at <https://github.com/schneik80/fusionlocalserver/issues>, please include:

1. **Version** — fetch `GET /api/meta` from the running server (e.g. `curl http://localhost:8080/api/meta`); the `version` field is what we need. It's also logged at startup and shown in the web UI's About dialog.
2. **OS and browser** — e.g. "macOS 14.5, Safari 17" or "Linux Fedora 40, Chrome 124".
3. **What you did** — three to five lines is plenty. "Signed in, picked the *IMA* hub, selected the *RC* project, drilled into *Designs*, opened the Uses tab on a 200-component assembly."
4. **What you expected vs. what happened** (and the failing request/URL if you can see it — the browser devtools Network tab shows the `/api/...` call that errored).
5. **The relevant slice of `server.log`**, ideally captured with `-v` so it includes the per-request lines and GraphQL traces around the failure. The last few hundred lines around the failure are usually enough.
6. **The recovered-panic block** in full if the server returned a 500 — the `panic recovered` log line carries the stack trace.

If the issue is a slow or flaky API call, we'll usually want to see at least one full GraphQL request/response pair plus any retry lines that fired.

A good bug-report skeleton:

```
Version: v0.1.0   (from GET /api/meta)
OS: macOS 14.5 (Apple Silicon)
Browser: Safari 17

Steps:
1. Started: fusionlocalserver -v
2. Signed in, selected hub "IMA"
3. Selected project "RC"
4. Drilled into "Mechanical Drawings"
5. Selected design "2021-02"
6. Opened the Drawings tab

Expected: list of drawings made from the design.
Actual: tab shows "Error: Query point value 23066 exceeds maximum...".
Failing request: GET /api/items/drawings?...

server.log excerpt (-v): <attached>
```

---

## Common log signatures and what they mean

Under `-v` these are the GraphQL-trace patterns that show up most often. Recognising them helps you tell whether you've hit something we already know about.

### Retry lines (`attempt=N`, `delay=…`)

The retry layer kicked in. The client wraps each GraphQL call in a 3-attempt retry loop for transient APS gateway flakiness — see [`api.md`](api.md#error-handling-and-retry). Seeing one or two retries is fine; seeing the loop bottom out with `APS GraphQL flaky after 3 attempts: …` after every action is a real bug worth filing.

### `code:NOT_FOUND, errorType:UNKNOWN, service:cw`

The APS Manufacturing Data Model gateway returns this intermittently for hub URNs it just successfully enumerated. It's an upstream bug, not yours. The retry layer absorbs it for root-level paths automatically.

### `Query point value <N> exceeds maximum allowed query point value 1000`

The GraphQL gateway rejected the request as too expensive. If this comes from a query that *we* sent, that's a real bug — please file it with the request line so we can shrink the query. The drawings tab in particular has tight limits because of this cap; see [`api.md`](api.md#getdrawingsfordesign--drawings-tab-on-a-designitem) for the trade-off.

### `unauthorized (HTTP 401)` / 401 from a data endpoint

The session's access token is stale, was revoked, or lacks the `data:read` / `user-profile:read` scope. The web UI turns a 401 from the API into a prompt to sign in again. If a fresh sign-in still produces 401, the APS app registration may be misconfigured — see [`authentication.md`](authentication.md).

### Hub-isolation errors: `409 hub_not_selected` / `403 hub_mismatch`

All local data is partitioned per hub, and every data route requires the session to be locked to one hub. A **409 with code `hub_not_selected`** means the session lost its hub lock (e.g. a server restart before re-selecting, or an expired lock) — the SPA reacts by returning to the hub gate, exactly like a 401 returns you to login. A **403 with code `hub_mismatch`** means a request carried a `hubId` different from the session's locked hub; the request is refused by design. Neither is a bug in itself — but if the SPA loops on them, that's worth filing.

### `GraphQL partial errors (kept data): …`

A response came back with both useful data and field-level errors. The client kept the data and surfaced the errors via this log line. Typically harmless — common when one row in a list references a deactivated or deleted record. If a tab unexpectedly shows missing fields, the corresponding `GraphQL partial errors` line identifies which row.

### `ClassifyAssembly` query bursts after a folder load

When a folder's contents load, the app dispatches up to 50 small `componentVersion(...).occurrences(pagination: { limit: 1 })` queries in parallel, capped at 8 concurrent in flight by a semaphore. Under `-v` this shows up as a burst of similar requests right after the `itemsByFolder` / `itemsByProject` response. Each one returns a single-result list (or empty) and refines a row's icon to assembly or part. If those queries 401 or get rate-limited, the corresponding rows silently keep their generic design icon — failures here never block navigation. See [`api.md`](api.md#classifyassembly--asyncpartassembly-subtype) for the query and the concurrency cap.

---

## Privacy and security

- `server.log` is written with mode `0600` (owner-only read/write).
- Per-user sessions are held in server memory and persisted only in encrypted form (`sessions.enc`, AES-256-GCM, key in `session.key` mode 0600); no plaintext token file exists on disk.
- The `Authorization` header is never logged, and `signedUrl` values are redacted from traces.
- Nothing is uploaded automatically. Logs stay on your machine until you choose to share them.

If you find a bug that *does* leak tokens or other sensitive data into a log, please report it privately rather than filing a public issue — see `SECURITY-TODO.md` and the security-fix history (PR #1) for how the project handles such reports.
