.PHONY: build test test-race lint clean install docker

BINARY=nstack
VERSION=0.1.0

build:
	go build -ldflags "-s -w -X main.version=$(VERSION)" -o bin/$(BINARY) ./cmd/nstack/

test:
	go test ./... -v -count=1

test-race:
	go test -race ./... -count=1

lint:
	golangci-lint run ./...

clean:
	rm -rf bin/

install: build
	cp bin/$(BINARY) /usr/local/bin/

docker:
	docker build -t nstack:$(VERSION) .

ci: lint test-race build
