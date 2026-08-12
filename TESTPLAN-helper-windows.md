# fls-helper on Windows — first-run test plan

**Temporary. Not committed — delete when done.**

The helper has never executed on Windows. It cross-compiles and its shared logic
is covered by tests, but four things have only ever been reasoned about:

1. `-H windowsgui` + `AttachConsole` — whether CLI commands print at all
2. the registry write for the `fusionlocal://` scheme
3. `MessageBoxW` for failure dialogs
4. `%USERPROFILE%\.config\fusionlocalserver\helper.json` as a config path

This plan is ordered so a failure stops you at the cheapest possible point.
Record the **exact** output of anything unexpected — especially anything that
prints nothing.

---

## Phase 0 — before you touch the helper

These are the things most likely to waste your time if skipped.

### 0.1 The server is running and the Windows box can resolve its name

**This one is load-bearing.** The server has `https://ryzen-nobara.local:8080`
baked in as its public URL, so every `fusionlocal://` launch tells the helper to
redeem its ticket at *that hostname* — not at whatever address you typed. If
Windows cannot resolve `ryzen-nobara.local` (mDNS is not always reliable there),
the helper will fail at ticket redemption no matter what else is correct.

From **PowerShell on Windows**:

```powershell
Resolve-DnsName ryzen-nobara.local
# or
ping ryzen-nobara.local
```

- [ ] resolves to the Linux box's IP

> If it does not resolve: add a hosts entry
> (`C:\Windows\System32\drivers\etc\hosts`, as Administrator):
> `10.0.4.26  ryzen-nobara.local`
> Using the bare IP instead will *not* work — the server redirects to its
> canonical host, and the pairing must match it exactly.

### 0.2 The SPA works from Windows

In a Windows browser, open **https://ryzen-nobara.local:8080** — accept the
self-signed certificate warning, sign in, pick the hub.

- [ ] the browser lands in the app and shows projects

### 0.3 Fusion is running and on the same hub

- [ ] Autodesk Fusion is open
- [ ] signed in to the **same hub** you selected in the SPA

### 0.4 Settings agrees that you need the helper

Settings → Connection → **Fusion helper**.

- [ ] it says the browser is **not** on the server's computer, and shows the
      `register` / `pair` commands with the server URL filled in

> If it says the opposite, you are browsing from the Linux box — the helper is
> not involved at all and this plan does not apply.

---

## Phase 1 — install

### 1.1 Copy the binary

From the Linux box, `dist/fls-helper-windows-amd64.exe` (use `-arm64` on
Snapdragon hardware). **Rename it to `fls-helper.exe`** and put it somewhere
permanent — every command below invokes it by that name:

```powershell
mkdir "$env:LOCALAPPDATA\Programs\fls-helper" -Force
copy <source>\fls-helper-windows-amd64.exe "$env:LOCALAPPDATA\Programs\fls-helper\fls-helper.exe"
```

- [ ] copied **and renamed**

> Both must happen **before phase 2**. `register` records the path *and the name*
> the binary was at, so renaming it afterwards leaves the registry pointing at a
> file that no longer exists — and a `fusionlocal://` launch that fails with no
> console and no window to read it in.

### 1.2 First run — SmartScreen

Open **PowerShell** and `cd` to that folder.

```powershell
.\fls-helper.exe version
```

- [ ] SmartScreen warned, *or* did not — see below. If it did: "More info" →
      "Run anyway"
- [ ] **prints** `fls-helper v0.1.0-205-gdbfc3fa`

> **No SmartScreen warning is not a failure.** The binaries are unsigned, but
> the warning is triggered by the mark-of-the-web, which only a browser download
> attaches. A copy that arrived by file share, `scp`, or a sync client
> (Dropbox, OneDrive) carries no `Zone.Identifier` stream and starts silently.
> Check which you have with `Get-Item <path> -Stream *`.

> **This is test #1 and the one most likely to fail.** The binary is linked as a
> GUI app so protocol launches do not flash a console, and `AttachConsole`
> reattaches to your terminal. Expected quirks that are **not** failures:
> output may appear *after* the prompt returns, or interleaved with it — a shell
> does not wait for a GUI-subsystem process.
>
> **Record which of these you see:**
> - [ ] prints normally
> - [ ] prints, but after the prompt came back
> - [ ] prints nothing at all  ← this is a real failure, note it and continue

### 1.3 Baseline status

```powershell
.\fls-helper.exe status
```

Expected — nothing set up yet, but Fusion already reachable:

```
URL scheme  : fusionlocal:// NOT registered — run `fls-helper register`
Paired with : nothing — run `fls-helper pair <server-url>`
Fusion      : running, N project(s) in its active hub
```

