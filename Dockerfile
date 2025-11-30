# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

FROM golang:1.25.4-alpine3.21 AS builder

RUN apk add --no-cache clang llvm bpftool libbpf-dev

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
COPY ./api/ ./api/
COPY ./internal/ ./internal/
COPY ./ebpf/ ./ebpf/

RUN go generate ./internal/redirect

# CGO_ENABLED=0 to build a statically-linked binary
# -ldflags '-w -s' to strip debugging information for smaller size
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o gke-metadata-server \
    github.com/matheuscscp/gke-metadata-server

FROM gcr.io/distroless/static:latest

COPY --from=builder /app/gke-metadata-server .
COPY LICENSE .

ENTRYPOINT ["./gke-metadata-server"]
