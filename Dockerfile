# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

ARG TARGETARCH

FROM golang:1.27.0-alpine3.23@sha256:3747dcba41c8b0db3211fda4db61638b980e17ac5bb3c94460a975a9cfe19395 AS builder

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

FROM gcr.io/distroless/static:latest@sha256:f2ea2709ac8db56323cbd7d014277f32cb572d9ea124b0076f7aafe5980678fe

COPY --from=builder /app/gke-metadata-server .
COPY LICENSE .

ENTRYPOINT ["./gke-metadata-server"]