- [ ] scheme: NOT registered
- [ ] paired: nothing
- [ ] **Fusion: running, N projects** ← this proves the MCP client works on Windows

> If Fusion says "not reachable" while Fusion is open, stop and report it. That
> is `internal/fusionmcp` failing, and nothing downstream can work.

---

## Phase 2 — register the URL scheme

```powershell
.\fls-helper.exe register
```

- [ ] prints `registered the fusionlocal:// scheme for this user`

Verify what it actually wrote:

```powershell
reg query HKCU\Software\Classes\fusionlocal /s
```

- [ ] `URL Protocol` value exists
- [ ] `shell\open\command` is `"C:\...\fls-helper.exe" "%1"` — **with the quotes**

```powershell
.\fls-helper.exe status
```

- [ ] scheme now reports registered

---

## Phase 3 — pair with the server

```powershell
.\fls-helper.exe pair https://ryzen-nobara.local:8080
```

- [ ] prints `paired with https://ryzen-nobara.local:8080`

```powershell
.\fls-helper.exe status
type $env:USERPROFILE\.config\fusionlocalserver\helper.json
```

- [ ] status shows the server, with either `pinned certificate <hash>` or
      `system-trusted certificate`
- [ ] `helper.json` exists at that path and contains the origin

> The `.config` path is Unix-shaped on purpose — it matches where the server
> keeps its own config. Confirm Windows created it without complaint.

---

## Phase 4 — the actual thing

In the Windows browser, select a **Fusion-native design** (not an uploaded STEP
or PDF — the buttons only appear for native documents).

### 4.1 Open

Click **Open** in the details header.

- [ ] the browser asks permission to open the helper → allow it
- [ ] **no black console window flashes** ← this is test #2, the `-H windowsgui`
      verification. Watch carefully; it would be brief.
- [ ] Fusion comes to the front with the document open
- [ ] the browser shows **no** dialog (success is deliberately silent)

### 4.2 Insert

With a design already open in Fusion, select a *different* design and click
**Insert**.

- [ ] the component is inserted as an occurrence into the open design
- [ ] no console flash, no dialog

---

## Phase 5 — failure paths

Each of these should produce a **specific** message, not a generic one.

### 5.1 Fusion closed

Quit Fusion entirely, then click **Open** in the browser.

- [ ] a **native Windows dialog** appears (test #3, `MessageBoxW`) saying Fusion
      is not running
- [ ] the browser dialog says "Autodesk Fusion is not running on your computer."
- [ ] the notification bell gets an entry within a few seconds

Restart Fusion before continuing.

### 5.2 Nothing to insert into

In Fusion, close all documents (no active design). Click **Insert**.

- [ ] the message names *this* problem — "Fusion has no design open to insert
      into" — not a generic failure

### 5.3 Wrong hub

If you have a second hub: switch Fusion to it, leave the SPA on the first, click
**Open**.

- [ ] the message says Fusion is signed in to a different hub

---

## Phase 6 — security behaviour

### 6.1 An unpaired server is refused

```powershell
.\fls-helper.exe unpair https://ryzen-nobara.local:8080
```

Now click **Open** in the browser.

- [ ] a native dialog says it **refused** a request from that server and tells
      you the `pair` command
- [ ] Fusion does **not** open anything

Re-pair before continuing:

```powershell
.\fls-helper.exe pair https://ryzen-nobara.local:8080
```

### 6.2 A hand-made URL does nothing

```powershell
Start-Process "fusionlocal://v1/open?ticket=made-up&server=https%3A%2F%2Fevil.example"
```

- [ ] a native dialog refuses it (unpaired server)
- [ ] Fusion does nothing

---

## Phase 7 — Archive (no helper involved)

Archive runs entirely between the server and Autodesk; this just confirms it
works from a Windows browser too.

- [ ] click **Archive** on a native design
- [ ] the bell raises a notification within a couple of minutes
- [ ] clicking the download button in the bell saves a real `.f3d` / `.f3z`
- [ ] the file **is not** a `.json`
- [ ] the downloaded file opens in Fusion

---

## What to send back

1. The three test results called out above:
   - CLI output behaviour (1.2)
   - console flash on launch, yes/no (4.1)
   - native dialog appearance (5.1)
2. Full output of `fls-helper status` after Phase 3
3. Exact text of anything that failed
4. From the Linux box, the tail of the server log for the same period:
   ```
   grep -aE 'fusion|archive' ~/.config/fusionlocalserver/server.log | tail -40
   ```

## Known, expected, not bugs

- SmartScreen warning on first run — the binaries are unsigned
- CLI output arriving after the prompt returns — inherent to a GUI-subsystem
  binary; a shell does not wait for one
- The browser asking permission the first time a `fusionlocal://` link is opened
- Success being silent — Fusion coming to the front is the confirmation
