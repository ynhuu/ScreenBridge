SHELL := /bin/sh

PACKAGE := dist/ScreenBridge-v1.0.0.zip
SERVER := bin/screenbridge

.PHONY: all build package test web-test native-test vet check clean distclean help

all: package

## Build the Android arm64 single-binary module (requires the target device via ADB).
build:
	./tools/build.sh

## Build and verify the installable Magisk ZIP.
package: build
	@test -s "$(PACKAGE)"
	unzip -t "$(PACKAGE)"

## Run Go unit tests on the host.
test:
	go test ./...

## Run Go static analysis on the host.
vet:
	go vet ./...

## Run browser-setting unit tests and JavaScript syntax checks (requires Node.js).
web-test:
	node --test tests/web-settings.test.cjs
	node --check app/web/settings.js
	node -e 'const fs = require("node:fs"), vm = require("node:vm"); const html = fs.readFileSync("app/web/index.html", "utf8"); new vm.Script(html.match(/<script>([\s\S]*?)<\/script>/)[1]);'

## Run portable native activity-detection tests (requires a host C++ compiler).
native-test:
	mkdir -p native/_build
	$(CXX) -std=c++17 -I native tests/frame_activity_test.cpp -o native/_build/frame_activity_test
	native/_build/frame_activity_test

## Run Go, web, and native tests, vet, and shell syntax checks.
check: test web-test native-test vet
	sh -n module/service.sh module/customize.sh module/uninstall.sh tools/build.sh

## Remove the generated executable and package, preserving downloaded NDK build caches.
clean:
	rm -f "$(SERVER)" "$(PACKAGE)"

## Remove build outputs and downloaded/generated native build inputs.
distclean: clean
	rm -rf native/_build native/generated native/libs

## Show available targets.
help:
	@sed -n 's/^## //p; s/^\([a-zA-Z0-9_-]*\):.*/  \1/p' Makefile
