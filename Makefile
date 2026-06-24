# openccu-loom — developer makefile
#
# Phase 0 skeleton: real behaviour is filled in as later phases land.
# Targets print a TODO marker where the backing config is not yet written.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

GO              ?= go
GOFUMPT         ?= gofumpt
GOIMPORTS       ?= goimports
GOLANGCI_LINT   ?= golangci-lint
GORELEASER      ?= goreleaser
GOVULNCHECK     ?= govulncheck
GOLICENSES      ?= go-licenses
GREMLINS        ?= gremlins

export CGO_ENABLED := 0

BIN_DIR   := bin
BIN_NAME  := openccu-loom
BIN       := $(BIN_DIR)/$(BIN_NAME)
MODULE    := github.com/SukramJ/openccu-loom
PKG_BUILD := $(MODULE)/internal/build

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo 0.1.0)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -s -w \
	-X $(PKG_BUILD).Version=$(VERSION) \
	-X $(PKG_BUILD).Commit=$(COMMIT) \
	-X $(PKG_BUILD).BuildDate=$(BUILD_DATE)

GO_BUILD_FLAGS := -trimpath -ldflags="$(LDFLAGS)"

.PHONY: help
help: ## show this help
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: setup
setup: ## install developer tooling and the pre-commit hook
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install golang.org/x/tools/cmd/goimports@latest
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	$(GO) install github.com/pressly/goose/v3/cmd/goose@latest
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) install github.com/google/go-licenses@latest
	$(GO) install github.com/go-gremlins/gremlins/cmd/gremlins@latest
	@if [ -d .git ]; then \
		install -m 0755 script/git/pre-commit .git/hooks/pre-commit 2>/dev/null || \
			echo "note: script/git/pre-commit not present yet (Phase 0)"; \
	fi

UI_DIR := assets/ui
UI_DIST := internal/north/ui/spa_dist

LOGO_SRC      := assets/logo
LOGO_PNG_DIR  := $(LOGO_SRC)/png
LOGO_SPA_DST  := $(UI_DIR)/public
LOGO_HTMX_DST := internal/north/ui/assets/logo
FAVICON_SIZES := 16 32 48 64 192 512

.PHONY: assets-wordmark-paths
assets-wordmark-paths: ## convert wordmark <text> to vector <path> (needs SPA npm deps)
	@if [ ! -d $(UI_DIR)/node_modules/opentype.js ]; then \
		echo "opentype.js not installed — run: cd $(UI_DIR) && npm install --save-dev opentype.js @fontsource/inter"; \
		exit 1; \
	fi
	@node script/wordmark_to_paths.mjs

.PHONY: assets-logo-png
assets-logo-png: ## regenerate PNGs from assets/logo/*.svg (needs rsvg-convert)
	@command -v rsvg-convert >/dev/null 2>&1 || { \
		echo "rsvg-convert not found — install with: brew install librsvg"; exit 1; \
	}
	@mkdir -p $(LOGO_PNG_DIR)
	@for s in $(FAVICON_SIZES); do \
		rsvg-convert --width=$$s --height=$$s $(LOGO_SRC)/favicon.svg \
			-o $(LOGO_PNG_DIR)/favicon-$$s.png; \
	done
	@rsvg-convert --width=180 --height=180 $(LOGO_SRC)/favicon.svg \
		-o $(LOGO_PNG_DIR)/apple-touch-icon-180.png
	@rsvg-convert --width=256 --height=256 $(LOGO_SRC)/mark-loom.svg \
		-o $(LOGO_PNG_DIR)/mark-loom-256.png
	@rsvg-convert --width=512 --height=512 $(LOGO_SRC)/mark-loom.svg \
		-o $(LOGO_PNG_DIR)/mark-loom-512.png
	@rsvg-convert --width=400 $(LOGO_SRC)/wordmark.svg \
		-o $(LOGO_PNG_DIR)/wordmark-400.png
	@rsvg-convert --width=1200 $(LOGO_SRC)/wordmark.svg \
		-o $(LOGO_PNG_DIR)/wordmark-1200.png

