BINARY  := derbyandales
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST    := dist

.DEFAULT_GOAL := help

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/## /  /'

## build: compile the binary for this machine
build:
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/$(BINARY) ./cmd/$(BINARY)

## run: build and run against a scratch data directory
run:
	go run ./cmd/$(BINARY) -data ./.devdata -debug

## test: run the full test suite
test:
	go test ./...

## e2e: two whole seasons through the web endpoints, published to a temp site
e2e:
	go test -tags e2e -count=1 -run TestE2E ./internal/web/

## race: run the suite under the race detector
race:
	go test -race ./...

## cover: report test coverage per package
cover:
	go test -cover ./...

## check: everything CI would run
check: fmt-check vet test

## fmt: format all Go source
fmt:
	gofmt -w .

## fmt-check: fail if anything is unformatted
fmt-check:
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

## vet: run go vet, including the end-to-end tests so they cannot rot unseen
vet:
	go vet ./...
	go vet -tags e2e ./internal/web/

## app: build DerbyAndAles.app (universal binary)
app:
	./packaging/make-app.sh "$(VERSION)"

## clean: remove build output and the scratch data directory
clean:
	rm -rf $(DIST) .devdata

.PHONY: help build run test e2e race cover check fmt fmt-check vet app clean
