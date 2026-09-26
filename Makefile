.PHONY: check lint test run build

check: lint test

lint:
	gofmt -l . | grep . && exit 1 || true
	go vet ./...

test:
	go test ./...

run:
	DATA_DIR=./data MASTER_KEY=dev-only-not-secret go run ./cmd/andon

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/andon ./cmd/andon