.PHONY: assets-sync
assets-sync: assets-logo-png ## mirror assets/logo/ into SPA public/ and HTMX embed dir
	@mkdir -p $(LOGO_SPA_DST) $(LOGO_HTMX_DST)
	@cp $(LOGO_SRC)/favicon.svg \
	    $(LOGO_SRC)/mark-loom.svg \
	    $(LOGO_SRC)/mark-loom-mono.svg \
	    $(LOGO_SRC)/mark-loom-inverse.svg \
	    $(LOGO_SRC)/wordmark.svg \
	    $(LOGO_SRC)/wordmark-mono.svg \
	    $(LOGO_SRC)/wordmark-inverse.svg \
	    $(LOGO_SRC)/manifest.webmanifest \
	    $(LOGO_SPA_DST)/
	@cp $(LOGO_PNG_DIR)/favicon-16.png \
	    $(LOGO_PNG_DIR)/favicon-32.png \
	    $(LOGO_PNG_DIR)/favicon-192.png \
	    $(LOGO_PNG_DIR)/favicon-512.png \
	    $(LOGO_PNG_DIR)/apple-touch-icon-180.png \
	    $(LOGO_PNG_DIR)/mark-loom-512.png \
	    $(LOGO_SPA_DST)/
	@cp $(LOGO_SRC)/favicon.svg \
	    $(LOGO_SRC)/mark-loom.svg \
	    $(LOGO_SRC)/mark-loom-mono.svg \
	    $(LOGO_SRC)/wordmark.svg \
	    $(LOGO_SRC)/wordmark-mono.svg \
	    $(LOGO_SRC)/manifest.webmanifest \
	    $(LOGO_HTMX_DST)/
	@cp $(LOGO_PNG_DIR)/favicon-16.png \
	    $(LOGO_PNG_DIR)/favicon-32.png \
	    $(LOGO_PNG_DIR)/favicon-192.png \
	    $(LOGO_PNG_DIR)/favicon-512.png \
	    $(LOGO_PNG_DIR)/apple-touch-icon-180.png \
	    $(LOGO_HTMX_DST)/
	@echo "synced logo assets → $(LOGO_SPA_DST)/ + $(LOGO_HTMX_DST)/"

.PHONY: ui-install
ui-install: ## install Svelte SPA dependencies (needs npm)
	cd $(UI_DIR) && npm install

.PHONY: ui-dev
ui-dev: ## run the Vite dev server (proxies /api to :8080)
	cd $(UI_DIR) && npm run dev

.PHONY: ui-build
ui-build: ## build the Svelte SPA into internal/north/ui/spa_dist/
	cd $(UI_DIR) && npm run build
	@touch $(UI_DIST)/.gitkeep

.PHONY: ui-check
ui-check: ## type-check the Svelte SPA
	cd $(UI_DIR) && npm run check

.PHONY: ui-types
ui-types: ## regenerate src/lib/api/types.generated.ts from assets/openapi.yaml
	cd $(UI_DIR) && npm run gen:types

.PHONY: build
build: ## build the daemon binary into ./bin/ (embeds current spa_dist/)
	mkdir -p $(BIN_DIR)
	$(GO) build $(GO_BUILD_FLAGS) -o $(BIN) ./cmd/openccu-loom

.PHONY: dist
dist: ui-build build ## full build: compile SPA first, then daemon

.PHONY: build-all
build-all: ## build all binaries in ./cmd/* into ./bin/
	mkdir -p $(BIN_DIR)
	@for cmd in $$(ls cmd 2>/dev/null); do \
		echo "-> build $$cmd"; \
		$(GO) build $(GO_BUILD_FLAGS) -o $(BIN_DIR)/$$cmd ./cmd/$$cmd; \
	done

.PHONY: test
test: ## run unit + contract tests
	$(GO) test ./...

.PHONY: race
race: ## run unit + contract tests with -race + -count=1 (CGO=1 — test-only, prod build stays CGO=0)
	CGO_ENABLED=1 $(GO) test -race -count=1 ./...

