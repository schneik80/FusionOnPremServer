# fls-helper — the Fusion helper app

`fls-helper` is a small native program that lets fusionlocalserver's **Open** and
**Insert** buttons drive the Autodesk Fusion desktop client running on *your*
computer.

It is not a background service. It has no window, no tray icon and no settings.
It runs for a second or two when you click a button, does one thing, and exits.

For the design and the reasoning behind it, see [STATUS.md](STATUS.md). This
document is how to install and use it.

---

## Do I need it?

**Probably not, if you run the server on your own machine.** When the browser,
the server and Fusion are all the same computer, the server talks to Fusion
directly and there is nothing to install. Settings → Connection says so:

> This browser is on the same computer as the server, so Open and Insert work
> with nothing installed.

**You need it when the server is somewhere else** — a shared machine, a NAS, a
colleague's workstation. A web page cannot reach `127.0.0.1` on *your* computer
when it was served from another host, so the helper does that part locally.

**Archive does not need the helper at all.** Archiving happens entirely between
the server and Autodesk; the file downloads through your browser like any other.

---

## Install

### 1. Get the binary

Download it from a **[helper release](https://github.com/schneik80/fusionlocalserver/releases?q=fls-helper)**
— these are tagged `helper/vX.Y.Z` and published separately from the server, so
the version numbers are unrelated and you do not need to match them.

Pick the one for your machine:

| File | For |
|---|---|
| `fls-helper-windows-amd64.exe` | Windows, Intel/AMD |
| `fls-helper-windows-arm64.exe` | Windows on ARM (Snapdragon, etc.) |
| `fls-helper-darwin-arm64` | macOS, Apple Silicon (M1 and later) |
| `fls-helper-darwin-amd64` | macOS, Intel |
| `fls-helper-linux-amd64` | Linux, Intel/AMD |
| `fls-helper-linux-arm64` | Linux, ARM64 |

(Or build them yourself: `make helper` writes all six into `dist/`.)

**Rename it to `fls-helper`** (`fls-helper.exe` on Windows) and put it somewhere
permanent before registering it — the registration records the path *and the
name* it was at, so renaming or moving it afterwards breaks the link. Sensible
homes:

- **Windows** — rename to `fls-helper.exe`, at
  `%LOCALAPPDATA%\Programs\fls-helper\fls-helper.exe`
- **macOS / Linux** — rename to `fls-helper`, at `/usr/local/bin/fls-helper`
  (`chmod +x` it)

> **The binaries are not code-signed.** macOS will quarantine the file on first
> run (right-click → Open, or `xattr -d com.apple.quarantine fls-helper`), and
> Windows SmartScreen will warn once ("More info" → "Run anyway"). This is a
> known gap, not a sign anything is wrong.

### 2. Register the URL scheme

```
fls-helper register
```

This tells your OS that `fusionlocal://` links belong to this program. It is
**per-user and needs no administrator rights**. What it actually writes:

| | |
|---|---|
| **Windows** | `HKCU\Software\Classes\fusionlocal` |
| **macOS** | a small handler app in `~/Applications`, then `lsregister` |
| **Linux** | `~/.local/share/applications/fls-helper.desktop`, then `update-desktop-database` |

`fls-helper unregister` reverses it.

> **Upgrading from a helper older than v0.2.0 on macOS: run `register` again.**
> Earlier versions installed a handler that macOS could start but could never
> hand the URL to, so Open and Insert failed while everything reported healthy.
> `fls-helper status` now says so explicitly instead of claiming the scheme is
> registered.

**macOS may ask for Local Network access the first time you use a button.** Allow
it. The helper's whole job is to reach a server on another machine, and macOS
blocks that until you agree — a denial looks exactly like the server being
unreachable. You can change it later in System Settings → Privacy & Security →
Local Network.

### 3. Pair with your server

```
fls-helper pair https://your-server:8080
```

Use the **exact URL you open in the browser** — scheme, host and port must all
match. Settings → Connection shows the right command with your server already
filled in.

This is the step that matters most: **the helper will not act for any server you
have not paired with.** See [Security](#security).

### 4. Check it

```
fls-helper status
```

```
fls-helper v0.1.0

URL scheme  : fusionlocal:// registered (~/Applications/Fusion Local Server Helper.app)
Paired with : https://your-server:8080 (pinned certificate a1b2c3d4…e5f6a7b8)
Fusion      : running, 12 project(s) in its active hub

Recent launches:
  2026-08-12T15:26:13Z launch open ticket=a1b2c3… server=https://your-server:8080
  2026-08-12T15:26:14Z   ok: open bracket.f3d
```

Three lines, three preconditions. If a button does nothing, this tells you which
one is missing without guessing.

The launches below them are the last few times your browser actually reached the
helper, read from `~/.config/fusionlocalserver/helper.log`. **An empty list while
the scheme says registered is itself the answer**: the click never arrived, so
the problem is the registration or the browser, not Fusion. A button press has no
terminal to print to, which is why it keeps this log at all.

---

## Using it

Select a Fusion-native document in the browser and use the details header:

| Button | What happens |
|---|---|
| **Open** | Fusion opens the document. |
| **Insert** | The document is inserted as an occurrence into the design Fusion currently has open. |

Insert only appears for 3D designs — inserting a drawing or a schematic is not a
thing Fusion can do.

Success is quiet: Fusion comes to the front, and that is the confirmation. Only
failures raise a message.

### When something goes wrong

| Message | Meaning |
|---|---|
| **Autodesk Fusion is not running on your computer.** | Start Fusion and try again. |
| **Fusion is signed in to a different hub.** | Fusion cannot see the document. Switch hubs in Fusion. |
| **Fusion has no design open to insert into.** | Insert needs an open design. |
| **No helper app responded.** | Nothing is registered for `fusionlocal://`. Run `register`, then `pair`. On macOS, also re-run `register` if you upgraded from an older helper. |
| **Refused a request from …** (native dialog) | That server is not paired on this machine. |
| **Could not confirm this request with the server.** | The helper could not reach the server, or the request expired. If it says *cannot reach* on macOS, check Local Network access (above); if the server's certificate was replaced, `pair` again. |

The first three appear both in the browser and, if you have navigated away, in
the notification bell.

---

## Security

A URL scheme is a **public entry point**. Any web page in any browser can
navigate to `fusionlocal://…` and your OS will launch the handler with whatever
that page chose. Two things stop that from being a way for a hostile site to
drive your Fusion.

### Pairing

The helper refuses any server you have not explicitly paired with, from a
terminal, on that machine. Nothing in a browser can add a pairing, and nothing
in a launch URL can bypass one. A launch from an unpaired server does nothing
except say so.

Pairings live in `~/.config/fusionlocalserver/helper.json` (mode `0600`). Remove
one with `fls-helper unpair <url>`.

### Certificate pinning

fusionlocalserver serves HTTPS from a self-signed certificate by default, which
no trust store accepts. Rather than disabling verification — which would make
pairing meaningless, since anyone on the network could then impersonate the
server — `pair` records the certificate's SHA-256 **at the moment you vouch for
it**, and later connections must present that exact certificate.

If the server's certificate is legitimately replaced, `pair` again. Until you
do, the helper refuses to talk to it and says why. A server whose certificate
your system already trusts is not pinned at all, because ordinary verification
survives renewal and a pin does not.

### Tickets

The `fusionlocal://` URL carries no document, no project and nothing about you —
only a ticket id and the server's address. The ticket is minted by the browser
while it was authenticated, is bound to that session, expires in **two minutes**,
and can be redeemed **once**. A leaked URL is worth one action, briefly, on a
document its owner could already open. A fabricated one redeems to nothing.

### What the helper can reach

Only `http://127.0.0.1:27182/mcp` — Fusion's own local MCP endpoint — and the
server you paired with. That endpoint is unauthenticated, but it already is:
anything running as you can drive your Fusion. The pairing check is what keeps
*web pages* out of that set.

---

## Versions, and when you actually need to upgrade

The helper and the server are released independently — `helper/vX.Y.Z` tags for
the helper, `vX.Y.Z` for the server — and their version numbers have nothing to
do with each other. A server upgrade does **not** imply a helper upgrade.

They can drift safely because the only thing they share is the shape of the
`fusionlocal://` URL, which carries a version segment (`fusionlocal://v1/open`).
An older helper meeting a newer server is told the version is unsupported and
says so, rather than misreading the request. **A helper upgrade is only required
when that segment changes**, which would be announced in the release notes.

Everything else — new document actions, archive changes, UI work — is
server-side and reaches you by reloading the page.

---

## Architecture

```
  browser                     server                    your machine
 ─────────                  ──────────                 ──────────────
  click Open
      │  POST /api/fusion/action
      ├──────────────────────────►│
      │                           │ same machine?
      │                           ├── yes ─► MCP call, answer directly
      │  { mode: launch,          │
      │    url: fusionlocal://… } │ no ─► mint a 2-minute single-use ticket
      │◄──────────────────────────┤
      │
      │ navigate to fusionlocal://v1/open?ticket=…&server=…
      ▼
   ┌──────────┐  1. is this server paired?  (else refuse, natively)
   │fls-helper│  2. GET /api/fusion/ticket  → action + document
   │          │  3. is Fusion up, same hub? → ActiveHubProjects
   │          │  4. open / insert           → MCP
   │          │  5. POST /api/fusion/callback
   └──────────┘
      │  Fusion opens the document
      ▼
  the browser polls /api/fusion/outcome and reports what happened
```

The **same-machine fast path** is decided on the connection (`RemoteAddr`), never
on anything the client claims. Getting that backwards would drive the *server
operator's* Fusion on a stranger's behalf, so a request carrying any forwarding
header is treated as remote.

### Shared code

The helper is not a reimplementation — it shares its logic with the server, so a
failure is explained the same way whichever path you took:

| Package | What it holds |
|---|---|
| `internal/fusionlink` | the `fusionlocal://` URL and the outcome codes |
| `internal/fusionmcp` | the Fusion MCP client (vendored from [FusionDataCLI](https://github.com/schneik80/FusionDataCLI)) |
| `internal/fusionact` | performing an action and naming its outcome |

---

## Building

```
make helper          # cross-compile every platform into dist/
make helper-install  # go install for this machine, then register + pair
```

Targets are in `HELPER_PLATFORMS` in the `Makefile`. `dist/` is git-ignored:
the binaries are release artifacts, not repository content.

Releases are cut by pushing a `helper/vX.Y.Z` tag, which runs
`.github/workflows/helper-release.yml` — that workflow is just `make helper`
plus checksums, deliberately not GoReleaser (which the server uses): GoReleaser
OSS cannot derive a version from a prefixed tag, and the helper needs none of
what it provides.

**The Windows builds are linked `-H windowsgui`.** They have to be: the OS starts
this program on every Open and Insert, and a console-subsystem binary flashes a
black window each time, which reads as a malfunction. `attachConsole`
(`console_windows.go`) reconnects to the launching terminal so the CLI commands
still print. One quirk is inherent to that trade-off: a shell does not *wait* for
a GUI-subsystem process, so output from `pair` / `register` / `status` arrives
after the prompt has already returned.

---

## Uninstall

```
fls-helper unpair https://your-server:8080
fls-helper unregister
```

Then delete the binary, and `~/.config/fusionlocalserver/helper.json` and
`helper.log` if the server does not also run on that machine.
