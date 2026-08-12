package main

import (
	"context"
	"fmt"
	"time"

	"github.com/schneik80/fusionlocalserver/internal/fusionlink"
	"github.com/schneik80/fusionlocalserver/internal/fusionmcp"
)

// printStatus reports everything that has to be true for Open/Insert to work,
// so a user who clicked a button and saw nothing can find out which of the
// three preconditions is missing without guessing.
func printStatus() {
	fmt.Printf("fls-helper %s\n\n", version)

	// 1. Can a launch reach this binary? Without this the browser click does
	//    nothing at all, which is the most confusing failure of the three — and
	//    a registration that exists but cannot deliver looks identical to one
	//    that works, so the two are reported differently on purpose.
	where, state, why := schemeRegistration()
	switch state {
	case schemeGood:
		fmt.Printf("URL scheme  : %s:// registered (%s)\n", fusionlink.Scheme, where)
	case schemeStale:
		fmt.Printf("URL scheme  : %s:// registered at %s, but %s\n", fusionlink.Scheme, where, why)
		fmt.Printf("              → run `fls-helper register` to fix it\n")
	default:
		fmt.Printf("URL scheme  : %s:// NOT registered — run `fls-helper register`\n", fusionlink.Scheme)
	}

	// 2. Which servers may drive this machine.
	pf, err := loadPairings()
	switch {
	case err != nil:
		fmt.Printf("Paired with : unreadable — %v\n", err)
	case len(pf.Servers) == 0:
		fmt.Printf("Paired with : nothing — run `fls-helper pair <server-url>`\n")
	default:
		for i, s := range pf.Servers {
			label := "Paired with :"
			if i > 0 {
				label = "             "
			}
			pin := "system-trusted certificate"
			if s.Fingerprint != "" {
				pin = "pinned certificate " + shortFingerprint(s.Fingerprint)
			}
			fmt.Printf("%s %s (%s)\n", label, s.Origin, pin)
		}
	}

	// 3. Is Fusion actually up? Uses the same probe a real action would.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if projects, perr := fusionmcp.NewClient().ActiveHubProjects(ctx); perr != nil {
		fmt.Printf("Fusion      : not reachable at %s (%v)\n", fusionmcp.DefaultEndpoint, perr)
	} else {
		fmt.Printf("Fusion      : running, %d project(s) in its active hub\n", len(projects))
	}

	// 4. What happened the last few times the OS launched us. A launch has no
	//    terminal, so this log is the only record it leaves — and an empty list
	//    under a registered scheme is itself the answer, because it means the
	//    click never reached this program.
	launches := recentLaunches(5)
	if len(launches) == 0 {
		fmt.Printf("\nNo launches recorded yet.\n")
		return
	}
	fmt.Printf("\nRecent launches:\n")
	for _, l := range launches {
		fmt.Printf("  %s\n", l)
	}
}

// shortFingerprint renders a pin compactly enough to compare by eye.
func shortFingerprint(fp string) string {
	if len(fp) <= 16 {
		return fp
	}
	return fp[:8] + "…" + fp[len(fp)-8:]
}
