# Hub isolation — status & architecture

Hubs separate clients, and hub boundaries are **IP boundaries**. Everything
this server stores locally is therefore physically partitioned by hub, the
browser session is locked to one hub at a time, and no code path — API,
backup, restore, admin tooling — can cross the partition. Shipped in four
commits on `admin` (H1 `a116065`, H2 `ffab132`, H3 `845dfdd`, H4 `7fb9a47`).

## The invariants

1. **Handlers resolve stores from the session's locked hub — never from a
   client-supplied hubId.** Where a request carries `hubId` (query, body,
   or multipart), it must equal the session hub or the request fails 403
   `hub_mismatch`. Data routes without a locked hub fail 409
   `hub_not_selected`. Enforcement is the `requireHub` middleware
   (`server/hubgate.go`) wrapping every data route; only `GET /api/hubs`
   and `POST /api/session/hub` sit outside it (they exist to pick the hub).
2. **Physical layout** under `config.Dir()`:
   ```
   hubs/<hubslug>/
     hub.json          # {hubId, hubName, createdAt} — identity marker
     backup.json       # per-hub backup config (dir, HH:MM, enabled)
     chat/ tasks/ production/ whiteboards/   # that hub's store roots
     pins-<hubslug>.json
   hubs/_unassigned/   # migration quarantine (see below)
   sessions.enc, session.key, config.json, server.json, tls-*.pem,
   server.log          # global: auth/TLS/ops — never hub data
   ```
3. **Slug collisions hard-refuse.** The slug (`internal/hubslug`, mirrored
   byte-for-byte in `web/src/state/hubKeys.ts`) is lossy; `hub.json` stores
   the raw hub id, and a mismatch refuses rather than silently merging two
   hubs' data.
4. **Backups are per hub end-to-end**: each hub has its own destination and
   schedule (`backup.json`), its engine roots at
   `<thatHubsBackupDir>/<hubslug>/`, manifests (v2) carry `hub` + `hubSlug`,
   and restore refuses foreign-hub manifests, pre-isolation (v1) manifests,
   and any path escaping the hub profile. Tests prove a hub A snapshot
   contains zero hub B bytes.

## Server shape

- `server/hubstores.go` — one lazily-built store-set per hub (`chat` +
  per-hub SSE `chat.Hub`, `tasks`, `production`, `whiteboards`), map keyed
  by slug; the map mutex is never held across store calls. `requireHub`
  resolves the set once per request into context, so a concurrent hub
  switch can't split a request.
- `POST /api/session/hub` validates APS hub membership with the session's
  token, then locks (or re-locks — that's the switch) the session.
  `Session.SelectedHub` persists across restarts (sessions.enc, additive).
- `/api/tasks/mine`, `/api/production/mine`, admin disk/delete/cleanup, and
  the backup endpoints all operate on the session hub's profile only.

## Migration (`internal/hubmigrate`, runs once at startup)

Relocates a pre-isolation layout into hub profiles: one atomic rename per
project dir (EXDEV → copy then remove-source-last), hub identity read from
each envelope's self-describing `hubId`. Chat's envelope predates the field,
so chat dirs resolve through a sibling map that includes the
already-migrated tree — a crash between phases still routes chat correctly
on rerun. Unresolvable or corrupt dirs land in `hubs/_unassigned/` with
bytes preserved; quarantined chat adopts into the session's hub on its
first roster-authorized access. Legacy global backup settings fan out into
every profile's `backup.json` before retiring from `server.json`. The
`.migrated` marker is an optimization — every pass is rerunnable.

## Frontend

Login lands in the remembered hub (persisted session, else `fls.lastHub`
auto-relock); the full-screen `HubGate` appears only on first run or when
the remembered hub was revoked. Switching lives in **Settings →
Connection** (port-change posture: warning → Apply → session re-lock →
full teardown + reload, no re-OAuth). Theme mode and custom colors are
stored per hub (deliberate anti-mixup cue: each client hub can look
different). Document cards referencing another hub render inert and muted
without fetching.

## Residual risks (documented, accepted)

- **Stale session lock after APS revocation**: a user whose hub membership
  is revoked keeps serving that hub's *local* data until their session
  ends (APS-proxied routes fail naturally; the gate and every re-lock
  re-validate membership). Bounded to data they once legitimately
  accessed. A TTL re-check in `requireHub` is a possible future hardening.
- **`_unassigned` quarantine** is not backed up by default (no
  `backup.json`) and adoption exists only for chat — tasks/production/
  whiteboards only quarantine on corrupt/unreadable envelopes, which
  should not occur for healthy data.
- **In-flight request during a switch**: a request that resolved its
  store-set immediately before a hub re-lock completes against the old
  hub (its own hub — never a third party's). The full-reload switch makes
  the window practically empty.

## Rules for new code

- Every new feature that persists or lists local data goes through the
  session's store-set (`storesFromCtx`) — never `config.Dir()` directly.
- New wire fields carrying a hub id must be validated against the session
  hub in the handler.
- New backup sources must be added per-hub in `backupEngineFor`, never as
  global globs.
- The cross-hub attack matrix lives in `server/hub_isolation_test.go` —
  extend it for any new store or endpoint family.
