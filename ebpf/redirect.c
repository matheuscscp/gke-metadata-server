// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

#include "vmlinux.h"

#include <bpf/bpf_core_read.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

// IPv4 family
#define AF_INET 2

struct Config {
	__u64 emulator_cgroup_id;
	__u32 emulator_ip;
	__u16 emulator_port;
	__u16 debug;
};

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 1);
	__type(key, __u32);
	__type(value, struct Config);
} map_config SEC(".maps");

// Hooks to connect() syscalls. Redirects connections targeting
// the GKE metadata server to the emulator.
SEC("cgroup/connect4")
int redirect_connect4(struct bpf_sock_addr *ctx) {
	// We only care about IPv4 TCP connections.
	if (ctx->user_family != AF_INET || ctx->protocol != IPPROTO_TCP) {
		return 1;
	}

	const __u32 dst = bpf_ntohl(ctx->user_ip4);
	const __u32 dst_port = bpf_ntohs(ctx->user_port);

	// 0xA9FEA9FE is 169.254.169.254, the GKE Metadata Server IP address.
	// If the connection is not targeting that address on port 80, do nothing.
	if (dst != 0xA9FEA9FE || dst_port != 80) {
		return 1;
	}

	// Fetch emulator configuration. If not found, log an error
	// and allow the connection without redirection.
	const __u32 key = 0;
	struct Config *conf = bpf_map_lookup_elem(&map_config, &key);
	if (!conf) {
		bpf_printk("Error: redirect_connect4 called without configuration");
		return 1;
	}

	// If the connection is coming from the emulator's cgroup, allow it
	// without redirection. The cgroup ID is set by userspace at startup
	// from the inode of the daemon's own cgroup directory; it is
	// kernel-attested and stable for the lifetime of the daemon's pod.
	const __u64 cgid = bpf_get_current_cgroup_id();
	if (cgid == conf->emulator_cgroup_id) {
		if (conf->debug) {
			bpf_printk("Not redirecting connection from emulator cgroup (id: %llu)", cgid);
		}
		return 1;
	}

	// Redirect the connection to the emulator.
	ctx->user_ip4 = bpf_htonl(conf->emulator_ip);
	ctx->user_port = bpf_htons(conf->emulator_port);
	if (conf->debug) {
		const __u32 emu = ctx->user_ip4;
		bpf_printk("Redirecting connection to emulator on %pI4:%d", &emu, conf->emulator_port);
	}
	return 1; // Allow the connection after redirection.
}

char __LICENSE[] SEC("license") = "GPL";
