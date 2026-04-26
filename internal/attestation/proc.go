// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

// Package attestation derives the kubernetes pod identity of a process from
// kernel-exposed state. The kubelet places every pod's processes under a
// cgroup whose path contains "pod<UID>", so the pod UID is recoverable
// either from a PID (read /proc/<pid>/cgroup) or from a cgroup ID (walk
// /sys/fs/cgroup until the matching inode is found). Both inputs are
// kernel-attested and cannot be forged from user space.
//
// PodUIDFromCgroup is used by the netlink SOCK_DIAG path (Loopback/None
// routing modes for hostNetwork pods), which resolves a 4-tuple to a PID
// via the host's socket table and a /proc/*/fd walk.
//
// PodUIDFromCgroupID is used by the eBPF sockops path, which records
// bpf_get_current_cgroup_id() at TCP connect time. Cgroup IDs are
// namespace-independent, so this works even when the daemon runs inside a
// nested PID namespace (e.g. inside a kind worker container).
package attestation

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
)

// ProcRoot is the prefix used to read /proc files. Overridable in tests.
var ProcRoot = "/proc"

// podUIDPattern matches the "pod<UID>" segment that the kubelet places in
// every pod cgroup path. Both styles are matched:
//   - cgroupfs driver: ".../pod<UID>/..."     (UID with hyphens)
//   - systemd driver:  ".../pod_<UID>.slice"  (UID with underscores)
//
// The UID itself is canonicalised to the hyphen form (kubernetes' API form).
var podUIDPattern = regexp.MustCompile(`pod([0-9a-fA-F]{8}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{4}[-_][0-9a-fA-F]{12})`)

// PodUIDFromCgroup reads /proc/<pid>/cgroup and extracts the pod UID from
// the kubepods cgroup path. Only the cgroup v2 unified hierarchy line
// (hierarchy id "0", empty controllers field) is consulted; hybrid v1+v2
// systems return the v2 entry, mixed-mode v1 systems are not supported.
func PodUIDFromCgroup(pid int) (string, error) {
	f, err := os.Open(fmt.Sprintf("%s/%d/cgroup", ProcRoot, pid))
	if err != nil {
		return "", fmt.Errorf("reading /proc/%d/cgroup: %w", pid, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		// Format: <hierarchy-id>:<controllers>:<path>.
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 || fields[0] != "0" || fields[1] != "" {
			continue
		}
		match := podUIDPattern.FindStringSubmatch(fields[2])
		if match == nil {
			return "", fmt.Errorf("pid %d cgroup path %q is not under a kubepods pod cgroup", pid, fields[2])
		}
		return strings.ReplaceAll(match[1], "_", "-"), nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scanning /proc/%d/cgroup: %w", pid, err)
	}
	return "", errors.New("no cgroup v2 entry in /proc/" + strconv.Itoa(pid) + "/cgroup")
}

// CgroupV2Mount is the cgroup v2 unified hierarchy mount point. Overridable
// in tests.
var CgroupV2Mount = "/sys/fs/cgroup"

// PodUIDFromCgroupID walks the cgroup v2 hierarchy looking for the directory
// whose inode equals the given cgroup id (which is what
// bpf_get_current_cgroup_id() reports), and extracts the pod UID from the
// matching path. The cgroup ID is namespace-independent and unique to a live
// pod cgroup, so this resolves the connecting process to its pod without any
// PID-namespace translation.
func PodUIDFromCgroupID(cgid uint64) (string, error) {
	var found string
	err := filepath.WalkDir(CgroupV2Mount, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Skip permission errors / transient races.
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		if !d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}
		if stat.Ino != cgid {
			return nil
		}
		match := podUIDPattern.FindStringSubmatch(path)
		if match == nil {
			return fmt.Errorf("cgroup id %d resolved to path %q, which is not under a kubepods pod cgroup", cgid, path)
		}
		found = strings.ReplaceAll(match[1], "_", "-")
		return fs.SkipAll
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("cgroup id %d not found under %s", cgid, CgroupV2Mount)
	}
	return found, nil
}
