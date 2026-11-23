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
	// Port the daemon listens on, in host byte order. Only connects to this
	// destination port are recorded.
	__u16 server_port;
	__u16 _pad0;
	__u32 _pad1;
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
// matched, ran[2] = AF_INET passed, ran[3] = map updated, ran[4] = map entry
// deleted on close. Lets userspace confirm whether the program is firing and
// how far it gets.
struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 5);
	__type(key, __u32);
	__type(value, __u64);
} map_attest_debug SEC(".maps");

static __always_inline void inc_debug(__u32 idx) {
	__u64 *c = bpf_map_lookup_elem(&map_attest_debug, &idx);
	if (c) __sync_fetch_and_add(c, 1);
}

// local_port is exposed in host byte order in the low 16 bits of a u32.
// remote_port is the kernel's __be16 sk_dport shifted into the *high*
// 16 bits of the u32 (see net/core/filter.c convert_bpf_sock_ops_access),
// so we right-shift before casting to u16. Both ports end up stored in
// network byte order so they match userspace's htons-encoded lookup key.
static __always_inline struct AttestKey attest_key(struct bpf_sock_ops *skops) {
	struct AttestKey key = {
		.src_ip = skops->local_ip4,
		.dst_ip = skops->remote_ip4,
		.src_port = bpf_htons((__u16)skops->local_port),
		.dst_port = (__u16)(skops->remote_port >> 16),
	};
	return key;
}

// Hooks active-side TCP connect to record the connecting task's cgroup ID
// along with the 4-tuple the kernel will eventually report to the
// server-side socket, and removes the record again when the socket closes.
//
// At BPF_SOCK_OPS_TCP_CONNECT_CB the local port has been bound but the SYN
// has not been sent yet, so the entry is in the map well before the server
// can possibly accept the connection. Userspace then walks /sys/fs/cgroup
// to find the directory whose inode equals this cgroup ID and extracts the
// pod UID from the kubelet's `pod<UID>` cgroup naming convention.
//
// The program is attached at the cgroup root, so it sees every active TCP
// connect on the node. Only connects whose destination port is the daemon's
// server port are recorded: the cgroup/connect4 redirect has already
// rewritten 169.254.169.254:80 to the daemon's address before this hook
// runs, and hostNetwork clients dial the daemon port directly. Any other
// connection can never be looked up, so recording it would only fill the
// LRU map and eventually evict entries of long-lived daemon connections.
//
// For recorded connections the program also enables the state callback on
// the socket and deletes the entry once the socket reaches TCP_CLOSE, so the
// map holds only connections that are currently open.
SEC("sockops")
int attest_connect(struct bpf_sock_ops *skops) {
	inc_debug(0);

	if (skops->op == BPF_SOCK_OPS_STATE_CB) {
		// Only sockets recorded below enable this callback.
		if (skops->args[1] == BPF_TCP_CLOSE && skops->family == AF_INET) {
			struct AttestKey key = attest_key(skops);
			if (bpf_map_delete_elem(&map_attest, &key) == 0) {
				inc_debug(4);
			}
		}
		return 0;
	}

	if (skops->op != BPF_SOCK_OPS_TCP_CONNECT_CB) {
		return 0;
	}
	inc_debug(1);
	if (skops->family != AF_INET) {
		return 0;
	}
	inc_debug(2);

	const __u32 conf_key = 0;
	struct AttestConfig *conf = bpf_map_lookup_elem(&map_attest_config, &conf_key);
	if (!conf) {
		return 0;
	}
	const __u16 dst_port = bpf_ntohs((__u16)(skops->remote_port >> 16));
	if (dst_port != conf->server_port) {
		return 0;
	}

	struct AttestKey key = attest_key(skops);

	// We record the connecting task's cgroup ID rather than its pid. The
	// cgroup ID is the inode of the cgroup directory in cgroupfs and is
	// global across PID namespaces, so the daemon can resolve it without
	// any namespace translation, even in nested setups (e.g. kind, where
	// the daemon's pid namespace is the kind worker's, not the kernel's
	// root). The pod UID is then derived from the cgroup path.
	struct AttestValue val = {
		.cgroup_id = bpf_get_current_cgroup_id(),
	};

	if (bpf_map_update_elem(&map_attest, &key, &val, BPF_ANY) != 0) {
		return 0;
	}
	inc_debug(3);

	// Get BPF_SOCK_OPS_STATE_CB for this socket so the entry is deleted
	// when the connection closes (including a failed connect).
	bpf_sock_ops_cb_flags_set(skops, skops->bpf_sock_ops_cb_flags | BPF_SOCK_OPS_STATE_CB_FLAG);
	return 0;
}

char __LICENSE[] SEC("license") = "GPL";
