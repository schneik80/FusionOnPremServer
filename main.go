package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/schneik80/fusionlocalserver/config"
	"github.com/schneik80/fusionlocalserver/internal/appver"
	"github.com/schneik80/fusionlocalserver/server"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func main() {
	// Recorded first so every data file the stores stamp from here on
	// carries the real build version (schemameta reads it).
	appver.Set(version)
	var (
		verbose   = flag.Bool("v", false, "verbose logging (debug level, to console and the log file)")
		dev       = flag.Bool("dev", false, "developer mode: proxy the web UI to the Vite dev server for HMR")
		tls       = flag.Bool("tls", false, "serve over HTTPS so the session cookie is Secure (self-signed cert auto-generated if -tls-cert/-tls-key are not given)")
		tlsCert   = flag.String("tls-cert", "", "path to a TLS certificate (PEM); requires -tls and -tls-key")
		tlsKey    = flag.String("tls-key", "", "path to the TLS private key (PEM); requires -tls and -tls-cert")
		publicURL = flag.String("public-url", "", "canonical external base URL clients use, e.g. https://fusion.lan:8080; when set, the OAuth redirect_uri is built from it (register just that one callback) and requests to other hosts are redirected to it")
	)
	flag.Parse()

	// Fall back to the build-time canonical URL (the address the APS app's OAuth
	// callback is registered under) when -public-url is not given, so a published
	// binary serves on the expected host without the flag.
	if *publicURL == "" && config.DefaultPublicURL != "" {
		*publicURL = config.DefaultPublicURL
	}

	cfg, cfgErr := config.Load()

	if err := server.Run(server.Options{
		Verbose:   *verbose,
		Dev:       *dev,
		TLS:       *tls,
		TLSCert:   *tlsCert,
		TLSKey:    *tlsKey,
		PublicURL: *publicURL,
		Config:    cfg,
		CfgErr:    cfgErr,
		Version:   version,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "server error: %v\n", err)
		os.Exit(1)
	}
}
