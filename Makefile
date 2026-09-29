.PHONY: build test check

build:
	CGO_ENABLED=0 go build -o bin/chrome-connector ./cmd/chrome-connector
	cd extension && npm run build

test:
	CGO_ENABLED=0 go test ./...
	cd extension && npm test

check:
	CGO_ENABLED=0 go vet ./...
	cd extension && npm run check
