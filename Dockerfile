# Pin the multi-platform indexes; refresh deliberately with the release runbook.
FROM docker.io/library/golang:1.25.13-trixie@sha256:9044d290beebfb6bb1d1f38d94f6c0f5a217fadff3497c73a760e5c577b24476 AS build
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/ third_party/
RUN go mod download
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY docs/ docs/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /markov ./cmd/markov

FROM docker.io/library/debian:trixie-slim@sha256:a72b4433de1cf7d6bae0c4cee74d0547f58601a643b3375f070c51600d63b6b2
RUN apt-get update && apt-get install -y --no-install-recommends \
    bash coreutils curl git jq ca-certificates procps python3 python3-yaml \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /markov /usr/local/bin/markov
WORKDIR /work
ENTRYPOINT ["markov"]