.PHONY: contract
contract: ## run contract tests only
	@if [ -d tests/contract ]; then \
		$(GO) test ./tests/contract/...; \
	else \
		echo "contract tests not implemented yet (Phase 2+)"; \
	fi

.PHONY: wire-snapshots
wire-snapshots: ## regenerate Custom-DP wire snapshots (golden JSON baselines for setter wire calls)
	$(GO) test -tags=snapshot_gen ./tests/contract/wire_snapshots/

.PHONY: wire-reference
wire-reference: ## regenerate aiohomematic reference wire snapshots (requires aiohomematic on PATH or in sibling venv)
	python3 script/aiohomematic_wire_snapshots.py

.PHONY: wire-compare
wire-compare: ## compare Go wire calls against aiohomematic reference (fails for every known drift)
	$(GO) test -tags=wire_reference ./tests/contract/wire_snapshots/ -run TestReferenceCompare -v

.PHONY: scenarios
scenarios: ## run behavior scenarios (docs/parity/matter/scenarios/*.json against the bridge harness)
	$(GO) test ./internal/north/matter/bridge/ -run TestScenarios -count=1 -race -timeout=60s

.PHONY: scenarios-regen-sidecars
scenarios-regen-sidecars: ## regenerate matter.js-canonical reference sidecars for every scenario (needs ../matter.js npm-installed)
	node docs/parity/matter/scenarios/_record.ts

.PHONY: integration
integration: ## run godevccu + Mosquitto integration tests (slow; Mosquitto needs Docker)
	@if [ -d tests/integration ]; then \
		$(GO) test -tags=integration ./tests/integration/...; \
	else \
		echo "integration tests not implemented yet (Phase 3+)"; \
	fi

.PHONY: e2e
e2e: build-all ## run black-box E2E tests against ./bin/openccu-loom + ./bin/hmcli (see docs/e2e-testplan.md)
	@if [ -d tests/e2e ]; then \
		$(GO) test -tags=e2e -timeout=180s ./tests/e2e/...; \
	else \
		echo "e2e tests not implemented yet"; \
	fi

.PHONY: e2e-dist
e2e-dist: ui-build build-all ## build SPA + binaries, then run E2E (CI uses this target)
	$(GO) test -tags=e2e -timeout=180s ./tests/e2e/...

.PHONY: chiptool-test
chiptool-test: build ## run the chip-tool capability suite against ./bin/openccu-loom + a local chip-tool (skip when chip-tool is not installed)
	@if [ -d tests/chiptool ]; then \
		$(GO) test -tags=chiptool -timeout=600s -v ./tests/chiptool/...; \
	else \
		echo "chiptool tests not present"; \
	fi

MATTER_SMOKE_COMPOSE := compose/matter-smoke.yml
MATTER_SMOKE_LOG     := tmp/matter-smoke.log

.PHONY: matter-smoke
matter-smoke: ## run chip-tool PASE smoke against the Matter bridge (Linux host only; ~2.5 GiB image pull on first run)
	@if [ "$$(uname -s)" != "Linux" ]; then \
		echo "matter-smoke requires a Linux host (Docker Desktop does not bridge multicast)."; \
		echo "See docs/contributor/matter-smoke.md for VM/CI alternatives."; \
		exit 1; \
	fi
	@mkdir -p tmp
	@echo "→ bringing up openccu-loom + chip-tool"
	docker compose -f $(MATTER_SMOKE_COMPOSE) up -d --build --wait
	@echo "→ executing chip-tool pairing already-discovered (PASE)"
	@docker compose -f $(MATTER_SMOKE_COMPOSE) exec -T chip-tool \
		chip-tool pairing already-discovered \
			0x1234 20202021 127.0.0.1 5540 \
			--bypass-attestation-verifier true \
			--pase-only true \
		2>&1 | tee $(MATTER_SMOKE_LOG)
	@echo "→ asserting on Pairing Success marker"
	@grep -q "Pairing Success" $(MATTER_SMOKE_LOG) \
		|| { echo "FAIL: chip-tool did not report Pairing Success"; \
		     docker compose -f $(MATTER_SMOKE_COMPOSE) logs openccu-loom | tail -80; \
		     docker compose -f $(MATTER_SMOKE_COMPOSE) down -v; exit 1; }
	@echo "→ tearing down compose stack"
	docker compose -f $(MATTER_SMOKE_COMPOSE) down -v
	@echo "matter-smoke: PASS (log retained in $(MATTER_SMOKE_LOG))"

