# Backups — status

A per-hub snapshot engine for the server's **local** data (the four project
stores, pins, and a redacted copy of the global config), with GFS retention,
integrity verification, and a guarded restore. Everything APS-side (designs,
wiki pages, uploads) is deliberately out of scope — that data lives in Fusion
Team.

Backups are **fully per-hub** (hubs are IP boundaries — see
[hubs](../hubs/STATUS.md)): each hub has its own configuration
(`hubs/<hubslug>/backup.json` — destination, daily time, enabled), its engine
snapshots only that hub's stores, and its snapshot tree roots at
`<thatHub'sBackupDir>/<hubslug>/` so two hubs configured to one location can
never interleave. Sessions only ever see and act on their own hub's tree.

## Snapshot layout & manifest

```
<backupDir>/<hubslug>/
  daily/20260726-033000/        UTC second-resolution timestamps —
  weekly/…                      lexicographic order == chronological
  monthly/…
  manual/…                      user-triggered; never pruned
  pre-restore/…                 taken automatically before a restore; never pruned
```

Each snapshot dir contains the backed-up files (their store-relative paths)
plus `manifest.json`:

- **`ManifestVersion: 2`** — v2 added the **hub identity** fields `hub` (raw
  hub id) and `hubSlug` (profile dir slug). Restore refuses v1 manifests
  outright: a pre-hub-isolation snapshot cannot prove which hub it belongs to.
- `appVersion`, `createdAt`, `kind`, and per-file entries: path, source store
  name, **SHA-256**, size, and the file's **schema version** (the store
  envelope version, 0 when the file has none) — captured so restore can
  refuse data written by a newer build.

A failed source aborts and **removes the whole snapshot dir** — a partial
snapshot listing as a valid backup would be worse than none.

## What is backed up — and what never is

Sources are **allow-list only** (`backup/sources.go`): the engine backs up
exactly what a Source hands it.

