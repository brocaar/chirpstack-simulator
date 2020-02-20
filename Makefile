.PHONY: build clean
VERSION := $(shell git describe --always |sed -e "s/^v//")

build:
	@echo "Compiling source"
	@mkdir -p build
	go build $(GO_EXTRA_BUILD_ARGS) -ldflags "-s -w -X main.version=$(VERSION)" -o build/chirpstack-simulator cmd/chirpstack-simulator/main.go

clean:
	@echo "Cleaning up workspace"
	@rm -rf build
	@rm -rf dist
	@rm -rf docs/public