.PHONY: matter-smoke-down
matter-smoke-down: ## tear down the matter-smoke compose stack (use after a failed run)
	docker compose -f $(MATTER_SMOKE_COMPOSE) down -v

.PHONY: snapshot-go
snapshot-go: ## dump openccu-loom's model snapshot against godevccu (~80k DPs)
	@$(GO) test -tags=integration -timeout=300s \
		-run TestModelSnapshotDumpAgainstGodevccu ./tests/integration/...
	@echo "→ tests/integration/testdata/model_snapshot_openccu-loom.json"

.PHONY: snapshot-py
snapshot-py: ## dump aiohomematic's model snapshot against pydevccu (~8k DPs)
	@python3 script/aiohomematic_snapshot.py
	@echo "→ tests/integration/testdata/model_snapshot_aiohomematic.json"

.PHONY: snapshot-diff
snapshot-diff: ## compare both stack snapshots; exit 0 = full intersection parity
	@if [ ! -f tests/integration/testdata/model_snapshot_openccu-loom.json ]; then \
		echo "missing go snapshot — run 'make snapshot-go' first"; exit 2; \
	fi
	@if [ ! -f tests/integration/testdata/model_snapshot_aiohomematic.json ]; then \
		echo "missing py snapshot — run 'make snapshot-py' first"; exit 2; \
	fi
	@python3 script/model_snapshot_diff.py | python3 script/model_snapshot_drift_check.py

.PHONY: snapshot
snapshot: snapshot-go snapshot-py snapshot-diff ## full snapshot-verification pipeline (datasource diff + both snapshots + diff)
	@echo "snapshot verification complete"

.PHONY: datasource-diff
datasource-diff: ## verify pydevccu and godevccu carry the identical wire data (399 devices × 12 attrs)
	@python3 script/datasource_diff.py >/dev/null && echo "datasource layer: 0 drift" || (echo "datasource drift detected" && exit 1)

.PHONY: routing-key-parity
routing-key-parity: ## verify aiohomematic == the Go-pinned routing-key golden fixtures (closes the manual-copy drift gap)
	@python3 script/routing_key_parity.py

.PHONY: bench
bench: ## run benchmarks (requires -tags=bench)
	@if [ -d tests/bench ]; then \
		$(GO) test -tags=bench -bench=. -benchmem -benchtime=1s ./tests/bench/...; \
	else \
		echo "benchmarks not implemented yet (Phase 10)"; \
	fi

MUTATION_PKGS ?= ./internal/parameter/ ./internal/payload/ ./internal/routingkey/ ./pkg/hmtypes/ ./pkg/hmenum/

.PHONY: mutation
mutation: ## run mutation testing (gremlins) on core packages — slow; report-only
	@for p in $(MUTATION_PKGS); do \
		echo "-> gremlins unleash $$p"; \
		$(GREMLINS) unleash "$$p" || true; \
	done

# Fuzz smoke budget. Iteration-based (`<n>x`) on purpose: a wall-clock
# budget (`5s`) makes go's fuzzing coordinator cancel mid-execution on a
# CPU-starved CI runner and report a spurious "context deadline exceeded"
# failure. A fixed execution count is immune to runner load — it just takes
# a little longer — while still replaying the full seed corpus and exploring
# new inputs. Override for a deeper nightly run, e.g. FUZZTIME=2000000x.
FUZZTIME     ?= 100000x
FUZZ_TIMEOUT ?= 120s

