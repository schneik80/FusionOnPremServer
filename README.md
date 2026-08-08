# fusionlocalserver

[![test](https://github.com/schneik80/fusionlocalserver/actions/workflows/test.yml/badge.svg)](https://github.com/schneik80/fusionlocalserver/actions/workflows/test.yml)

A local server and web UI for [Autodesk Platform Services (APS)](https://aps.autodesk.com) and the Fusion Manufacturing Data Model. Run it on your LAN and give your team a browser-based workspace over your Fusion hubs — design browsing plus project tasks, wiki, chat, production tracking, and whiteboards — where **each user signs in with their own Autodesk account**.

One Go binary: an HTTP server exposing a JSON API and serving an embedded React/MUI single-page UI. The Go side stays close to the standard library (two small dependencies: `golang.org/x/sync` and `lumberjack` for log rotation); everything else — including all visualizations — is built in.

> **Beta.** This is the first beta release. The feature set below is complete and tested, but expect rough edges; see [Beta notes](#beta-notes).

## Quick start

```sh
echo "your-aps-client-id" > .aps-client-id   # one time; see "Building from source"
make run                                      # build the UI + binary, then serve over HTTPS
```

`make run` serves over **HTTPS** (`-tls` is on by default), binds `0.0.0.0:8080`, and logs every reachable URL at startup:

```
server starting  addr=0.0.0.0:8080 tls=true ...
reachable on the LAN  url=https://localhost:8080
reachable on the LAN  url=https://192.168.1.50:8080
```

New here? [**docs/getting-started.md**](docs/getting-started.md) walks the whole
thing end to end — install, sign-in, hubs, every feature, and setting up your
team.

The first time, `-tls` generates and caches a self-signed certificate under `~/.config/fusionlocalserver/` (browsers warn once — accept it); pass `-tls-cert`/`-tls-key` to supply your own PEM pair. Open one of those URLs in a browser and click **Sign in with Autodesk**. Each visitor authenticates with their own Autodesk account; the server holds their tokens in a per-session store keyed by an `HttpOnly` cookie, and proxies their data calls under their own identity. Tokens never reach the browser's JavaScript.

> ⚠️ **Don't disable TLS on a shared network.** Over plain HTTP the session cookie is not marked `Secure` (browsers drop `Secure` cookies over `http://`), so anyone able to sniff the wire could capture a cookie and hijack that user's session until it expires. `make run` keeps `-tls` on for this reason; only override it (`make run TLS=`) behind a TLS-terminating proxy or for loopback-only testing. A warning is logged when the server binds a non-loopback address over plain HTTP.

## What's inside

**Design browser** — a three-column **Projects │ Contents │ Details** browser over hubs → projects → folders → designs. The details panel shows metadata beside a server-cached thumbnail, with tabs for version **History** (drawn as a branch graph), physical **Properties**, **Uses / Where Used / Drawings** (all clickable, navigating straight to the referenced document), **BOM**, folder/project **Permissions**, and an isometric **Activity** heat map built from the design's GraphQL activity report.

**Project apps** — every project gets five workspace tabs beside the browser:

- **Tasks** — a Kanban board plus a hand-drawn SVG **Gantt schedule**: drag bars to move/resize, drag between bars to add dependencies, group tasks into stages whose derived bar aggregates and moves its children, with progress roll-up and milestones. A cross-project "my tasks" screen sits on the nav rail.
- **Wiki** — markdown pages with drafts, image upload, and publishing; pages live in the project's Fusion Team storage, not on this server.
- **Chat** — channels (public and private), threads, reactions, unread cursors, and typing indicators, live over SSE. Access is derived from each user's APS project role — there is no parallel permission system.
- **Production** — a light MES / product tracker: jobs as step graphs carrying version-pinned plan documents, with branching **decision** nodes whose colour-coded results route the flow; batches freeze the plan at run time so later edits can never rewrite what a run recorded.
- **Whiteboards** — tldraw boards with live cards for designs, tasks, jobs, and batches, including expanding an assembly into a card tree.

**Hub isolation** — hubs are treated as hard client boundaries. Each browser session locks to one hub; every byte of local data (tasks, chat, production, whiteboards, pins, backups, even theme colors) is partitioned per hub, and no API or admin path can cross the partition. Switching hubs is a deliberate, full-reload action in Settings → Connection. See [`docs/hubs/STATUS.md`](docs/hubs/STATUS.md).

**Admin console** — a Settings dialog with real operations tools: appearance (light/dark/system + per-hub custom colors), **language** (English, Deutsch, Français, Español, Italiano, Português — switchable live), connection (runtime port change, hub switch), uptime, a rotated-log viewer, per-hub **GFS backups** (7 daily / 4 weekly / 12 monthly, with verify and restore), and data management (disk usage, per-project deletion, stale-file cleanup).

**Durability** — every local store writes one JSON/JSONL file per project with atomic writes, schema-versioned envelopes stamped with creation/update provenance, automatic forward migration on load, and `.bak` recovery on corruption. Backups carry sha256 manifests and refuse to restore into the wrong hub.

### Flags & settings

| Flag | Default | Purpose |
|------|---------|---------|
| `-v` | off | Verbose logging: debug level to the console **and** the log file, including a line per request and (redacted) upstream API traces |
| `-dev` | off | Developer mode: reverse-proxy the web UI to the Vite dev server for HMR instead of serving the embedded build |
| `-tls` | off | Serve over HTTPS so the session cookie is `Secure`. With no cert given, a self-signed one is generated and cached under `~/.config/fusionlocalserver/` (browsers warn once); use `-tls-cert`/`-tls-key` to supply your own PEM pair. The OAuth callback then becomes `https://…/api/auth/callback`. |
| `-public-url` | derived | Canonical external base URL clients use, e.g. `https://fusion.lan:8080`. When set, the OAuth `redirect_uri` is built from it — so you register **one** callback on the APS app — and any client that arrives via a different host is redirected to it. Without it, the callback is derived from each client's address and every distinct origin must be registered separately. |

> **APS callback registration.** APS validates the OAuth `redirect_uri` by exact match (no wildcards). You don't register clients — you register the server's callback URL(s). The simplest setup is to pick **one** stable address everyone uses (a hostname or static IP), pass it as `-public-url`, and register just `<public-url>/api/auth/callback`. `localhost` ≠ `127.0.0.1`, each LAN IP/hostname is distinct, and `-tls` makes the scheme `https` — so a fixed `-public-url` is the way to keep it to a single registration.

The listen **port is configurable at runtime** from Settings → Connection (persisted to `~/.config/fusionlocalserver/server.json`). Changing it restarts the listener in place; the page then reconnects on the new port. The port field is read-only in `-dev` mode (where the Vite proxy is pinned to the default port).

Sessions are kept in an encrypted file (`~/.config/fusionlocalserver/sessions.enc`, AES-256-GCM), so a server restart doesn't log anyone out. Each browser also remembers its last-used hub and relocks to it on the next login.

Logs go to the console and to `~/.config/fusionlocalserver/server.log`, rotated at 10 MB with three compressed generations kept. The default level is essential-only; `-v` adds per-request and upstream-trace detail. Logs are also viewable in-app under Settings → Logs.

### Non-US hubs

Set the region the server queries (applies to every user) via the APS region, e.g. `APS_REGION=EMEA` or `APS_REGION=AUS` (default is US). See [`docs/development.md`](docs/development.md) for configuration precedence.

## Install

**Homebrew (macOS / Linux)**
```sh
brew install schneik80/fusionlocalserver/fusionlocalserver
```

Or grab a binary from [Releases](https://github.com/schneik80/fusionlocalserver/releases):

```sh
# macOS arm64 / amd64, linux amd64 — pick your platform's asset
VERSION=$(curl -s https://api.github.com/repos/schneik80/fusionlocalserver/releases/latest | grep '"tag_name"' | cut -d'"' -f4 | tr -d v)
curl -L "https://github.com/schneik80/fusionlocalserver/releases/latest/download/fusionlocalserver-${VERSION}-darwin-arm64.tar.gz" | tar xz
sudo mv fusionlocalserver /usr/local/bin/
```

Released binaries ship the embedded web UI and a publisher client ID, so they need no build step or configuration.

## Building from source

Requires **Go 1.25+** and **Node/npm** (for the web UI).

```sh
git clone https://github.com/schneik80/fusionlocalserver
cd fusionlocalserver
```

Register a web app at [aps.autodesk.com/myapps](https://aps.autodesk.com/myapps) with scope `data:read user-profile:read`. **Register a Callback URL for every origin users will reach the server by** — APS allows no wildcards, so each `http(s)://host:port/api/auth/callback` is a separate exact-match entry. Since the server serves HTTPS by default, that is `https://localhost:8080/api/auth/callback` for local development (use `http://…` only if you run with `make run TLS=`); add each LAN address (and each configured port) you intend to use.

```sh
echo "your-client-id" > .aps-client-id    # git-ignored
echo "https://your-host:8080" > .aps-public-url   # optional, git-ignored: the URL your APS callback is registered under
make build                                # vite build → embed UI (-tags embed_ui) → go build
./fusionlocalserver -tls                  # serve over HTTPS, or just: make run
```

If `.aps-public-url` is present, `make build` bakes it in as the canonical base URL: the binary then builds the OAuth `redirect_uri` from it (so you register **one** callback) and redirects clients on other hosts to it — no `-public-url` flag needed. The flag still overrides the baked-in value.

The whiteboards feature uses [tldraw](https://tldraw.dev), which requires a licence key at build time: put `VITE_TLDRAW_LICENSE_KEY=…` in the git-ignored `web/.env.local` (see `web/.env.example`). Without a key the other features are unaffected.

| Target | What it does |
|--------|--------------|
| `make build` | Build the web UI, embed it (`-tags embed_ui`), and compile with the client ID baked in |
| `make run` | `make build` then serve over HTTPS on the LAN (`-tls` on by default; `make run TLS=` for plain HTTP, `ARGS="-v"` to add flags) |
| `make dev` | Go-only build, **no** embedded UI (serves a stub) and no embedded client ID — pair with `cd web && npm run dev` and `./fusionlocalserver -dev` for hot reload |
| `make check` | `go vet ./...` + `go test -race ./...` |

`server/webdist/` is entirely gitignored build output; a plain `go build` (no `embed_ui` tag) compiles against an in-memory stub, so the tree never needs a committed placeholder.

## Requirements

- An [Autodesk account](https://accounts.autodesk.com) with access to at least one Fusion Team hub (each user needs their own)
- macOS 12+, Linux, or Windows 10+
- The server's listen port (default `8080`) free on the host

## Beta notes

- The five non-English locales were machine-translation seeded and await native review ([`docs/i18n/STATUS.md`](docs/i18n/STATUS.md)).
- Whiteboards build against a tldraw evaluation licence that expires 2026-10-29; supply your own key for production use.
- The self-signed certificate produces a one-time browser warning per device; supply your own PEM pair to avoid it.
- Backups taken before the hub-isolation release (pre-v2 manifests) are readable but deliberately not restorable — take fresh per-hub backups.

## Documentation

| Doc | What it covers |
|---|---|
| [`docs/getting-started.md`](docs/getting-started.md) | **Start here** — install → sign in → hub → every feature, end to end |
| [`docs/web-ui.md`](docs/web-ui.md) | The full UI tour: sign-in, hub selection, the browser, project apps, settings |
| [`docs/architecture.md`](docs/architecture.md) | C4 diagrams, package layout, request/session flow, stores, resilience |
| [`docs/authentication.md`](docs/authentication.md) | Per-user OAuth (PKCE) login, sessions, cookies, token refresh |
| [`docs/hubs/STATUS.md`](docs/hubs/STATUS.md) | Hub isolation: the security model, per-hub layout, invariants |
| [`docs/tasks/STATUS.md`](docs/tasks/STATUS.md) | Tasks: Kanban, the Gantt schedule model, cross-project view |
| [`docs/wiki/STATUS.md`](docs/wiki/STATUS.md) | Wiki: drafts, publishing to Fusion Team, images |
| [`docs/chat/STATUS.md`](docs/chat/STATUS.md) | Chat: channels, threads, SSE, authorization |
| [`docs/production/STATUS.md`](docs/production/STATUS.md) | Production: jobs, steps, decisions, version pinning, batches |
| [`docs/whiteboards/STATUS.md`](docs/whiteboards/STATUS.md) | Whiteboards: tldraw integration, app cards, licence key |
| [`docs/backup/STATUS.md`](docs/backup/STATUS.md) | Backups: GFS rotation, manifests, verify, restore |
| [`docs/admin/STATUS.md`](docs/admin/STATUS.md) | The Settings admin console and data management |
| [`docs/i18n/STATUS.md`](docs/i18n/STATUS.md) | Localization: locales, conventions, adding strings |
| [`docs/api.md`](docs/api.md) | The APS GraphQL client: queries, retry behaviour, debug logging |
| [`docs/development.md`](docs/development.md) | Building from source, configuration, release pipeline, dependencies |
| [`docs/debugging.md`](docs/debugging.md) | Logging, `-v`, and **reporting a bug** — what to capture and how to file it |
| [`docs/testing.md`](docs/testing.md) | Test strategy and how to run / extend the suite |

## License

[GNU General Public License v3.0](LICENSE) — © Kevin Schneider
