GOEXP := GOEXPERIMENT=greenteagc

.PHONY: clean
clean:
	go clean -testcache

.PHONY: test
test: clean
	$(GOEXP) go test -v -race ./...

.PHONY: bench
bench: clean
	$(GOEXP) go test -v -run=^\$$ -bench=. ./... -benchmem

.PHONY: lint
lint: _lint

LINT_ARCH := $(shell uname -m)
LINT_OS := $(shell uname)
LINT_OS_LOWER := $(shell echo $(LINT_OS) | tr '[:upper:]' '[:lower:]')
LINT_ROOT := $(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))

LINTERS :=
FIXERS :=

GOLANGCI_LINT_CONFIG := $(LINT_ROOT)/.golangci.yml
GOLANGCI_LINT_VERSION ?= v2.7.2
GOLANGCI_LINT_BIN := $(LINT_ROOT)/out/linters/golangci-lint-$(GOLANGCI_LINT_VERSION)-$(LINT_ARCH)
$(GOLANGCI_LINT_BIN):
	mkdir -p $(LINT_ROOT)/out/linters
	rm -rf $(LINT_ROOT)/out/linters/golangci-lint-*
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(LINT_ROOT)/out/linters $(GOLANGCI_LINT_VERSION)
	mv $(LINT_ROOT)/out/linters/golangci-lint $@

LINTERS += golangci-lint-lint
golangci-lint-lint: $(GOLANGCI_LINT_BIN)
	find . -maxdepth 1 -name go.mod -print0 | xargs -0 -L1 -I{} /bin/sh -c '"$(GOLANGCI_LINT_BIN)" run -c "$(GOLANGCI_LINT_CONFIG)"' \;

FIXERS += golangci-lint-fix
golangci-lint-fix: $(GOLANGCI_LINT_BIN)
	find . -maxdepth 1 -name go.mod -execdir "$(GOLANGCI_LINT_BIN)" run -c "$(GOLANGCI_LINT_CONFIG)" --fix \;

install-gosec:
	go install github.com/securego/gosec/v2/cmd/gosec@latest

LINTERS += gosec
.PHONY: gosec
gosec: install-gosec
	$$GOPATH/bin/gosec -conf .gosec.json ./...

LINTERS += vet
.PHONY: vet
vet:
	go vet -c=3 -json ./...

LINTERS += modernize
.PHONY: modernize
modernize:
	go run golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize@latest -fix -test ./...

.PHONY: install-osv
install-osv:
	go install github.com/google/osv-scanner/v2/cmd/osv-scanner@v2

LINTERS += osv
.PHONY: osv
osv: install-osv
	$$GOPATH/bin/osv-scanner scan source -r --format json ./

.PHONY: _lint $(LINTERS)
_lint: $(LINTERS)

# use a smaller value for local runs since CI runs each fuzzer for 60 seconds
FUZZ_TIME := 10s
.PHONY: fuzz
fuzz:
	$(GOEXP) go test --fuzz=FuzzDiff -run=FuzzDiff -fuzztime=$(FUZZ_TIME) ./pkg/diff
	$(GOEXP) go test --fuzz=FuzzEqual -run=FuzzEqual -fuzztime=$(FUZZ_TIME) ./pkg/diff
	$(GOEXP) go test --fuzz=FuzzSoname -run=FuzzSoname -fuzztime=$(FUZZ_TIME) ./pkg/diff
	$(GOEXP) go test --fuzz=FuzzScript -run=FuzzScript -fuzztime=$(FUZZ_TIME) ./pkg/diff
	$(GOEXP) go test --fuzz=FuzzSuffix -run=FuzzSuffix -fuzztime=$(FUZZ_TIME) ./pkg/diff
	$(GOEXP) go test --fuzz=FuzzEmbedded -run=FuzzEmbedded -fuzztime=$(FUZZ_TIME) ./pkg/diff
