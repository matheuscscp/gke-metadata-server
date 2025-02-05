// MIT License
//
// Copyright (c) 2025 Matheus Pimenta
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

#include "vmlinux.h"

#include <bpf/bpf_core_read.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include <bpf/bpf_helpers.h>

// IPv4 family
#define AF_INET 2

struct Config {
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
	// We only care about IPv4 TCP connections
	if (ctx->user_family != AF_INET || ctx->protocol != IPPROTO_TCP) {
		return 1;
	}

	const __u32 dst = bpf_ntohl(ctx->user_ip4);
	const __u32 dst_port = bpf_ntohs(ctx->user_port);

	// 0xA9FEA9FE is 169.254.169.254, the GKE Metadata Server IP address
	if (dst != 0xA9FEA9FE || dst_port != 80) {
		return 1;
	}

	// Fetch emulator configuration
	const __u32 key = 0;
	struct Config *conf = bpf_map_lookup_elem(&map_config, &key);
	if (!conf) {
		bpf_printk("Error: redirect_connect4 called without configuration");
		return 1;
	}

	// Redirect the connection to the emulator
	ctx->user_ip4 = bpf_htonl(conf->emulator_ip);
	ctx->user_port = bpf_htons(conf->emulator_port);

	if (conf->debug) {
		const __u32 emu = ctx->user_ip4;
		bpf_printk("Redirecting connection to %pI4:%d", &emu, conf->emulator_port);
	}

	return 1;
}

char __LICENSE[] SEC("license") = "GPL";