.PHONY: fuzz
fuzz: ## run each fuzz target for $(FUZZTIME) executions as a smoke test
	@for pkg in $$($(GO) list ./internal/client/transport/xmlrpc/... ./internal/client/transport/binrpc/... ./internal/client/transport/jsonrpc/... ./internal/north/matter/im/... ./internal/north/matter/tlv/...); do \
		for fn in $$($(GO) test -list 'Fuzz.*' $$pkg 2>/dev/null | grep '^Fuzz'); do \
			echo "-> fuzz $$pkg :: $$fn ($(FUZZTIME))"; \
			$(GO) test $$pkg -fuzz=^$${fn}$$ -fuzztime=$(FUZZTIME) -timeout=$(FUZZ_TIMEOUT) -run=^$$ || exit 1; \
		done; \
	done

COVERAGE_OUT       ?= coverage.out
COVERAGE_THRESHOLD ?= 88

.PHONY: coverage
coverage: ## run unit + contract + integration tests with coverage profile -> $(COVERAGE_OUT) (CGO=1, atomic mode)
	# -coverpkg=./... attributes coverage across package boundaries, so a
	# package exercised mainly through another package's (or the integration
	# suite's) tests is credited. Without it the per-package tier gate only
	# sees each package's self-coverage and understates the real numbers.
	CGO_ENABLED=1 $(GO) test -tags=integration -covermode=atomic -coverpkg=./... -coverprofile=$(COVERAGE_OUT) ./...
	@$(GO) tool cover -func=$(COVERAGE_OUT) | tail -1

# Note: `-race` is intentionally off when combined with `-tags=integration`
# because the daemon's reload_test.go has pre-existing race conditions that
# surface only under stress from the wider integration test surface. Use
# `make coverage-unit` (which still runs -race) for race-detector coverage
# on the unit+contract subset, and `make race` for the full -race sweep.

.PHONY: coverage-unit
coverage-unit: ## same as coverage but unit + contract only (no integration tag) — useful in lightweight CI; runs with -race
	CGO_ENABLED=1 $(GO) test -race -covermode=atomic -coverprofile=$(COVERAGE_OUT) ./...
	@$(GO) tool cover -func=$(COVERAGE_OUT) | tail -1

.PHONY: coverage-html
coverage-html: coverage ## render HTML report from $(COVERAGE_OUT)
	$(GO) tool cover -html=$(COVERAGE_OUT) -o coverage.html
	@echo "wrote coverage.html"

.PHONY: coverage-check
coverage-check: ## fail when total coverage drops below $(COVERAGE_THRESHOLD)
	@./script/coverage_threshold.sh $(COVERAGE_THRESHOLD) $(COVERAGE_OUT)

.PHONY: coverage-check-per-package
coverage-check-per-package: ## fail when any package drops below its tier threshold
	@./script/coverage_per_package.sh $(COVERAGE_OUT)

.PHONY: lint
lint: ## run golangci-lint
	$(GOLANGCI_LINT) run ./...

.PHONY: vuln
vuln: ## scan dependencies + reachable code for known vulnerabilities (govulncheck)
	$(GOVULNCHECK) ./...

.PHONY: licenses
licenses: ## fail on copyleft dependency licenses (GPL/AGPL/LGPL forbidden; MPL = reciprocal)
	$(GOLICENSES) check ./... --disallowed_types=forbidden,restricted,reciprocal

.PHONY: tidy-check
tidy-check: ## verify go.mod/go.sum are tidy + module checksums (CI gate)
	$(GO) mod verify
	$(GO) mod tidy
	@git diff --exit-code go.mod go.sum || { echo "go.mod/go.sum not tidy — run 'make tidy'"; exit 1; }

.PHONY: secrets
secrets: ## scan the repo for committed secrets (gitleaks; allowlist in .gitleaks.toml)
	$(GO) run github.com/zricethezav/gitleaks/v8@latest detect --no-banner --redact -c .gitleaks.toml

