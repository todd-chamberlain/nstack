.PHONY: build test clean install

BINARY=nstack
VERSION=0.1.0

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/nstack/

test:
	go test ./... -v -count=1

clean:
	rm -rf bin/

install: build
	cp bin/$(BINARY) /usr/local/bin/
