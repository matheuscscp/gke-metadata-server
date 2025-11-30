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
	__u32 emulator_pid;
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
	// 0xA9FEC4F5 is 169.254.196.245, the IP address we chose for
	// self-discovery of the emulator PID, and 12345 is the port.
	// If the connection is not targeting either of these addresses,
	// do nothing, just allow it.
	if (!(dst == 0xA9FEA9FE && dst_port == 80) && !(dst == 0xA9FEC4F5 && dst_port == 12345)) {
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

	// Get the PID of the current process.
	const __u64 pid_tgid = bpf_get_current_pid_tgid();
	const __u32 pid = pid_tgid >> 32;

	// If the emulator PID is 0 and the connection is targeting the self-discovery
	// address, store the current PID as the emulator PID and block the connection.
	if (conf->emulator_pid == 0) {
		if (dst == 0xA9FEC4F5 && dst_port == 12345) {
			conf->emulator_pid = pid;
			if (conf->debug) {
				bpf_printk("Discovered emulator PID: %d", pid);
			}
			return 0; // Block the connection.
		}
		bpf_printk("Error: redirect_connect4 called before emulator PID discovery");
		return 1;
	}

	// If the connection is coming from the emulator process, allow it without redirection.
	if (pid == conf->emulator_pid) {
		if (conf->debug) {
			bpf_printk("Not redirecting connection from emulator process (PID: %d)", pid);
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