.PHONY: sbom
sbom: ## generate a CycloneDX SBOM for the daemon -> sbom.json
	$(GO) run github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@latest app -json -output sbom.json -main ./cmd/openccu-loom .

.PHONY: fieldalign
fieldalign: ## report sub-optimal struct field alignment (advisory, ~900 hits — not a gate)
	@$(GO) run golang.org/x/tools/go/analysis/passes/fieldalignment/cmd/fieldalignment@latest ./... || true

.PHONY: nilcheck
nilcheck: ## run Uber nilaway nil-flow analysis (advisory, noisy — not a gate)
	@$(GO) run go.uber.org/nilaway/cmd/nilaway@latest ./... || true

.PHONY: deadcode-xtools
deadcode-xtools: ## cross-check dead code via golang.org/x/tools/cmd/deadcode (advisory)
	@$(GO) run golang.org/x/tools/cmd/deadcode@latest ./... || true

.PHONY: openapi-lint
openapi-lint: ## lint assets/openapi.yaml with vacuum (advisory; ruleset in .vacuum.yaml)
	@$(GO) run github.com/daveshanley/vacuum@latest lint -r .vacuum.yaml assets/openapi.yaml || true

.PHONY: deadlock-test
deadlock-test: ## run tests with go-deadlock lock-order detection (syncx-migrated packages)
	CGO_ENABLED=1 $(GO) test -tags deadlock -race -count=1 ./internal/central/coordinators/...

.PHONY: fmt
fmt: ## run gofumpt + goimports
	$(GOFUMPT) -l -w .
	$(GOIMPORTS) -w -local $(MODULE) .

.PHONY: fmt-check
fmt-check: ## verify formatting (CI)
	@diff=$$($(GOFUMPT) -l .); \
	if [ -n "$$diff" ]; then \
		echo "gofumpt: the following files are not formatted:"; \
		echo "$$diff"; \
		exit 1; \
	fi

.PHONY: tidy
tidy: ## sync go.mod / go.sum
	$(GO) mod tidy

.PHONY: reachability
reachability: ## run dead-code reachability analysis → docs/parity/dead-code-inventory.json
	$(GO) run ./script/reachability

.PHONY: qa-pillars
qa-pillars: reachability wire-compare ## run all four structural-parity pillars locally (E2E needs 'make e2e' separately)
	$(GO) test ./tests/contract/wiring_pins/...
	@echo "All local pillars passed. Run 'make e2e' separately (requires build tag + binaries)."

.PHONY: vet
vet: ## go vet
	$(GO) vet ./...

.PHONY: pre-commit
pre-commit: ## run the pre-commit hook against the current working tree (no commit)
	@./script/git/pre-commit

OPENCCU_REPO     ?= ../openccu-data
CCUDATA_EMBED    := internal/ccudata/embedded

