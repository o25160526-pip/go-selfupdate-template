APP        ?= app
MODULE     := github.com/o25160526-pip/go-selfupdate-template
PKG_VER    := $(MODULE)/internal/version
PKG_UPD    := $(MODULE)/internal/updater

# Version is always generated in UTC. See docs/VERSIONING.md.
VERSION    ?= $(shell go run ./tools/genversion -check-tags=false -format=display)
SEMVER     ?= $(shell go run ./tools/genversion -check-tags=false -format=semver)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
CHANNEL    ?= dev
PUBKEY     ?=
PUBKEY_NEXT ?=

LDFLAGS = -s -w \
	-X '$(PKG_VER).Current=$(VERSION)' \
	-X '$(PKG_VER).Commit=$(COMMIT)' \
	-X '$(PKG_VER).BuildDate=$(BUILD_DATE)' \
	-X '$(PKG_VER).Channel=$(CHANNEL)' \
	-X '$(PKG_UPD).PublicKeyPrimary=$(PUBKEY)' \
	-X '$(PKG_UPD).PublicKeyNext=$(PUBKEY_NEXT)'

# os/arch targets. 6 of them, not 3: arm64 is not optional anymore.
TARGETS = linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: all build test vet lint dist clean keygen sign run menu new-feature tidy

all: vet test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/app

test:
	go test ./... -race -count=1

vet:
	go vet ./...

lint: vet
	gofmt -l -d .

tidy:
	go mod tidy

dist:
	@mkdir -p dist
	@for t in $(TARGETS); do \
		os=$${t%/*}; arch=$${t#*/}; ext=""; \
		if [ "$$os" = "windows" ]; then ext=".exe"; fi; \
		echo "  -> $(APP)_$${os}_$${arch}$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o dist/$(APP)_$${os}_$${arch}$$ext ./cmd/app || exit 1; \
	done
	@cd dist && sha256sum $(APP)_* > checksums.txt 2>/dev/null || shasum -a 256 $(APP)_* > checksums.txt
	@echo "dist ready (version $(VERSION))"

keygen:
	go run ./tools/keygen

sign:
	go run ./tools/sign -in dist/checksums.txt -out dist/checksums.txt.sig

run: build
	./bin/$(APP) $(ARGS)

menu: build
	./bin/$(APP) menu

# Scaffold a new feature package. This is what makes this repo a template.
new-feature:
	@test -n "$(NAME)" || (echo "usage: make new-feature NAME=myfeature"; exit 1)
	go run ./tools/newfeature -name $(NAME)

clean:
	rm -rf bin dist
