// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package redirect

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

//go:generate sh -c "bpftool btf dump file /sys/kernel/btf/vmlinux format c > ../../ebpf/vmlinux.h"
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -type Config redirect ../../ebpf/redirect.c

const cgroupv2Mount = "/sys/fs/cgroup"

func LoadAndAttach(emulatorIP netip.Addr, emulatorPort int) func() (func() error, error) {
	return func() (func() error, error) {
		var objs redirectObjects
		if err := loadRedirectObjects(&objs, nil); err != nil {
			return nil, fmt.Errorf("error loading redirect eBPF redirect objects: %w", err)
		}

		// Resolve the daemon's own cgroup ID so the eBPF program can identify
		// outbound connections from the emulator itself (e.g. proxy passthrough
		// to the real metadata server) and let them through unredirected.
		cgroupID, err := selfCgroupID()
		if err != nil {
			return nil, fmt.Errorf("error resolving emulator cgroup id: %w", err)
		}

		// Configure the eBPF program with the emulator's IP and port.
		emulatorIPv4 := emulatorIP.As4()
		config := redirectConfig{
			EmulatorCgroupId: cgroupID,
			EmulatorIp:       binary.BigEndian.Uint32(emulatorIPv4[:]),
			EmulatorPort:     uint16(emulatorPort),
		}
		if logging.Debug() {
			config.Debug = 1
		}
		var key uint32 = 0
		if err := objs.redirectMaps.MapConfig.Update(&key, &config, ebpf.UpdateAny); err != nil {
			return nil, fmt.Errorf("error updating redirect eBPF config map: %w", err)
		}

		// Attach the eBPF program to the cgroup.
		link, err := link.AttachCgroup(link.CgroupOptions{
			Path:    cgroupv2Mount,
			Attach:  ebpf.AttachCGroupInet4Connect,
			Program: objs.RedirectConnect4,
		})
		if err != nil {
			return nil, fmt.Errorf("error attaching redirect eBPF program to cgroup: %w", err)
		}

		return func() (err error) {
			e1 := link.Close()
			e2 := objs.Close()
			if e1 == nil {
				return e2
			}
			if e2 == nil {
				return e1
			}
			return errors.Join(e1, e2)
		}, nil
	}
}

// selfCgroupID resolves the kernel cgroup ID of the daemon's own cgroup. The
// cgroup ID is the inode number of the cgroup directory in cgroupfs and is
// what bpf_get_current_cgroup_id() returns from inside an eBPF program.
//
// Only cgroup v2 (unified hierarchy) is supported. The caller's cgroup is
// read from /proc/self/cgroup, which on a unified system has a single line
// of the form "0::<path>".
func selfCgroupID() (uint64, error) {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return 0, fmt.Errorf("error opening /proc/self/cgroup: %w", err)
	}
	defer f.Close()

	var cgroupPath string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: <hierarchy-id>:<controllers>:<path>. The unified-v2 line
		// has hierarchy-id "0" and empty controllers.
		fields := strings.SplitN(line, ":", 3)
		if len(fields) == 3 && fields[0] == "0" && fields[1] == "" {
			cgroupPath = fields[2]
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading /proc/self/cgroup: %w", err)
	}
	if cgroupPath == "" {
		return 0, errors.New("could not find cgroup v2 entry in /proc/self/cgroup; only the unified hierarchy is supported")
	}

	dir := filepath.Join(cgroupv2Mount, cgroupPath)
	var st syscall.Stat_t
	if err := syscall.Stat(dir, &st); err != nil {
		return 0, fmt.Errorf("error stating cgroup directory %q: %w", dir, err)
	}
	return st.Ino, nil
}
