VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X flock/internal/cli.Version=$(VERSION)

.PHONY: build build-all test lint notices i18n i18n-status i18n-missing app dmg windows windows-test

build:
	go build -ldflags "$(LDFLAGS)" -o bin/flock ./cmd/flock

build-all:
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/flock-darwin-arm64 ./cmd/flock
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/flock-windows-amd64.exe ./cmd/flock

test:
	go test -race ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l .)"

# THIRD_PARTY_NOTICES, after changing dependencies (CI fails while it's out of date).
notices:
	go run ./tools/notices

# The apps' translation files, from lang/ and the keys the apps use (CI fails while they're out of
# date). lang/README.md describes how translations work.
i18n:
	go run ./tools/i18n

# How much of each language is translated.
i18n-status:
	go run ./tools/i18n -status

# The texts a language lacks, with their English and notes: make i18n-missing lang=de
i18n-missing:
	go run ./tools/i18n -missing $(lang)

# build/macos/Flock.app (with the flock CLI inside) and build/macos/Flock.dmg.
app:
	VERSION=$(VERSION) macos/build-app.sh

dmg: app

# flock.exe, flock-helper.exe and flockd.exe for Windows (copy them to the Windows machine and run `flock.exe install`).
windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/windows-amd64/flock.exe ./cmd/flock
	GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/windows-amd64/flock-helper.exe ./cmd/flock-helper
	GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -H windowsgui" -o dist/windows-amd64/flockd.exe ./cmd/flockd

# The tray app's models and API client (windows/Flock.Kit) run on any OS; the app itself is built on Windows (windows/build.ps1).
windows-test:
	dotnet test windows/Flock.Kit.Tests
