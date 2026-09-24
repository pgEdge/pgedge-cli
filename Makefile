BINARY  := pgedge
VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
PKG         := github.com/pgEdge/pgedge-cli/internal/cli
PKG_STARFLEET   := github.com/pgEdge/pgedge-cli/internal/starfleet
PKG_CONTROLPLANE      := github.com/pgEdge/pgedge-cli/internal/controlplane

# Per-module versions. The launcher stamps to the release tag (VERSION);
# each module stamps to its own version from versions.env, so modules
# move on their own cadence. Values there are overridable from the
# environment (?=) for one-off local builds.
include versions.env
STARFLEET_VERSION ?= dev
CONTROLPLANE_VERSION    ?= dev

LDFLAGS := -ldflags "-X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).BuildDate=$(DATE) -X $(PKG_STARFLEET).Version=$(STARFLEET_VERSION) -X $(PKG_CONTROLPLANE).Version=$(CONTROLPLANE_VERSION)"

.PHONY: build test test-scripts test-integration test-integration-lifecycle test-inspect-live lint lint-docs clean generate docs docs-check build-all build-one notice vendor-spec vendor-spec-check

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/pgedge

# The gate script owns the profile filter and the threshold, and CI runs
# the same script with the same defaults. It used to be duplicated here,
# which put two filters in two files with only a substring heuristic
# holding them together — a second filter spelled any other way was
# invisible to that (#224).
test:
	go test -race -coverprofile=coverage.raw.out ./...
	scripts/coverage-gate.sh

# Shell-script tests: shellcheck + the shell unit-test suites.
test-scripts:
	shellcheck install.sh scripts/coverage-gate.sh scripts/lint-docs.sh \
		scripts/cp-service-keys.sh scripts/lint-version.sh \
		scripts/third-party-licenses.sh \
		test/install_checksum_test.sh \
		test/coverage_gate_test.sh test/cp_service_keys_test.sh \
		test/lint_version_test.sh test/third_party_licenses_test.sh
	sh test/install_checksum_test.sh
	sh test/coverage_gate_test.sh
	sh test/cp_service_keys_test.sh
	sh test/lint_version_test.sh
	sh test/third_party_licenses_test.sh

# Integration tests against a live API. Requires a configured profile
# (PGEDGE_INTEGRATION_PROFILE, default "dev") in ~/.pgedge/cli/config.yaml.
# Excluded from `make test` — these hit the network.
test-integration:
	PGEDGE_INTEGRATION=1 go test ./test/integration/ -v -count=1

# Every inspect analysis through the built binary against a real
# Postgres pair from test/inspectlive/compose.yaml, cell values checked
# against rows the suite creates. The unit tests answer SQL from a
# scripted fake, so this is the only test that can see an alias bound to
# the wrong expression. Needs Docker; PG_VERSION picks the image (18, 17
# or 16). Excluded from `make test`. About six minutes, five of them
# waiting for long-running-queries' session to turn five minutes old;
# GOTESTFLAGS=-short skips that one test.
PG_VERSION ?= 18
test-inspect-live:
	PG_VERSION=$(PG_VERSION) docker compose -f test/inspectlive/compose.yaml up --wait
	PGEDGE_INSPECT_LIVE=1 PGEDGE_INSPECT_LIVE_PG_VERSION=$(PG_VERSION) \
	    go test ./test/inspectlive/ -v -count=1 -timeout 15m $(GOTESTFLAGS); \
	    rc=$$?; docker compose -f test/inspectlive/compose.yaml down -v; exit $$rc

# Build-up/tear-down suite: creates REAL cloud infrastructure, costs
# money, and takes 40-90 minutes. Needs
# PGEDGE_INTEGRATION_CLOUD_ACCOUNT_ID set to a cloud account you own.
# -timeout 3h because the Go default of 10m would kill the run
# mid-provision and leak the infrastructure it had created.
test-integration-lifecycle:
	PGEDGE_INTEGRATION=1 PGEDGE_INTEGRATION_LIFECYCLE=1 \
		go test ./test/integration/ -run TestBYOCLifecycle \
		-v -count=1 -timeout 3h

lint:
	@scripts/lint-version.sh
	golangci-lint run
	gofmt -l . | tee /dev/stderr | test -z "$$(cat)"

# Prose linting (Vale) over Markdown, llms.txt and Go doc comments.
# Kept separate from `lint`: CI runs both as sibling jobs. The scope
# (which files, which style) lives in .vale.ini; the generated
# command-reference blocks and doc-gate lines need a line-based
# pre-filter that Vale's own ignore directives can't express — see
# scripts/lint-docs.sh's header comment for why.
lint-docs:
	scripts/lint-docs.sh

