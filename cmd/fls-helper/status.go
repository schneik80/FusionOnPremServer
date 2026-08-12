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

	// 1. Is the scheme registered? Without this the browser click does nothing
	//    at all, which is the most confusing failure of the three.
	if where, ok := schemeRegistration(); ok {
		fmt.Printf("URL scheme  : %s:// registered (%s)\n", fusionlink.Scheme, where)
	} else {
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
	projects, err := fusionmcp.NewClient().ActiveHubProjects(ctx)
	if err != nil {
		fmt.Printf("Fusion      : not reachable at %s (%v)\n", fusionmcp.DefaultEndpoint, err)
		return
	}
	fmt.Printf("Fusion      : running, %d project(s) in its active hub\n", len(projects))
}

// shortFingerprint renders a pin compactly enough to compare by eye.
func shortFingerprint(fp string) string {
	if len(fp) <= 16 {
		return fp
	}
	return fp[:8] + "…" + fp[len(fp)-8:]
}
