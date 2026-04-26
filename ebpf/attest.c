// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

#include "vmlinux.h"

#include <bpf/bpf_core_read.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

// IPv4 family
#define AF_INET 2

// Attestation key/value: written by the active-connect sockops hook below
// and consumed by userspace at HTTP request time. The key is the 4-tuple
// the kernel will report on the server-side socket (so userspace can match
// using conn.RemoteAddr() + the daemon's accepted LocalAddr) and the value
// carries the connecting task's cgroup ID. The cgroup ID is the inode of
// the cgroup directory in cgroupfs: kernel-attested, namespace-independent,
// and not reused after the pod cgroup is destroyed.
struct AttestKey {
	__u32 src_ip;       // network byte order, matches what server sees
	__u32 dst_ip;       // ditto
	__u16 src_port;     // network byte order
	__u16 dst_port;     // network byte order
};

struct AttestValue {
	__u64 cgroup_id;
};

struct AttestConfig {
	// reserved for future tuneables; kept as a struct for ABI room.
	__u64 _reserved;
};

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct AttestConfig);
} map_attest_config SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_LRU_HASH);
	__uint(max_entries, 65536);
	__type(key, struct AttestKey);
	__type(value, struct AttestValue);
} map_attest SEC(".maps");

// Debug counters: ran[0] = sockops entered (any op), ran[1] = TCP_CONNECT_CB
// matched, ran[2] = AF_INET passed, ran[3] = map updated. Lets userspace
// confirm whether the program is firing and how far it gets.
struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 4);
	__type(key, __u32);
	__type(value, __u64);
} map_attest_debug SEC(".maps");

static __always_inline void inc_debug(__u32 idx) {
	__u64 *c = bpf_map_lookup_elem(&map_attest_debug, &idx);
	if (c) __sync_fetch_and_add(c, 1);
}

// Hooks active-side TCP connect to record the connecting task's cgroup ID
// along with the 4-tuple the kernel will eventually report to the
// server-side socket.
//
// At BPF_SOCK_OPS_TCP_CONNECT_CB the local port has been bound but the SYN
// has not been sent yet, so the entry is in the map well before the server
// can possibly accept the connection. Userspace then walks /sys/fs/cgroup
// to find the directory whose inode equals this cgroup ID and extracts the
// pod UID from the kubelet's `pod<UID>` cgroup naming convention.
SEC("sockops")
int attest_connect(struct bpf_sock_ops *skops) {
	inc_debug(0);
	if (skops->op != BPF_SOCK_OPS_TCP_CONNECT_CB) {
		return 0;
	}
	inc_debug(1);
	if (skops->family != AF_INET) {
		return 0;
	}
	inc_debug(2);

	// local_port is exposed in host byte order in the low 16 bits of a u32.
	// remote_port is the kernel's __be16 sk_dport shifted into the *high*
	// 16 bits of the u32 (see net/core/filter.c convert_bpf_sock_ops_access),
	// so we right-shift before casting to u16. Both ports end up stored in
	// network byte order so they match userspace's htons-encoded lookup key.
	struct AttestKey key = {
		.src_ip = skops->local_ip4,
		.dst_ip = skops->remote_ip4,
		.src_port = bpf_htons((__u16)skops->local_port),
		.dst_port = (__u16)(skops->remote_port >> 16),
	};

	// We record the connecting task's cgroup ID rather than its pid. The
	// cgroup ID is the inode of the cgroup directory in cgroupfs and is
	// global across PID namespaces, so the daemon can resolve it without
	// any namespace translation, even in nested setups (e.g. kind, where
	// the daemon's pid namespace is the kind worker's, not the kernel's
	// root). The pod UID is then derived from the cgroup path.
	struct AttestValue val = {
		.cgroup_id = bpf_get_current_cgroup_id(),
	};

	bpf_map_update_elem(&map_attest, &key, &val, BPF_ANY);
	inc_debug(3);
	return 0;
}

char __LICENSE[] SEC("license") = "GPL";
