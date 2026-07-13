.PHONY: clean
clean:
	go clean -testcache

# verify reuses the lint targets (plus the fixers) so local verification and
# the CI lint gates can never drift apart.
.PHONY: verify
verify: clean fmt golangci-lint-lint gosec vet modernize-fix
	go test -count=1 -race ./...

.PHONY: fmt
fmt:
	gofumpt -w .

.PHONY: test
test: clean
	go test -v -race ./...

.PHONY: test-win
test-win: clean
	go test -v ./...

.PHONY: bench
bench: clean
	go test -v -run=^\$$ -bench=. ./... -benchmem

.PHONY: lint
lint: _lint

LINT_ARCH := $(shell uname -m)
LINT_OS := $(shell uname)
LINT_OS_LOWER := $(shell echo $(LINT_OS) | tr '[:upper:]' '[:lower:]')
LINT_ROOT := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

LINTERS :=
FIXERS :=

# Tool versions. Everything is pinned (including the install script ref
# below) so lint-gate behavior only changes with a repo diff.
GOLANGCI_LINT_VERSION ?= v2.12.2
GOSEC_VERSION ?= v2.27.1
MODERNIZE_VERSION ?= v0.48.0
OSV_SCANNER_VERSION ?= v2.4.0

GOLANGCI_LINT_CONFIG := $(LINT_ROOT)/.golangci.yml
GOLANGCI_LINT_BIN := $(LINT_ROOT)/out/linters/golangci-lint-$(GOLANGCI_LINT_VERSION)-$(LINT_ARCH)
$(GOLANGCI_LINT_BIN):
	mkdir -p $(LINT_ROOT)/out/linters
	rm -rf $(LINT_ROOT)/out/linters/golangci-lint-*
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/$(GOLANGCI_LINT_VERSION)/install.sh | sh -s -- -b $(LINT_ROOT)/out/linters $(GOLANGCI_LINT_VERSION)
	mv $(LINT_ROOT)/out/linters/golangci-lint $@

LINTERS += golangci-lint-lint
.PHONY: golangci-lint-lint
golangci-lint-lint: $(GOLANGCI_LINT_BIN)
	"$(GOLANGCI_LINT_BIN)" run -c "$(GOLANGCI_LINT_CONFIG)"

FIXERS += golangci-lint-fix
.PHONY: golangci-lint-fix
golangci-lint-fix: $(GOLANGCI_LINT_BIN)
	"$(GOLANGCI_LINT_BIN)" run -c "$(GOLANGCI_LINT_CONFIG)" --fix

.PHONY: install-gosec
install-gosec:
	go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)

LINTERS += gosec
.PHONY: gosec
gosec: install-gosec
	gosec -quiet -conf .gosec.json ./...

LINTERS += vet
.PHONY: vet
vet:
	go vet -c=3 ./...

# The gate runs without -fix: the analyzer exits non-zero on findings, so CI
# actually fails when modernization drift appears. Use modernize-fix locally.
LINTERS += modernize
.PHONY: modernize
modernize:
	go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@$(MODERNIZE_VERSION) -test ./...

FIXERS += modernize-fix
.PHONY: modernize-fix
modernize-fix:
	go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@$(MODERNIZE_VERSION) -fix -test ./...

.PHONY: install-osv
install-osv:
	go install github.com/google/osv-scanner/v2/cmd/osv-scanner@$(OSV_SCANNER_VERSION)

LINTERS += osv
.PHONY: osv
osv: install-osv
	osv-scanner scan source -r --format json ./

.PHONY: _lint $(LINTERS)
_lint: $(LINTERS)

.PHONY: fix
fix: $(FIXERS)

# Per-fuzzer budget; keep in sync with the fuzz job in .github/workflows/ci.yml.
FUZZ_TIME := 10s
PKG_DIR := ./pkg/files
# The fuzzer list is derived from the test binary, so it can never drift from
# the fuzz targets defined in the source.
FUZZERS = $(shell go test -list '^Fuzz' -run '^$$' $(PKG_DIR) | grep '^Fuzz')
.PHONY: fuzz
fuzz:
	$(foreach f,$(FUZZERS),go test -parallel=1 -fuzz='^$(f)$$' -run='^$(f)$$' -fuzztime=$(FUZZ_TIME) $(PKG_DIR) || exit 1;)
