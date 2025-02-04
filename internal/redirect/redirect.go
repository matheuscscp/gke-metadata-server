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

package redirect

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

//go:generate sh -c "bpftool btf dump file /sys/kernel/btf/vmlinux format c > ../../ebpf/vmlinux.h"
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -type Config redirect ../../ebpf/redirect.c

type BPF struct {
	objs redirectObjects
	link link.Link
}

func LoadAndAttachBPF(emulatorIP netip.Addr, emulatorPort int, debug bool) (*BPF, error) {
	var b BPF

	if err := loadRedirectObjects(&b.objs, nil); err != nil {
		return nil, fmt.Errorf("error loading redirect eBPF redirect objects: %w", err)
	}

	emulatorIPv4 := emulatorIP.As4()
	config := redirectConfig{
		EmulatorIp:   binary.BigEndian.Uint32(emulatorIPv4[:]),
		EmulatorPort: uint16(emulatorPort),
	}
	if debug {
		config.Debug = 1
	}
	var key uint32 = 0
	if err := b.objs.redirectMaps.MapConfig.Update(&key, &config, ebpf.UpdateAny); err != nil {
		return nil, fmt.Errorf("error updating redirect eBPF config map: %w", err)
	}

	var err error
	b.link, err = link.AttachCgroup(link.CgroupOptions{
		Path:    "/sys/fs/cgroup",
		Attach:  ebpf.AttachCGroupInet4Connect,
		Program: b.objs.RedirectConnect4,
	})
	if err != nil {
		return nil, fmt.Errorf("error attaching redirect eBPF program to cgroup: %w", err)
	}

	return &b, nil
}

func (b *BPF) Close() error {
	e1 := b.link.Close()
	e2 := b.objs.Close()
	if e1 == nil {
		return e2
	}
	if e2 == nil {
		return e1
	}
	return errors.Join(e1, e2)
}
