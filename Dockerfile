# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

ARG TARGETARCH

FROM golang:1.26.6-alpine3.23@sha256:e57c41c1d5864341031181b0db34b9a537bb5773eb6428e4e5bdaea0f9135406 AS builder

RUN apk add --no-cache clang llvm bpftool libbpf-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY ./api/ ./api/
COPY ./internal/ ./internal/
COPY ./ebpf/ ./ebpf/

RUN go generate ./internal/redirect
RUN go generate ./internal/attestation/bpf

# CGO_ENABLED=0 to build a statically-linked binary
# -ldflags '-w -s' to strip debugging information for smaller size
ARG TARGETARCH
RUN CGO_ENABLED=0 GOFIPS140=latest GOOS=linux GOARCH=${TARGETARCH} go build -ldflags="-w -s" -o gke-metadata-server \
    github.com/matheuscscp/gke-metadata-server

FROM gcr.io/distroless/static:latest@sha256:9197324ba51d9cd071af8505989365c006adf9d6d2067eada25aef00abbb5278

COPY --from=builder /app/gke-metadata-server .
COPY LICENSE .

ENTRYPOINT ["./gke-metadata-server"]
