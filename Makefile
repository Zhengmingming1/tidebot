.PHONY: all build test vet measure mock tidy clean ci

export GOTOOLCHAIN ?= local

PREFIX ?= .
BINDIR ?= $(PREFIX)/bin
MOCK := $(CURDIR)/testdata/mock-claude/claude

all: build

tidy:
	go mod tidy

build: tidy
	mkdir -p $(BINDIR)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINDIR)/agentd ./cmd/agentd
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BINDIR)/agentctl ./cmd/agentctl

vet:
	go vet ./...

test: mock
	go test ./...

mock:
	chmod +x $(MOCK)

measure: build
	./scripts/measure.sh

ci: vet test

clean:
	rm -rf bin measure-out coverage.out
