SHELL=bash
.ONESHELL:

# Go parameters
GOCMD=GO111MODULE=on go
GOBUILD=$(GOCMD) build
GOINSTALL=$(GOCMD) install
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod
GOFMT=$(GOCMD) fmt
GODOC=godoc

MAKEFILE_PATH := $(abspath $(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
BIN_DIR := "${MAKEFILE_PATH}/bin"

GOLANGCI_VERSION=2.6.0

.PHONY: all test coverage
all: test

checkfmt:
	@echo 'Checking gofmt';\
 	bash -c "diff -u <(echo -n) <(gofmt -d .)";\
	EXIT_CODE=$$?;\
	if [ "$$EXIT_CODE"  -ne 0 ]; then \
		echo '$@: Go files must be formatted with gofmt'; \
	fi && \
	exit $$EXIT_CODE

lint: .prepare-golangci
	@$(BIN_DIR)/golangci-lint run

get:
	$(GOGET) -v ./...

fmt: .prepare-golangci
	@$(BIN_DIR)/golangci-lint run --fix

test: get
	$(GOTEST) -count=1 ./...

coverage: get test
	$(GOTEST) -count=1 -race -coverprofile=coverage.txt -covermode=atomic .

benchmark: get
	$(GOTEST) -bench=. -benchmem

godoc:
	$(GODOC)

.prepare-bin:
	@[[ -d "$(MAKEFILE_PATH)/bin" ]] || mkdir "$(MAKEFILE_PATH)/bin"

.prepare-golangci: .prepare-bin
	@if ! "${BIN_DIR}/golangci-lint" --version 2>/dev/null | grep '${GOLANGCI_VERSION}' >/dev/null 2>&1 ; then \
		echo "Installing golangci-lint to '${BIN_DIR}'" ; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b bin/ v$(GOLANGCI_VERSION) ; \
	fi