.PHONY: update-ccu-data
update-ccu-data: ## refresh embedded archives from an openccu-data checkout
	@if [ ! -d "$(OPENCCU_REPO)/openccu_data/data" ]; then \
		echo "openccu-data repo not found at $(OPENCCU_REPO); set OPENCCU_REPO="; exit 1; \
	fi
	rm -rf $(CCUDATA_EMBED)/profiles $(CCUDATA_EMBED)/translation_custom
	mkdir -p $(CCUDATA_EMBED)/profiles $(CCUDATA_EMBED)/translation_custom
	cp $(OPENCCU_REPO)/openccu_data/data/translation_extract.json.gz $(CCUDATA_EMBED)/
	cp $(OPENCCU_REPO)/openccu_data/data/easymode_extract.json.gz    $(CCUDATA_EMBED)/
	cp $(OPENCCU_REPO)/openccu_data/data/profiles/*.json.gz          $(CCUDATA_EMBED)/profiles/
	cp $(OPENCCU_REPO)/openccu_data/data/profiles/_receiver_type_aliases.json \
	   $(CCUDATA_EMBED)/profiles/
	cp $(OPENCCU_REPO)/openccu_data/data/translation_custom/*.json   $(CCUDATA_EMBED)/translation_custom/
	@echo "-- synced from $(OPENCCU_REPO) --"
	@ls -la $(CCUDATA_EMBED)/

.PHONY: ccudata-drift
ccudata-drift: ## Verify embedded openccu-data matches upstream (set OPENCCU_DATA_PATH= to override)
	@OPENCCU_DATA_PATH="$(OPENCCU_REPO)" ./script/ccudata_drift.sh

.PHONY: refresh-ccudata
refresh-ccudata: update-ccu-data generate ## one-shot: pull openccu-data + regenerate Go profiles
	@echo "-- ccudata refresh complete; run 'make test' to validate --"

MATTERJS_DIR ?= ../matter.js

.PHONY: generate-matter-schema
generate-matter-schema: ## regenerate matter schema snapshot + internal/north/matter/schema/ from matter.js HEAD
	# Step 1: extract the schema from the matter.js checkout (must be built:
	# `cd $(MATTERJS_DIR)/packages/model && npm run build`). The extractor
	# resolves type-inheritance, so type-referencing clusters keep their
	# inherited revision + members. Node >= 23 runs the .ts directly; it is
	# copied into the matter.js tree so the bare `@matter/model` import resolves.
	cp docs/parity/matter/extract-from-matter-js.ts $(MATTERJS_DIR)/.occu-extract.mts
	cd $(MATTERJS_DIR) && node .occu-extract.mts \
		> $(CURDIR)/docs/parity/matter/matter-schema-snapshot.json; \
		rc=$$?; rm -f .occu-extract.mts; exit $$rc
	# Step 2: keep the parity embed copy in sync (see internal/north/matter/parity/parity.go).
	cp docs/parity/matter/matter-schema-snapshot.json internal/north/matter/parity/schema.json
	# Step 3: regenerate the typed Go revision/name maps from the snapshot.
	$(GO) run ./script/generate_matter_schema.go
	@if command -v $(GOFUMPT) >/dev/null 2>&1; then \
		$(GOFUMPT) -w internal/north/matter/schema/; \
	else \
		gofmt -w internal/north/matter/schema/; \
	fi

.PHONY: export-schemas
export-schemas: ## emit assets/schemas/{enums,types}.json for external-language codegen
	$(GO) run ./script/export_schemas.go
	$(MAKE) generate-schema-digest

.PHONY: generate-schema-digest
generate-schema-digest: ## regenerate the contract digest constant from the schema assets
	$(GO) run ./script/generate_schema_digest.go

.PHONY: generate
generate: ## run all code generators
	$(GO) generate ./...
	$(MAKE) export-schemas
	$(MAKE) ui-types
	@if [ -x script/generate_profiles.py ]; then \
		./script/generate_profiles.py; \
	else \
		echo "profile generator not present yet (Phase 5)"; \
	fi

.PHONY: docker
docker: ## build multi-arch Docker images (requires buildx)
	@if [ ! -f Dockerfile ]; then \
		echo "Dockerfile not present yet (Phase 0 follow-up)"; exit 1; \
	fi
	docker buildx build \
		--platform=linux/amd64 \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t openccu-loom:$(VERSION) .

.PHONY: release
release: ## goreleaser snapshot (local test build)
	@if [ ! -f .goreleaser.yaml ]; then \
		echo ".goreleaser.yaml not present yet (Phase 0 follow-up)"; exit 1; \
	fi
	$(GORELEASER) release --snapshot --clean

.PHONY: ccu-addon
ccu-addon: ui-build ## package the CCU/RaspberryMatic add-on tarball into dist/
	script/build_ccu_addon.sh $(VERSION)

.PHONY: ha-addon
ha-addon: ## build the Home Assistant add-on image for the host arch (smoke build)
	script/build_ha_addon.sh $(VERSION)

.PHONY: clean
clean: ## remove build artefacts
	rm -rf $(BIN_DIR) dist/ coverage/

.PHONY: version
version: build ## print the embedded version info
	$(BIN) version
