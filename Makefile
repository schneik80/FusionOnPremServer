# fusionlocalserver Makefile
#
# The APS client_id is injected at build time so it never appears in source.
# Store your client_id in a local .aps-client-id file (git-ignored), or set
# the CLIENT_ID variable directly:
#
#   make build CLIENT_ID=your-client-id
#   make install CLIENT_ID=your-client-id

CLIENT_ID  ?= $(shell cat .aps-client-id 2>/dev/null | tr -d '[:space:]')
REGION     ?= $(shell cat .aps-region 2>/dev/null | tr -d '[:space:]')
# PUBLIC_URL is the canonical external base URL the APS app's OAuth callback is
# registered under (e.g. https://ryzen-nobara.local:8080). Stored in a local
# .aps-public-url file (git-ignored) and baked in, so the binary serves on the
# host APS expects without the -public-url flag.
PUBLIC_URL ?= $(shell cat .aps-public-url 2>/dev/null | tr -d '[:space:]')
MODULE     := github.com/schneik80/fusionlocalserver
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS    := -X $(MODULE)/config.DefaultClientID=$(CLIENT_ID) \
              -X $(MODULE)/config.DefaultRegion=$(REGION) \
              -X $(MODULE)/config.DefaultPublicURL=$(PUBLIC_URL) \
              -X main.version=$(VERSION)

.PHONY: build install run clean check web helper helper-install

# Build the React/MUI web UI into server/webdist. The whole server/webdist/
# tree is gitignored build output; the `-tags embed_ui` builds below embed it
# via server/static_embed.go. build and install depend on this so the binary
# always ships the current UI. Requires Node/npm.
web:
	cd web && npm install && npm run build

# Production build: bundle the UI and embed it (-tags embed_ui). Without the tag
# the binary serves the "not built yet" stub (server/static_stub.go) instead.
build: web
	@[ -n "$(CLIENT_ID)" ] || (echo "ERROR: CLIENT_ID is not set. See Makefile header." && exit 1)
	go build -tags embed_ui -ldflags "$(LDFLAGS)" -o fusionlocalserver .

install: web
	@[ -n "$(CLIENT_ID)" ] || (echo "ERROR: CLIENT_ID is not set. See Makefile header." && exit 1)
	go install -tags embed_ui -ldflags "$(LDFLAGS)" .

# Build the full app and serve it over HTTPS on the LAN. Binds 0.0.0.0:8080 by
# default (change the port from the web UI's Settings dialog); startup logs the
# reachable https://<lan-ip>:8080 URLs so you can open it from another machine.
# -tls is on by default so the session cookie is Secure (a self-signed cert is
# auto-generated/cached; supply your own via ARGS="-tls-cert … -tls-key …").
# Pass ARGS to add flags, e.g. `make run ARGS="-v"`. To serve plain HTTP instead,
# override TLS: `make run TLS=`.
TLS ?= -tls
run: build
	./fusionlocalserver $(TLS) $(ARGS)

# Dev build: no embedded UI and no embedded client_id — for local dev using env
# vars or config.json. Go-only (stub UI); pair with `cd web && npm run dev` and
# run `./fusionlocalserver -dev` for HMR.
dev:
	go build -o fusionlocalserver .

# The Fusion helper app (cmd/fls-helper): a tiny per-user binary that lets a
# browser on THIS machine drive the local Fusion desktop client. It is only
# needed when the browser and the server are different machines — a
# same-machine setup goes through the server directly. See
# docs/fusion-actions/STATUS.md.
#
# It carries no APS credentials and never talks to APS, so it takes none of
# the ldflags above beyond its version stamp.
HELPER_LDFLAGS := -X main.version=$(VERSION)
# Both macOS architectures are required (Apple Silicon and Intel). Windows gets
# arm64 too — Fusion runs on Windows-on-ARM, and a native helper avoids the
# emulation layer. Linux is built for completeness: Fusion itself is Windows/
# macOS only, so a Linux helper is useful only where Fusion runs under
# Wine/CrossOver and exposes its MCP port on the host loopback.
HELPER_PLATFORMS := darwin/arm64 darwin/amd64 windows/amd64 windows/arm64 linux/amd64 linux/arm64

# Cross-compile release artifacts into dist/.
helper:
	@mkdir -p dist
	@for p in $(HELPER_PLATFORMS); do \
	  os=$${p%/*}; arch=$${p#*/}; ext=""; lf="$(HELPER_LDFLAGS)"; \
	  if [ "$$os" = "windows" ]; then \
	    ext=".exe"; \
	    lf="$$lf -H windowsgui"; \
	  fi; \
	  echo "building dist/fls-helper-$$os-$$arch$$ext"; \
	  GOOS=$$os GOARCH=$$arch go build -ldflags "$$lf" \
	    -o dist/fls-helper-$$os-$$arch$$ext ./cmd/fls-helper || exit 1; \
	done

# Install the helper for the current user and register the URL scheme. Pair it
# with a server afterwards: `fls-helper pair https://your-server:8080`.
helper-install:
	go install -ldflags "$(HELPER_LDFLAGS)" ./cmd/fls-helper
	@echo "Now run: fls-helper register && fls-helper pair <server-url>"

clean:
	rm -f fusionlocalserver
	rm -rf dist

check:
	go vet ./...
	go test -race ./...
