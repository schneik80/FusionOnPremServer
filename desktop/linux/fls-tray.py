#!/usr/bin/env python3
"""
Fusion Local Server — Linux tray app (Ayatana AppIndicator).

A StatusNotifierItem that lives in the tray / notifications area and lets you
start, stop, and monitor the `fusionlocalserver` Go binary. Part of desktop/
(OS-specific manager apps).

Indicator backends, newest first:
  1. AyatanaAppIndicatorGlib 2.0 (libayatana-appindicator-glib) — the current
     library: GTK-free, menu is a Gio.Menu whose item actions live in a
     Gio.SimpleActionGroup under the mandatory ``indicator.`` namespace.
  2. AyatanaAppIndicator3 0.1 (libayatana-appindicator-gtk3) — the deprecated
     GTK3 library, kept as a fallback so the tray still runs where the new
     package isn't available. It prints "libayatana-appindicator is
     deprecated. Please use libayatana-appindicator-glib" on start; installing
     the new library makes this app switch automatically.

Behaviour:
  * The tray icon reflects server state (running / stopped) and the menu offers
    Start, Stop, Open in browser, Show log, and Quit.
  * The server is launched DETACHED (its own session) so it keeps running when
    the tray app quits. The process we started is recorded in a pidfile so Stop
    still works across app restarts.
  * "Running" is detected by probing the configured TCP port — the server has
    no health endpoint, so a listening socket is the ground truth. The port is
    read from ~/.config/fusionlocalserver/server.json (falling back to 8080).
    The probe completes a real TLS handshake and closes cleanly so polling
    never spams the server log with handshake-EOF noise.
  * Start/Stop raise a desktop notification.

Fedora/Nobara packages (the -glib one comes from the terra repo):
    sudo dnf install libayatana-appindicator-glib python3-gobject libnotify
On GNOME the icon needs the "AppIndicator and KStatusNotifierItem Support"
Shell extension (KDE/XFCE/Budgie/etc. render it natively). Run with the SYSTEM
interpreter:
    /usr/bin/python3 fls-tray.py

Environment overrides:
  FLS_BINARY  path to the fusionlocalserver binary (default: repo root, then $PATH)
  FLS_REPO    repo root used as the server's working directory
  FLS_ARGS    extra args appended to the launch command (default just adds -tls)
"""

from __future__ import annotations

import json
import os
import signal
import socket
import ssl
import subprocess
import sys
from pathlib import Path
from shutil import which

try:
    import gi

    gi.require_version("Notify", "0.7")
    from gi.repository import Gio, GLib, Notify  # noqa: E402
except (ImportError, ValueError) as exc:  # pragma: no cover - runtime env guard
    sys.stderr.write(
        "Fusion Local Server tray needs the system PyGObject stack "
        "(python3-gobject, libnotify).\nRun it with the system interpreter, "
        "e.g.:\n\n    /usr/bin/python3 fls-tray.py\n\n"
        f"(import error: {exc})\n"
    )
    raise SystemExit(1)


def load_indicator():
    """Import the best available AppIndicator binding.

    Returns ("glib", module) for AyatanaAppIndicatorGlib 2.0, or
    ("gtk3", module, Gtk) for the deprecated GTK3 library.
    """
    try:
        gi.require_version("AyatanaAppIndicatorGlib", "2.0")
        from gi.repository import AyatanaAppIndicatorGlib as ind  # noqa: PLC0415

        return ("glib", ind, None)
    except (ImportError, ValueError):
        pass
    try:
        gi.require_version("Gtk", "3.0")
        gi.require_version("AyatanaAppIndicator3", "0.1")
        from gi.repository import AyatanaAppIndicator3 as ind  # noqa: PLC0415
        from gi.repository import Gtk  # noqa: PLC0415

        return ("gtk3", ind, Gtk)
    except (ImportError, ValueError) as exc:
        sys.stderr.write(
            "No Ayatana AppIndicator binding found. Install one of:\n"
            "  libayatana-appindicator-glib   (current; terra repo on Fedora)\n"
            "  libayatana-appindicator-gtk3   (deprecated fallback, needs gtk3)\n"
            f"(import error: {exc})\n"
        )
        raise SystemExit(1)


APP_ID = "io.github.schneik80.FusionLocalServerTray"
APP_NAME = "Fusion Local Server"
DEFAULT_PORT = 8080
POLL_SECONDS = 2

# Themed icon names (present in Adwaita and most icon themes). The tray icon
# swaps between them to signal server state at a glance.
ICON_RUNNING = "network-transmit-receive"
ICON_STOPPED = "network-offline"


# ---- server discovery / state ------------------------------------------------


def repo_root() -> Path:
    env = os.environ.get("FLS_REPO")
    if env:
        return Path(env).expanduser()
    # desktop/linux/fls-tray.py -> repo root is two levels up.
    return Path(__file__).resolve().parents[2]


def server_binary() -> Path | None:
    env = os.environ.get("FLS_BINARY")
    if env:
        p = Path(env).expanduser()
        return p if p.exists() else None
    cand = repo_root() / "fusionlocalserver"
    if cand.exists():
        return cand
    found = which("fusionlocalserver")
    return Path(found) if found else None


def config_dir() -> Path:
    return Path(GLib.get_user_config_dir()) / "fusionlocalserver"


def state_dir() -> Path:
    d = Path(GLib.get_user_cache_dir()) / "fusionlocalserver"
    d.mkdir(parents=True, exist_ok=True)
    return d


def configured_port() -> int:
    """The server's bind port from server.json, or the 8080 default."""
    try:
        data = json.loads((config_dir() / "server.json").read_text())
        port = int(data.get("port") or 0)
        if 1 <= port <= 65535:
            return port
    except (OSError, ValueError, json.JSONDecodeError):
        pass
    return DEFAULT_PORT


def port_is_open(port: int) -> bool:
    """Probe the server port WITHOUT spamming its log.

    A bare TCP connect-and-close makes Go's TLS server log
    "http: TLS handshake error ... EOF" — once every poll, that floods the
    log. So after connecting we complete a real TLS handshake (certificate
    unverified — the server's cert is self-signed and we only care about
    liveness) and close cleanly with close_notify, which the server treats
    as an ordinary quiet disconnect. If the handshake fails the server is
    running plain HTTP; the successful TCP connect already proved liveness.
    """
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=0.4) as s:
            ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT)
            ctx.check_hostname = False
            ctx.verify_mode = ssl.CERT_NONE
            try:
                with ctx.wrap_socket(s, server_hostname="localhost"):
                    pass
            except (ssl.SSLError, OSError):
                pass  # non-TLS listener (or handshake hiccup) — still alive
            return True
    except OSError:
        return False


class ServerManager:
    """Launches / signals the Go server and reports whether it is listening."""

    def __init__(self) -> None:
        self.pidfile = state_dir() / "server.pid"
        self.logfile = state_dir() / "server.log"
        # The Popen of a server we started, kept ONLY so it can be reaped. The
        # server is detached (start_new_session) and outlives this app, but it
        # is still our child, so nobody else can reap it — see reap().
        self._proc: subprocess.Popen | None = None

    def _read_pid(self) -> int | None:
        try:
            return int(self.pidfile.read_text().strip())
        except (OSError, ValueError):
            return None

    @staticmethod
    def _alive(pid: int) -> bool:
        try:
            os.kill(pid, 0)
        except OSError:
            return False
        return True

    def reap(self) -> None:
        """Collect the exit status of a server we started, once it has exited.

        A detached child is still a child: the kernel keeps its process entry
        until the parent collects it, so without this every server we start and
        stop leaves a <defunct> entry behind for as long as the tray runs.

        This is not only tidiness. `kill(pid, 0)` SUCCEEDS for a zombie — the
        pid still exists — so managed_pid() would keep reporting a stopped
        server as ours-and-alive, leaving Stop enabled for a process that had
        already exited. Polling Popen.poll() rather than installing a SIGCHLD
        handler keeps subprocess's own bookkeeping authoritative, and costs one
        non-blocking waitpid per tick.
        """
        if self._proc is None:
            return
        if self._proc.poll() is not None:  # exited and now reaped
            self._proc = None

    def managed_pid(self) -> int | None:
        """The pid of a server WE started that is still alive, else None."""
        self.reap()
        pid = self._read_pid()
        return pid if pid and self._alive(pid) else None

    def port(self) -> int:
        return configured_port()

    def is_running(self) -> bool:
        return port_is_open(self.port())

    def url(self) -> str:
        return f"https://localhost:{self.port()}"

    def start(self) -> None:
        binary = server_binary()
        if binary is None:
            raise FileNotFoundError(
                "fusionlocalserver binary not found — build it with `make build` "
                "(or set FLS_BINARY)."
            )
        args = [str(binary), "-tls"]
        if extra := os.environ.get("FLS_ARGS"):
            args += extra.split()
        stamp = GLib.DateTime.new_now_local().format_iso8601()
        logf = open(self.logfile, "ab", buffering=0)
        logf.write(f"\n=== started {stamp} ({' '.join(args)}) ===\n".encode())
        proc = subprocess.Popen(  # noqa: S603 - args are our own, not user input
            args,
            cwd=str(repo_root()),
            stdout=logf,
            stderr=subprocess.STDOUT,
            stdin=subprocess.DEVNULL,
            start_new_session=True,  # detach: the server outlives the tray
        )
        # Held so reap() can collect it. Starting a second server replaces this
        # reference, but stop() reaps the previous one first, so nothing leaks.
        self._proc = proc
        self.pidfile.write_text(str(proc.pid))

    def stop(self) -> None:
        pid = self.managed_pid()
        if pid is None:
            self.pidfile.unlink(missing_ok=True)  # clear a stale file
            return
        try:
            os.killpg(os.getpgid(pid), signal.SIGTERM)
        except OSError:
            try:
                os.kill(pid, signal.SIGTERM)
            except OSError:
                pass
        self.pidfile.unlink(missing_ok=True)
        # The server drains connections before exiting, so it is not gone yet.
        # Reaping is left to the poll tick rather than waited on here: blocking
        # the GLib loop would freeze the tray for the length of the shutdown.
        self.reap()


