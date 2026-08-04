# Production server runbook

How to stand up, operate, and roll back the small-team deployment: a single
Ubuntu VM with full root, public HTTPS behind Caddy, sign-in restricted by the
`admin_users` whitelist, and automatic deploys from GitHub releases over SSH.
The companion plan (and its living status) is [`PLAN.md`](PLAN.md); the
artifacts referenced here live in [`deploy/`](../../deploy/).

The app is a single process with a single data directory
(`~/.config/fusionlocalserver` of the service user) — there is no multi-
instance mode, no external database, and restarts do **not** log the team out
(sessions persist encrypted on disk).

## 1. Provision the VM

Any always-on Ubuntu 24.04 VM with **full root** works. Reference choice:
**DreamHost Self-Managed VPS "Stack 4"** (2 vCPU / 4 GB / 75 GB NVMe) — 4 GB
comfortably clears the app's ~1 GB worst-case memory (32 live whiteboard
rooms). DreamHost's *managed* VPS and shared plans do **not** work (no
root/sudo → no systemd service, no Caddy). Alternatives: DreamCompute 2 GB,
Hetzner CX22, any equivalent.

Pick the VM location to match `APS_REGION` / where the team sits.

## 2. Bootstrap

On your workstation, mint the deploy keypair GitHub Actions will use:

```
ssh-keygen -t ed25519 -f deploy_key -N "" -C deploy@github-actions
```

Copy the repo's `deploy/` directory to the VM and run, as root:

```
sudo ./bootstrap.sh "$(cat deploy_key.pub)"
```

Idempotent; it creates the `fls` service user (owns the data, no login shell)
and the `deploy` user (owns `/opt/fusionlocalserver/bin`, target of the
release SSH, sudo limited to exactly `systemctl restart
fusionlocalserver.service`), installs the systemd unit + env template +
sudoers drop-in, hardens sshd (no passwords, no root password login), enables
**ufw** (deny all inbound except 22/80/443 — load-bearing: the binary binds
`0.0.0.0:8080` unconditionally, and this firewall is what keeps that port off
the internet), and installs Caddy.

## 3. Configure

1. **`/etc/fusionlocalserver/env`** (root-only; systemd reads it before
   dropping privileges): APS client id + secret, `APS_REGION`, `PUBLIC_URL`,
   and — **required before DNS goes live** — a non-empty `FLS_ADMIN_USERS`.
   Without the whitelist any Autodesk account can sign in, and today every
   session has admin powers (see
   [`authentication.md`](../authentication.md)).
2. **Caddy**: merge `/etc/caddy/Caddyfile.fls-example` into
   `/etc/caddy/Caddyfile` with the real hostname; `systemctl reload caddy`.
3. **DNS**: A record for the subdomain (e.g. `fls.example.com`) → the VM's IP.
   If the main domain's DNS is at DreamHost, this is one panel entry.
4. **APS app**: register `https://<host>/api/auth/callback` as a callback URL
   — exact string match, no wildcards.
5. **First binary install** (releases deploy automatically afterwards): as
   printed by bootstrap — download the current release's
   `fusionlocalserver-<version>-linux-amd64.tar.gz`, install the binary to
   `/opt/fusionlocalserver/bin/fusionlocalserver` owned `deploy:deploy`, then
   `systemctl start fusionlocalserver`.
6. **GitHub repo configuration**:
   - secret `DEPLOY_SSH_KEY` — contents of `deploy_key` (the private half);
   - variable `DEPLOY_HOST` — the hostname;
   - variable `DEPLOY_KNOWN_HOSTS` — output of `ssh-keyscan <host>` (pins the
     server's identity; the workflow uses `StrictHostKeyChecking=yes`).

## 4. Verify

- `curl https://<host>/api/meta` — 200 with the installed version (proves
  DNS + Caddy + certificate + app).
- Sign in from a whitelisted account — full OAuth round-trip (proves the APS
  callback registration and `PUBLIC_URL`).
- Sign in from a non-whitelisted account — login screen shows "not authorized
  on this server".
- Open a chat channel, idle >60 s — the SSE stream stays connected (proves no
  proxy timeout).
- `ssh deploy@<host>` with the deploy key; `sudo systemctl restart
  fusionlocalserver.service` (proves the deploy path end to end); reload the
  browser — still signed in (sessions persist across restarts).
- From outside: `curl http://<ip>:8080` must be refused (ufw).
- Pipeline: push a throwaway prerelease tag (e.g. `v0.9.9-test.1`) — the
  release runs, the `deploy` job is **skipped**. Then run the Deploy workflow
  by hand with that tag — full chain through the version-asserting smoke
  test. Finally dispatch the previous stable tag (rollback rehearsal) and
  delete the test release + tag.

## 5. How deploys work

Tag `v*` → GoReleaser publishes the release → the `deploy` job (stable tags
only; prerelease tags contain `-`) calls
[`deploy.yml`](../../.github/workflows/deploy.yml), which downloads the
`linux-amd64` archive from the release, verifies it against `checksums.txt`,
scp's it to the server, hardlinks the running binary to
`fusionlocalserver.prev`, atomically `mv`'s the new one into place, restarts
the service, and polls `GET /api/meta` until it reports the deployed version
(≈30 s budget). Deploys serialize via a concurrency group; a failed deploy
leaves `.prev` on disk and reports loudly. Expect a few seconds of downtime
per deploy; nobody is logged out.

## 6. Rollback

> **Read this first.** Local stores use **forward-only schema migrations**
> with a future-version guard: if release N migrated a store's schema,
> running release N−1 against that data makes the store **refuse to load**
> (503s on those features) until the newer binary returns. Sessions and APS
> data are unaffected. Check the release notes for schema changes before
> rolling back; restoring a pre-migration backup is the alternative.

- **Normal path**: GitHub → Actions → Deploy → "Run workflow" with the older
  tag. Same pipeline, same smoke test.
- **Emergency path** (GitHub unreachable): on the server, as `deploy`:
  ```
  cd /opt/fusionlocalserver/bin
  ln -f fusionlocalserver fusionlocalserver.bad
  ln -f fusionlocalserver.prev fusionlocalserver
  sudo systemctl restart fusionlocalserver.service
  ```

There is deliberately **no automatic rollback** on a failed smoke test — the
schema guard makes a blind rollback worse than a loud failure.

## 7. Operations notes

- **Whitelist changes**: edit `FLS_ADMIN_USERS` in `/etc/fusionlocalserver/env`
  (or `admin_users` in the `fls` user's
  `~/.config/fusionlocalserver/config.json`), then
  `systemctl restart fusionlocalserver`. Removal revokes existing sessions on
  their next request; sessions of removed users are also dropped at restart.
- **Port**: keep the app on 8080. Changing it in Settings breaks Caddy's
  `reverse_proxy 127.0.0.1:8080` target until the Caddyfile is updated.
- **Monitoring**: `GET /api/meta` is the health probe (there is no dedicated
  health endpoint). `sudo journalctl -u fusionlocalserver` for logs; the app
  also rotates its own `server.log` in the data dir.
- **Backups**: configure per-hub GFS backups in Settings → Backups to a
  directory outside the data dir (e.g. `/var/backups/fusionlocalserver`), and
  offsite that directory (rclone/restic cron) — a VM-provider snapshot is not
  guaranteed on every product. `sessions.enc`/`session.key`/TLS keys are
  excluded from app backups by construction; `config.json` is captured with
  the client secret blanked.
- **OS updates**: `unattended-upgrades` is on by default in Ubuntu for
  security patches; reboots restart the service via systemd (`enable`d) and
  Caddy re-serves automatically.
