GO ?= go
BINDIR ?= bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build build-server build-sparkctl clean release test FORCE

build: build-server build-sparkctl

build-server: $(BINDIR)/spark-server

build-sparkctl: $(BINDIR)/sparkctl

$(BINDIR)/spark-server: FORCE
	mkdir -p $(BINDIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $@ ./cmd/spark-server

$(BINDIR)/sparkctl: FORCE
	mkdir -p $(BINDIR)
	CGO_ENABLED=0 $(GO) build -trimpath -ldflags="-s -w" -o $@ ./cmd/sparkctl

release:
	VERSION="$(VERSION)" ./scripts/build-release.sh

test:
	$(GO) test ./...

clean:
	rm -rf $(BINDIR) dist

FORCE:
