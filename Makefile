BINARY   := meshcore-tui
MODULE   := github.com/alvarow/meshcore-go-tui
VERSION  := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS  := -ldflags "-X $(MODULE)/cmd.Version=$(VERSION) -s -w"

GOOS     ?= $(shell go env GOOS)
GOARCH   ?= $(shell go env GOARCH)

.PHONY: all build clean run lint test install

all: build

build:
	go build $(LDFLAGS) -o $(BINARY) .

run:
	go run . $(ARGS)

test:
	go test ./...

lint:
	go vet ./...

clean:
	rm -f $(BINARY) $(BINARY)-*

install:
	go install $(LDFLAGS) .

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
