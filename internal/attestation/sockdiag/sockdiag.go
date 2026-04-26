// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

// Package sockdiag implements kernel-attested pod identification for
// hostNetwork pods in routing modes that do not use eBPF (Loopback, None).
//
// Both endpoints of a hostNetwork pod's TCP connection live in the host
// network namespace, so the active-side socket is visible to a netlink
// SOCK_DIAG query on the host. We resolve the connection's 4-tuple to the
// owning socket's inode, walk /proc/<pid>/fd to find the process that holds
// an fd backed by that inode, then read /proc/<pid>/cgroup and extract the
// pod UID from the kubelet's `pod<UID>` cgroup naming convention.
package sockdiag

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"

	"github.com/matheuscscp/gke-metadata-server/internal/attestation"

	"github.com/vishvananda/netlink"
)

// ErrNotFound mirrors the eBPF map's contract: returned when the connection
// 4-tuple is not visible in the host's socket table or the inode owner is
// not in /proc.
var ErrNotFound = errors.New("sockdiag: 4-tuple not in host socket table")

// Lookuper resolves a connection 4-tuple to the kubernetes pod UID of the
// owning process via netlink SOCK_DIAG plus a /proc walk.
type Lookuper struct{}

// New constructs a Lookuper. Stateless; safe to share across goroutines.
func New() *Lookuper { return &Lookuper{} }

// Lookup implements server.AttestationLookuper.
func (l *Lookuper) Lookup(srcIP, dstIP netip.Addr, srcPort, dstPort uint16) (string, error) {
	local := &net.TCPAddr{IP: srcIP.AsSlice(), Port: int(srcPort)}
	remote := &net.TCPAddr{IP: dstIP.AsSlice(), Port: int(dstPort)}

	sock, err := netlink.SocketGet(local, remote)
	if err != nil {
		return "", fmt.Errorf("netlink SocketGet for %s -> %s: %w", local, remote, err)
	}
	if sock.INode == 0 {
		return "", ErrNotFound
	}

	pid, err := pidForSocketInode(uint64(sock.INode))
	if err != nil {
		return "", err
	}

	uid, err := attestation.PodUIDFromCgroup(pid)
	if err != nil {
		return "", fmt.Errorf("resolving pod uid for pid %d: %w", pid, err)
	}
	return uid, nil
}

// pidForSocketInode walks /proc/<pid>/fd looking for a symlink to
// "socket:[<inode>]". O(processes * fds-per-process) — acceptable for an
// occasional metadata request handler, but worth caching if the call rate
// ever grows.
func pidForSocketInode(inode uint64) (int, error) {
	target := fmt.Sprintf("socket:[%d]", inode)

	procDir, err := os.Open(attestation.ProcRoot)
	if err != nil {
		return 0, fmt.Errorf("opening %s: %w", attestation.ProcRoot, err)
	}
	defer procDir.Close()

	for {
		names, err := procDir.Readdirnames(256)
		if errors.Is(err, os.ErrClosed) || len(names) == 0 && err != nil {
			break
		}
		for _, name := range names {
			pid, err := strconv.Atoi(name)
			if err != nil {
				continue
			}
			fdDir := filepath.Join(attestation.ProcRoot, name, "fd")
			entries, err := os.ReadDir(fdDir)
			if err != nil {
				// Process may have exited between readdir and now;
				// or we may lack permission for a kernel-thread.
				continue
			}
			for _, e := range entries {
				link, err := os.Readlink(filepath.Join(fdDir, e.Name()))
				if err != nil {
					continue
				}
				if link == target {
					return pid, nil
				}
			}
		}
		if err != nil {
			break
		}
	}

	return 0, fmt.Errorf("%w: no /proc/*/fd link to inode %d", ErrNotFound, inode)
}
