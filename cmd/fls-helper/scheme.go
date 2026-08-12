package main

// schemeState is what schemeRegistration reports, and it has three values
// rather than two on purpose.
//
// A boolean "is it registered" is not the question that matters. macOS shipped
// a registration that existed, named the right scheme, and could never receive
// a URL — because the OS delivers one as an Apple Event to a bundle, not as
// argv to an executable, and the bundle's executable was a plain `sh` shim. The
// check said "registered" for as long as that lasted, so the one command whose
// job is to find the missing precondition pointed away from it.
//
// So the question is "can this registration actually deliver a URL to *this*
// binary", and its wrong answer has two distinguishable causes with different
// fixes: nothing is installed, or something is installed that cannot work.
type schemeState int

const (
	// schemeAbsent — nothing is registered for the scheme.
	schemeAbsent schemeState = iota
	// schemeStale — a registration exists but cannot deliver to this binary:
	// written by an older fls-helper, or pointing at a path the binary has
	// since moved from. `register` fixes it.
	schemeStale
	// schemeGood — registered, the right shape, and pointing here.
	schemeGood
)
