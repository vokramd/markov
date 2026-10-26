BINARY  := markov
BINDIR  := bin
MODULE  := github.com/jctanner/markov
CMDDIR  := ./cmd/markov

IMAGE   ?= ghcr.io/jctanner/markov
TAG     ?= latest
GOFLAGS ?=

.PHONY: all build build-static clean test lint fmt vet docker-build

all: build

build:
	go build $(GOFLAGS) -o $(BINDIR)/$(BINARY) $(CMDDIR)

# A statically linked build, for containers and sandboxes whose C library differs from the host's
# (an agent in an OpenShell sandbox running `markov validate`).
build-static:
	CGO_ENABLED=0 go build $(GOFLAGS) -o $(BINDIR)/static/$(BINARY) $(CMDDIR)

clean:
	rm -rf $(BINDIR)

test:
	go test ./...

lint: vet fmt

vet:
	go vet ./...

fmt:
	@files="$$(gofmt -l $$(find . -name '*.go' -not -path './third_party/*'))"; test -z "$$files" || (echo "$$files" && exit 1)

docker-build:
	docker build -t $(IMAGE):$(TAG) .