# ---- tray UI -----------------------------------------------------------------


class TrayBase:
    """Backend-independent state + actions; subclasses render the menu/icon."""

    def __init__(self) -> None:
        self.manager = ServerManager()

    # -- state --
    def state(self) -> dict:
        running = self.manager.is_running()
        managed = self.manager.managed_pid() is not None
        have_binary = server_binary() is not None
        if running:
            suffix = "" if managed else " (external)"
            label = f"Running{suffix} · {self.manager.url()}"
        elif have_binary:
            label = "Stopped"
        else:
            label = "Stopped · binary not found (make build)"
        return {
            "icon": ICON_RUNNING if running else ICON_STOPPED,
            "icon_desc": "running" if running else "stopped",
            "label": label,
            "can_start": not running and have_binary,
            # We can only stop a process we started (tracked via the pidfile).
            "can_stop": running and managed,
            "can_open": running,
        }

    def _tick(self) -> bool:
        self.refresh()
        return GLib.SOURCE_CONTINUE

    def _refresh_soon(self) -> None:
        # Port state lags a start/stop by a beat; nudge one refresh, then the
        # periodic poll takes over.
        GLib.timeout_add(600, self._refresh_once)

    def _refresh_once(self) -> bool:
        self.refresh()
        return GLib.SOURCE_REMOVE

    def _notify(self, body: str, icon: str) -> None:
        Notify.Notification.new(APP_NAME, body, icon).show()

    # -- actions --
    def on_start(self, *_a) -> None:
        try:
            self.manager.start()
            self._notify(f"Starting on {self.manager.url()}", ICON_RUNNING)
        except OSError as exc:
            self._notify(str(exc), ICON_STOPPED)
        self._refresh_soon()

    def on_stop(self, *_a) -> None:
        self.manager.stop()
        self._notify("Stopping server…", ICON_STOPPED)
        self._refresh_soon()

    def on_open(self, *_a) -> None:
        Gio.AppInfo.launch_default_for_uri(self.manager.url(), None)

    def on_log(self, *_a) -> None:
        Gio.AppInfo.launch_default_for_uri(self.manager.logfile.as_uri(), None)

    def refresh(self) -> None:  # pragma: no cover - overridden
        raise NotImplementedError


