VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
BIN := bin/ec

.PHONY: build test vet fmt e2e clean

build:
	go build -trimpath -ldflags "-s -w -X github.com/theriseunion-edge/edge-cli/internal/cli.Version=$(VERSION)" -o $(BIN) ./cmd/ec

test:
	go test ./...

vet:
	go vet ./...

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

e2e: build
	./scripts/e2e.sh

clean:
	rm -rf bin