clean:
	rm -f $(BINARY) coverage.out coverage.raw.out
	rm -rf dist/

generate:
	go generate ./...

# Regenerate the command reference (llms.txt + internal/*/llms.txt)
# from the cobra tree. Only the text between the <!-- BEGIN/END
# GENERATED --> markers is rewritten; every other line is hand-written
# and left alone. Run this after adding a command or changing a flag —
# TestReferenceDocsConform fails until you do.
docs:
	go run ./cmd/gendocs

# What CI runs: the same generator in report-only mode.
docs-check:
	go run ./cmd/gendocs -check

# Capture the published per-product OpenAPI contracts into openapi/.
#
# Each contract is served unauthenticated at
# {base}/{product}/v1/openapi.json (saas #1920), already
# filtered to the public, enterprise-maximal surface by saas's own
# GeneratePublicSpec — the tool fetches, validates fail-closed and
# converts to YAML; nothing here derives or filters. Captures from
# PRODUCTION by default, because the vendored contract must be the one
# customers are served; override with SPEC_BASE for a comparison
# capture. Record every capture in openapi/SOURCE.
SPEC_BASE ?= https://api.pgedge.com

vendor-spec:
	go run ./cmd/vendorspec -base $(SPEC_BASE) -out openapi

# Fetch and report drift without rewriting. Not part of CI: both
# targets hit the network.
vendor-spec-check:
	go run ./cmd/vendorspec -base $(SPEC_BASE) -out openapi -check

# Every platform .goreleaser.yaml releases. The windows pair was
# missing here, so a cross-compile break on the platform two release
# assets are built for reached the tag unchecked.
BUILD_TARGETS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 \
	windows/amd64 windows/arm64

# One target, so CI's matrix can run the six in parallel and both it
# and build-all issue the same command. The .exe suffix is derived
# here rather than by each caller: a caller deriving it in shell gets
# `[ x = windows ] && ext=.exe`, which under GitHub's `bash -e` exits
# non-zero and fails the step on every leg that is not windows.
build-one:
	GOOS=$(GOOS) GOARCH=$(GOARCH) go build $(LDFLAGS) -o \
		dist/$(BINARY)_$(GOOS)_$(GOARCH)$(if $(filter windows,$(GOOS)),.exe) \
		./cmd/pgedge

build-all:
	@for t in $(BUILD_TARGETS); do \
		$(MAKE) --no-print-directory build-one \
			GOOS="$${t%/*}" GOARCH="$${t#*/}" || exit 1; \
	done

# The operating systems .goreleaser.yaml builds for. Both licence
# files are generated once per system and unioned, because the
# dependency set differs: cobra pulls in a Windows-only module and
# x/sys links a different package on each.
RELEASE_GOOS := linux darwin windows

# go-licenses at a pinned version, installed into a temp GOBIN for the
# host and then run with GOOS set per target, since `go run` under a
# foreign GOOS would build the tool itself for that system. Stderr is
# left alone: it warns about assembly in x/crypto and x/sys, and a real
# failure would otherwise be a silent empty file.
notice:
	tmp="$$(mktemp -d)" && \
		trap 'rm -rf "$$tmp" NOTICE.txt.tmp' EXIT && \
		GOBIN="$$tmp/bin" go install github.com/google/go-licenses@v1.6.0 && \
		for goos in $(RELEASE_GOOS); do \
			GOOS=$$goos "$$tmp/bin/go-licenses" report \
				--ignore github.com/pgEdge/pgedge-cli ./... \
				>> "$$tmp/report" || exit 1; \
		done && \
		LC_ALL=C sort -u "$$tmp/report" > NOTICE.txt.tmp && \
		mv NOTICE.txt.tmp NOTICE.txt

# The licence texts themselves. NOTICE.txt is an inventory with links;
# the MIT and BSD licences and Apache-2.0 4(a) and 4(d) require the
# notice text to travel with the binary, so this file ships in every
# archive. go-licenses save copies each package's licence and NOTICE
# file into a read-only tree per GOOS; the script unions the trees.
third-party-licenses:
	tmp="$$(mktemp -d)" && \
		trap 'chmod -R u+w "$$tmp"; rm -rf "$$tmp"' EXIT && \
		GOBIN="$$tmp/bin" go install github.com/google/go-licenses@v1.6.0 && \
		for goos in $(RELEASE_GOOS); do \
			GOOS=$$goos "$$tmp/bin/go-licenses" save \
				--ignore github.com/pgEdge/pgedge-cli \
				--save_path "$$tmp/save-$$goos" ./... || exit 1; \
		done && \
		sh scripts/third-party-licenses.sh THIRD_PARTY_LICENSES.txt \
			$(foreach g,$(RELEASE_GOOS),"$$tmp/save-$(g)")
