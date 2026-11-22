VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
PACKAGE := github.com/theriseunion-edge/edge-cli
BIN := bin/ec
DIST := dist
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64

.PHONY: build test vet fmt dist e2e clean

build:
	go build -trimpath -ldflags "-s -w -X $(PACKAGE)/internal/cli.Version=$(VERSION)" -o $(BIN) ./cmd/ec

test:
	go test ./...

vet:
	go vet ./...

fmt:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "$$out"; exit 1; fi

dist:
	rm -rf $(DIST) && mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags "-s -w -X $(PACKAGE)/internal/cli.Version=$(VERSION)" -o $(DIST)/ec ./cmd/ec || exit 1; \
		tar -czf $(DIST)/edge-cli-$(VERSION)-$$os-$$arch.tar.gz -C $(DIST) ec || exit 1; \
		rm -f $(DIST)/ec; \
	done
	@printf '{"name":"edge-cli","version":"%s"}\n' "$(VERSION)" > $(DIST)/component.json
	cd $(DIST) && sha256sum *.tar.gz > SHA256SUMS

e2e: build
	./scripts/e2e.sh

clean:
	rm -rf $(BIN) $(DIST)
