// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

// Package bpf wraps the eBPF sockops program and 4-tuple -> cgroup-ID map
// that the userspace metadata server consults to derive the kubernetes pod
// identity of a connecting process. The cgroup ID is resolved to a pod UID
// by walking /sys/fs/cgroup for the matching inode (see attestation.PodUIDFromCgroupID).
package bpf

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"

	"github.com/matheuscscp/gke-metadata-server/internal/attestation"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -type AttestConfig -type AttestKey -type AttestValue attest ../../../ebpf/attest.c

// Map wraps the loaded sockops program and its companion lookup map. Lookup
// returns ErrNotFound when the 4-tuple is unknown — typically because the
// connect happened before the program was attached, or the LRU evicted the
// entry. The (mode, pod-kind) dispatch in the server treats this as a hard
// attestation failure (HTTP 403); there is no fallback to source IP by
// design.
type Map struct {
	objs attestObjects
	link link.Link
}

// ErrNotFound is returned when no record exists for a 4-tuple.
var ErrNotFound = errors.New("attestation: 4-tuple not in map")

// LoadAndAttach loads the sockops program and attaches it to the host's
// cgroup root so it fires for every active TCP connect on the node.
func LoadAndAttach() (*Map, error) {
	var objs attestObjects
	if err := loadAttestObjects(&objs, nil); err != nil {
		return nil, fmt.Errorf("loading attest eBPF objects: %w", err)
	}

	lnk, err := link.AttachCgroup(link.CgroupOptions{
		Path:    "/sys/fs/cgroup",
		Attach:  ebpf.AttachCGroupSockOps,
		Program: objs.AttestConnect,
	})
	if err != nil {
		objs.Close()
		return nil, fmt.Errorf("attaching attest sockops program to cgroup: %w", err)
	}

	return &Map{objs: objs, link: lnk}, nil
}

// Close detaches the program and frees BPF resources.
func (m *Map) Close() error {
	e1 := m.link.Close()
	e2 := m.objs.Close()
	return errors.Join(e1, e2)
}

// Lookup returns the pod UID for the connection identified by the given
// 4-tuple, derived from the cgroup ID the BPF program recorded at active-
// connect time. Returns ErrNotFound if the 4-tuple is not in the map.
func (m *Map) Lookup(srcIP, dstIP netip.Addr, srcPort, dstPort uint16) (string, error) {
	// The BPF program writes skops->{local,remote}_ip4 (kernel __be32 fields)
	// directly into the map key, so the in-memory bytes are network byte
	// order. To match in Go we read the IP bytes as a native-endian uint32
	// — that preserves the byte ordering verbatim across the boundary.
	srcIPv4 := srcIP.As4()
	dstIPv4 := dstIP.As4()
	key := attestAttestKey{
		SrcIp:   binary.NativeEndian.Uint32(srcIPv4[:]),
		DstIp:   binary.NativeEndian.Uint32(dstIPv4[:]),
		SrcPort: htons(srcPort),
		DstPort: htons(dstPort),
	}

	var val attestAttestValue
	if err := m.objs.attestMaps.MapAttest.Lookup(&key, &val); err != nil {
		if errors.Is(err, ebpf.ErrKeyNotExist) || errors.Is(err, os.ErrNotExist) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("looking up attestation map: %w", err)
	}
	uid, err := attestation.PodUIDFromCgroupID(val.CgroupId)
	if err != nil {
		return "", fmt.Errorf("resolving pod from cgroup id %d: %w", val.CgroupId, err)
	}
	return uid, nil
}

// Verify checks that the BPF sockops program captured the given 4-tuple.
// Returns ErrNotFound if the entry isn't there — typically meaning the
// program is not yet attached or did not fire for this connection. Used by
// the daemon's readiness probe to gate /readyz on the pipeline being live.
func (m *Map) Verify(srcIP, dstIP netip.Addr, srcPort, dstPort uint16) error {
	srcIPv4 := srcIP.As4()
	dstIPv4 := dstIP.As4()
	key := attestAttestKey{
		SrcIp:   binary.NativeEndian.Uint32(srcIPv4[:]),
		DstIp:   binary.NativeEndian.Uint32(dstIPv4[:]),
		SrcPort: htons(srcPort),
		DstPort: htons(dstPort),
	}
	var val attestAttestValue
	if err := m.objs.attestMaps.MapAttest.Lookup(&key, &val); err != nil {
		if errors.Is(err, ebpf.ErrKeyNotExist) || errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("verifying attestation map: %w", err)
	}
	return nil
}

func htons(v uint16) uint16 {
	return v<<8 | v>>8
}
