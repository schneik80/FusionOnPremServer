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

Pick the one for your machine from the release artifacts:

| File | For |
|---|---|
| `fls-helper-windows-amd64.exe` | Windows, Intel/AMD |
| `fls-helper-windows-arm64.exe` | Windows on ARM (Snapdragon, etc.) |
| `fls-helper-darwin-arm64` | macOS, Apple Silicon (M1 and later) |
| `fls-helper-darwin-amd64` | macOS, Intel |
| `fls-helper-linux-amd64` | Linux, Intel/AMD |
| `fls-helper-linux-arm64` | Linux, ARM64 |

Put it somewhere permanent before registering it — the registration records the
path it was at, so moving it afterwards breaks the link. Sensible homes:

- **Windows** — `%LOCALAPPDATA%\Programs\fls-helper\fls-helper.exe`
- **macOS / Linux** — `/usr/local/bin/fls-helper` (`chmod +x` it)

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
| **macOS** | a minimal `.app` in `~/Applications`, then `lsregister` |
| **Linux** | `~/.local/share/applications/fls-helper.desktop`, then `update-desktop-database` |

`fls-helper unregister` reverses it.

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
```

Three lines, three preconditions. If a button does nothing, this tells you which
one is missing without guessing.

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
| **No helper app responded.** | Nothing is registered for `fusionlocal://`. Run `register`, then `pair`. |
| **Refused a request from …** (native dialog) | That server is not paired on this machine. |

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

Targets are in `HELPER_PLATFORMS` in the `Makefile`.

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

Then delete the binary, and `~/.config/fusionlocalserver/helper.json` if the
server does not also run on that machine.
