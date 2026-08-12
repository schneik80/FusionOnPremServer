// Command fls-helper bridges a browser to the Fusion desktop client running on
// the same machine.
//
// fusionlocalserver's SPA may be served from another machine, and a browser
// cannot reach 127.0.0.1 on the user's computer from a page hosted elsewhere.
// So the SPA hands the OS a fusionlocal:// URL, the OS launches this program,
// and this program makes the local MCP call to Fusion and reports the result
// back to the server.
//
// It is deliberately tiny and has no UI: it runs for a couple of seconds and
// exits, showing a native message only when something went wrong (there is no
// browser tab it could report into).
//
// Usage:
//
//	fls-helper fusionlocal://v1/open?ticket=…&server=…   launch (what the OS invokes)
//	fls-helper pair <server-url>                         trust a server
//	fls-helper unpair <server-url>                       stop trusting one
//	fls-helper register / unregister                     (de)register the URL scheme
//	fls-helper status                                    report what this machine can do
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
)

// version is stamped at build time (-ldflags "-X main.version=…"), matching how
// the server takes its own version.
var version = "dev"

func main() {
	// Windows only: reconnect to the launching terminal, because the binary is
	// linked as a GUI app so protocol launches do not flash a console. No-op
	// elsewhere. Must run before anything prints.
	attachConsole()

	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	// A launch is the common case and arrives as a bare URL argument, not a
	// subcommand — the OS passes "%1" with no verb. Recognizing it by scheme
	// keeps the subcommand names free.
	if strings.HasPrefix(strings.ToLower(args[0]), fusionlink.Scheme+":") {
		os.Exit(runLaunch(args[0]))
	}

	switch args[0] {
	case "pair":
		if len(args) < 2 {
			fatal("pair needs a server URL, e.g. fls-helper pair https://fusion.example:8080")
		}
		if err := pair(args[1]); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("paired with %s\n", fusionlink.NormalizeOrigin(args[1]))

	case "unpair":
		if len(args) < 2 {
			fatal("unpair needs a server URL")
		}
		if err := unpair(args[1]); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("unpaired %s\n", fusionlink.NormalizeOrigin(args[1]))

	case "register":
		if err := registerScheme(); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("registered the %s:// scheme for this user\n", fusionlink.Scheme)

	case "unregister":
		if err := unregisterScheme(); err != nil {
			fatal(err.Error())
		}
		fmt.Printf("unregistered the %s:// scheme\n", fusionlink.Scheme)

	case "status":
		printStatus()

	case "version", "-v", "--version":
		fmt.Printf("fls-helper %s\n", version)

	case "help", "-h", "--help":
		usage()

	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", args[0])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `fls-helper %s — opens and inserts Fusion documents on behalf of fusionlocalserver.

  fls-helper pair <server-url>     trust a fusionlocalserver (required before it can act)
  fls-helper unpair <server-url>   stop trusting one
  fls-helper register              register the %s:// scheme with this OS, for this user
  fls-helper unregister            remove that registration
  fls-helper status                show paired servers, scheme registration, and whether Fusion is up
  fls-helper version

The browser invokes it as:
  fls-helper %s://v1/open?ticket=<id>&server=<origin>
`, version, fusionlink.Scheme, fusionlink.Scheme)
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "fls-helper: "+msg)
	os.Exit(1)
}