class TrayGlib(TrayBase):
    """AyatanaAppIndicatorGlib 2.0: Gio.Menu + Gio.SimpleActionGroup (no GTK).

    Menu item actions MUST use the ``indicator.`` namespace, and the indicator
    is only rendered once it has BOTH a menu and an action group (per the
    library docs).
    """

    def __init__(self, ind_mod, quit_cb) -> None:
        super().__init__()
        self.ind_mod = ind_mod
        self.quit_cb = quit_cb

        self.actions = Gio.SimpleActionGroup()
        self.action = {}
        for name, cb in (
            ("status", None),  # the insensitive status line
            ("start", self.on_start),
            ("stop", self.on_stop),
            ("open", self.on_open),
            ("log", self.on_log),
            ("quit", lambda *_a: quit_cb()),
        ):
            act = Gio.SimpleAction.new(name, None)
            if cb is not None:
                act.connect("activate", cb)
            else:
                act.set_enabled(False)
            self.actions.add_action(act)
            self.action[name] = act

        # Sections: [status] / [start/stop/open/log] / [quit]. The status
        # section is rebuilt whenever its text changes (GMenu labels are
        # immutable in place).
        self.status_section = Gio.Menu()
        self._status_label = ""
        ops = Gio.Menu()
        ops.append("Start server", "indicator.start")
        ops.append("Stop server", "indicator.stop")
        ops.append("Open in browser", "indicator.open")
        ops.append("Show server log", "indicator.log")
        tail = Gio.Menu()
        tail.append("Quit", "indicator.quit")
        menu = Gio.Menu()
        menu.append_section(None, self.status_section)
        menu.append_section(None, ops)
        menu.append_section(None, tail)

        self.indicator = ind_mod.Indicator.new(
            APP_ID, ICON_STOPPED, ind_mod.IndicatorCategory.APPLICATION_STATUS
        )
        self.indicator.set_title(APP_NAME)
        self.indicator.set_actions(self.actions)
        self.indicator.set_menu(menu)
        self.indicator.set_status(ind_mod.IndicatorStatus.ACTIVE)
        # Primary click (where the panel supports it) opens the app.
        self.indicator.connect("activate", lambda *_a: self.on_open())

        self.refresh()
        GLib.timeout_add_seconds(POLL_SECONDS, self._tick)

    def refresh(self) -> None:
        st = self.state()
        self.indicator.set_icon(st["icon"], st["icon_desc"])
        if st["label"] != self._status_label:
            self._status_label = st["label"]
            self.status_section.remove_all()
            self.status_section.append(st["label"], "indicator.status")
        self.action["start"].set_enabled(st["can_start"])
        self.action["stop"].set_enabled(st["can_stop"])
        self.action["open"].set_enabled(st["can_open"])


class TrayGtk3(TrayBase):
    """AyatanaAppIndicator3 0.1 (deprecated GTK3 fallback): Gtk.Menu."""

    def __init__(self, ind_mod, gtk, quit_cb) -> None:
        super().__init__()
        self.gtk = gtk

        self.menu = gtk.Menu()
        self.status_item = gtk.MenuItem(label="…")
        self.status_item.set_sensitive(False)
        self.start_item = gtk.MenuItem(label="Start server")
        self.start_item.connect("activate", self.on_start)
        self.stop_item = gtk.MenuItem(label="Stop server")
        self.stop_item.connect("activate", self.on_stop)
        self.open_item = gtk.MenuItem(label="Open in browser")
        self.open_item.connect("activate", self.on_open)
        self.log_item = gtk.MenuItem(label="Show server log")
        self.log_item.connect("activate", self.on_log)
        quit_item = gtk.MenuItem(label="Quit")
        quit_item.connect("activate", lambda *_a: quit_cb())

        for widget in (
            self.status_item,
            gtk.SeparatorMenuItem(),
            self.start_item,
            self.stop_item,
            self.open_item,
            self.log_item,
            gtk.SeparatorMenuItem(),
            quit_item,
        ):
            self.menu.append(widget)
        self.menu.show_all()

        self.indicator = ind_mod.Indicator.new(
            APP_ID, ICON_STOPPED, ind_mod.IndicatorCategory.APPLICATION_STATUS
        )
        self.indicator.set_status(ind_mod.IndicatorStatus.ACTIVE)
        self.indicator.set_title(APP_NAME)
        self.indicator.set_menu(self.menu)

        self.refresh()
        GLib.timeout_add_seconds(POLL_SECONDS, self._tick)

    def refresh(self) -> None:
        st = self.state()
        self.indicator.set_icon_full(st["icon"], st["icon_desc"])
        self.status_item.set_label(st["label"])
        self.start_item.set_sensitive(st["can_start"])
        self.stop_item.set_sensitive(st["can_stop"])
        self.open_item.set_sensitive(st["can_open"])


def main() -> int:
    backend, ind_mod, gtk = load_indicator()
    Notify.init(APP_NAME)

    if backend == "glib":
        loop = GLib.MainLoop()
        TrayGlib(ind_mod, loop.quit)
        for sig in (signal.SIGINT, signal.SIGTERM):
            GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, sig, loop.quit)
        try:
            loop.run()
        finally:
            Notify.uninit()
    else:
        TrayGtk3(ind_mod, gtk, gtk.main_quit)
        for sig in (signal.SIGINT, signal.SIGTERM):
            GLib.unix_signal_add(GLib.PRIORITY_DEFAULT, sig, gtk.main_quit)
        try:
            gtk.main()
        finally:
            Notify.uninit()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
