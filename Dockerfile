# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

ARG TARGETARCH

FROM golang:1.26.5-alpine3.23@sha256:622e56dbc11a8cfe87cafa2331e9a201877271cbff918af53d3be315f3da88cc AS builder

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

FROM gcr.io/distroless/static:latest@sha256:d5f030ca7c5793784e9ea4178a116da360250411d13921a5af27c6cb5a5949bf

COPY --from=builder /app/gke-metadata-server .
COPY LICENSE .

ENTRYPOINT ["./gke-metadata-server"]
