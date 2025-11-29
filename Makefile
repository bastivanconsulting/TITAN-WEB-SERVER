
# Makefile for Titan Web Server

BINARY_NAME=titan-web-server
VERSION=$(shell cat VERSION)
GO_FILES=$(shell find . -name "*.go")

.PHONY: all build clean test package

all: build

build:
	go build -o $(BINARY_NAME) ./cmd/server

build-windows:
	GOOS=windows go build -o $(BINARY_NAME).exe ./cmd/server

test:
	go test -v ./...

clean:
	rm -f $(BINARY_NAME) $(BINARY_NAME).exe
	rm -rf packages/

# Requires nfpm installed: go install github.com/goreleaser/nfpm/v2/cmd/nfpm@latest
package: build build-windows
	mkdir -p packages
	VERSION=$(VERSION) nfpm package --config nfpm.yaml --packager deb --target packages/
	VERSION=$(VERSION) nfpm package --config nfpm.yaml --packager rpm --target packages/
