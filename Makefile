BINARY   := meshcore-tui
MODULE   := github.com/alvarow/meshcore-go-tui
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X $(MODULE)/cmd.Version=$(VERSION) -s -w"

GOOS     ?= $(shell go env GOOS)
GOARCH   ?= $(shell go env GOARCH)

DEBUG_LOG ?= /tmp/meshcore-go-tui.log

.PHONY: all build clean run debug-run lint test install

all: build

build:
	go build $(LDFLAGS) -o $(BINARY) .

run:
	go run . $(ARGS)

debug-run: build
	@echo "Debug log: $(DEBUG_LOG)"
	./$(BINARY) --debug $(ARGS) 2>"$(DEBUG_LOG)"; cat "$(DEBUG_LOG)"

test:
	go test ./...

lint:
	go vet ./...
	# go install golang.org/x/vuln/cmd/govulncheck@latest
	@command -v $$(go env GOPATH)/bin/govulncheck >/dev/null 2>&1 && $$(go env GOPATH)/bin/govulncheck ./... || true

clean:
	rm -f $(BINARY) $(BINARY)-*

install:
	go install $(LDFLAGS) .
	install -d $(DESTDIR)$(PREFIX)/share/man/man1
	install -m 644 meshcore-tui.1 $(DESTDIR)$(PREFIX)/share/man/man1/

# Cross-compile targets
build-linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-linux-amd64 .

build-linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-linux-arm64 .

build-darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-darwin-amd64 .

build-darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BINARY)-darwin-arm64 .

build-windows-amd64:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BINARY)-windows-amd64.exe .

build-all: build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-darwin-arm64 build-windows-amd64
