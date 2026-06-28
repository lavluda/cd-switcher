# CD-Switcher build targets.
#
# Note: the UI now uses Fyne (OpenGL + cgo), so every binary must be built ON
# its target OS with a C toolchain — there is no CGO-free path anymore. Cross-
# compiling to Windows requires fyne-cross (Docker) or a mingw toolchain; see
# the windows target.

BIN := cd-switcher

.PHONY: build run test fmt vet clean windows

build:
	go build -o bin/$(BIN) .

run: build
	./bin/$(BIN)

test:
	go test ./internal/...

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