| Source | Files |
|---|---|
| `chat` / `tasks` / `production` / `whiteboards` | Each store's `Snapshot` method streams its per-project files under the project mutex (never a mid-mutation file). |
| `pins` | `pins-*.json` glob at the hub profile root (`.bak`/`.tmp` siblings don't match). |
| `config` | `config.json` — **parsed, `client_secret` blanked, re-marshaled**; the raw bytes are never copied, so the secret cannot leak (unparseable config is skipped for the same reason). Plus `server.json` as-is. |

`sessions.enc`, `session.key`, TLS keys and logs are unreachable **by
construction, not by filtering** — no Source reads them.

## GFS rotation & scheduler

`backup/gfs.go`: **7 daily / 4 weekly / 12 monthly** (`backup.GFS`). Each
daily run promotes itself to `weekly/` when the ISO week has no entry yet and
to `monthly/` when the calendar month has none — promotion is a full
directory **copy** (not hardlinks), so pruning a daily can never hollow out
the weekly born from it. Pruning keeps the newest N per managed tier and only
touches dirs whose names parse as snapshot timestamps; `manual/` and
`pre-restore/` are never pruned.

The scheduler (`server/handlers_backup.go: runBackupScheduler`) is one loop
over **every enabled hub profile on disk** (a hub can have backups configured
without being opened this process run): it computes the minimum next-fire
time across all per-hub `HH:MM` local schedules (`backup.NextRun`, default
`03:30`), sleeps until it, runs every hub whose slot arrived (per-hub
failures log and skip, never stopping other hubs), and a config change pokes
it awake to recompute. **There is deliberately NO missed-window catch-up
(settled decision):** a server that was down at 03:30 backs up at the next
03:30, not at startup.

## Verify

`backup/verify.go` re-checks a snapshot against its manifest. Per file:
present, **re-hashes** to the recorded SHA-256, **parses** (`.json` must be
one valid JSON document, `.jsonl` valid JSON per line), and carries a schema
**version** no newer than what this build writes
(`server.expectedSchemaVersion` maps store+path → current version; whiteboard
`doc-*.json` and config files have no schema authority and are exempt). Files
on disk that the manifest doesn't list fail the snapshot too. The engine
method additionally surfaces a report-level **warning** for snapshots that
verify byte-clean but that restore would refuse: pre-v2 manifests, or ones
stamped for a different hub.

## Restore

`backup/restore.go`, deliberately conservative — nothing is written until
everything is proven:

1. **Validate**: manifest loads; `CheckRestorable` (v2+ and stamped for THIS
   engine's hub — slug must match, and raw ids too when both present, which
   catches two hubs whose lossy slugs collide); no file's schema version
   newer than this build; snapshot's app version not newer than the running
   one ("dev"/unparseable always pass); **every manifest path must resolve
   inside its root** (`safeRel` + `containedJoin` — a crafted manifest cannot
   direct a single byte outside `hubs/<slug>/`, config.json aside).
2. **Read + re-hash every file into memory first** — a restore never starts
   writing from a snapshot it can't fully read intact.
3. **Pre-restore safety snapshot** of the current data (`pre-restore/` tier);
   if it fails, nothing has been touched.
4. Write everything via atomic rename.

Special files: **`config.json` is merged** — every field restores except
`client_secret`, which keeps the live value (the backup's copy was blanked at
capture). **`server.json` is never restored** — it holds live operational
state (listen port, and historically backup config); restoring an old copy
could flip the port out from under the connected client.

The HTTP handler then **`Reset()`s the session hub's four store caches**
(otherwise stale in-memory state would rewrite pre-restore data) and restarts
the listener via the port-change rebind flow (reply
`{restarting:true}`, rebind ~0.5 s later; the SPA shows a reconnect screen).
Restore requires a **typed confirmation** equal to the snapshot's timestamp
dir name — enforced server-side, not just in the UI.

## API

All under the standard authenticated-session + hub-locked gating
(see [admin](../admin/STATUS.md)); `path` is backup-dir-relative
(e.g. `manual/20260726-033000`) and is containment-checked against the
session hub's own subtree.

```
GET  /api/admin/backups            config + all snapshots, newest first
POST /api/admin/backups/run        manual snapshot now
GET  /api/admin/backups/config     the session hub's backup.json
POST /api/admin/backups/config     {backupDir,backupTime,backupEnabled} — validates
                                   HH:MM, absolute path, creatable + write-probed;
                                   then re-schedules
POST /api/admin/backups/verify     {path} → per-file hash/parse/version report
POST /api/admin/backups/restore    {path,confirm} → {restarting:true}
GET  /api/admin/fs/dirs            ?path — dirs-only browse for the folder picker
```

**UI**: the Settings console's **BackupsTool**
(`web/src/components/settings/BackupsTool.tsx`) — schedule config with the
`DirPickerDialog` server-side folder browser (directories only, hidden dirs
excluded), manual run, snapshot table with per-row verify (expandable
per-file results) and restore (typed confirmation, reconnect flow).

## Residual risks / known gaps

- **The backup destination is trusted storage.** Snapshots are plain files,
  not encrypted or signed; the manifest hashes detect corruption, not
  tampering by someone with write access to the backup dir.
- Restore replaces store files wholesale; there is no per-project or
  per-store partial restore.
- The scheduler resolution is per-process: with the server stopped at the
  scheduled time nothing runs later (the settled no-catch-up decision — a
  long-running server is the assumed posture).
- `server.json` never restoring means a restore does not bring back a
  historical port or backup schedule — by design, but worth knowing.
- Snapshots hold only local app data: designs, wiki pages and uploads live in
  Fusion Team and are outside this system entirely.

## Verifying

```
go test ./backup/... ./server/...    # engine, GFS promote/prune, hub identity,
                                     # sources redaction, verify, restore refusals
```

End-to-end: configure a folder + enable in Settings → Backups → run a manual
backup → check `<dir>/<hubslug>/manual/<ts>/manifest.json` (and that
`config.json` in it has a blank `client_secret`) → verify the snapshot →
corrupt a byte in a copy and verify again (hash failure) → restore with the
typed confirmation and confirm the reconnect, the restored data, and the new
`pre-restore/` snapshot.
