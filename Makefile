# CD-Switcher build targets.
#
# Note: the UI now uses Fyne (OpenGL + cgo), so every binary must be built ON
# its target OS with a C toolchain — there is no CGO-free path anymore. Cross-
# compiling to Windows requires fyne-cross (Docker) or a mingw toolchain; see
# the windows target.

BIN := cd-switcher

# On macOS the Go toolchain appends -lobjc once per cgo package containing .m
# files (Fyne, glfw, systray, ...), so the Xcode 15+ linker warns about the
# duplicates. Harmless, but noisy — silence it on darwin only.
ifeq ($(shell uname -s),Darwin)
GOFLAGS_LD := -ldflags=-extldflags=-Wl,-no_warn_duplicate_libraries
endif

.PHONY: build run mac-app install uninstall test fmt vet clean windows

build:
	go build $(GOFLAGS_LD) -o bin/$(BIN) .

ifeq ($(shell uname -s),Darwin)
run: mac-app
	open bin/CD-Switcher-dev.app
else
run: build
	./bin/$(BIN)
endif

# Native notifications use the bundle icon; a bare binary uses AppleScript's.
mac-app: build
	sh scripts/package-dev-macos.sh

# Linux: installs for the current user (~/.local/bin, app-menu entry, icon).
install: build
	go run ./cmd/genicon bin/icon.png
	sh scripts/install-linux.sh bin/$(BIN) bin/icon.png

uninstall:
	sh scripts/install-linux.sh --uninstall

test:
	go test ./internal/...
ifeq ($(shell uname -s),Darwin)
	sh scripts/test-inbox-notifications.sh
endif

fmt:
	gofmt -w .

vet:
	go vet ./...

# Windows build. Fyne needs cgo + OpenGL, so a plain `go build` cross-compile no
# longer works. Use fyne-cross (Docker): `go run fyne.io/fyne-cross windows
# -arch=amd64`, or build natively on Windows with a C/mingw toolchain installed.
windows:
	@echo "Cross-compiling Fyne needs fyne-cross or a Windows host with cgo." >&2
	@echo "Run: go run fyne.io/fyne-cross windows -arch=amd64" >&2
	@exit 1

clean:
	rm -rf bin
