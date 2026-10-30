// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package routing

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/matheuscscp/gke-metadata-server/api"
	"github.com/matheuscscp/gke-metadata-server/internal/attestation"
	"github.com/matheuscscp/gke-metadata-server/internal/loopback"
	"github.com/matheuscscp/gke-metadata-server/internal/redirect"

	corev1 "k8s.io/api/core/v1"
)

var selfCgroupDir = redirect.SelfCgroupDir

// LoadAndAttach looks up the routing mode from the Node's annotations
// or labels and loads and attaches the routing mechanism accordingly.
// In eBPF mode it also returns the cgroup path the redirect is attached to.
func LoadAndAttach(node *corev1.Node, emulatorIP netip.Addr, emulatorPort int, redirectCgroupPath string) (string, string, func() error, error) {
	var loadAndAttach func() (func() error, error)
	var cgroupPath string

	mode := getMode(node)
	switch mode {
	case api.RoutingModeBPF:
		var err error
		if cgroupPath, err = checkCgroupPath(redirectCgroupPath); err != nil {
			return mode, "", nil, err
		}
		loadAndAttach = redirect.LoadAndAttach(emulatorIP, emulatorPort, cgroupPath)
	case api.RoutingModeLoopback:
		loadAndAttach = loopback.LoadAndAttach
	case api.RoutingModeNone:
		loadAndAttach = func() (func() error, error) {
			return func() error { return nil }, nil
		}
	default:
		return mode, "", nil, fmt.Errorf("invalid routing mode: %s", mode)
	}

	close, err := loadAndAttach()
	if err != nil {
		return mode, "", nil, err
	}
	return mode, cgroupPath, close, nil
}

func getMode(node *corev1.Node) string {
	if m := node.Annotations[api.AnnotationRoutingMode]; m != "" {
		return m
	}
	if m := node.Labels[api.AnnotationRoutingMode]; m != "" {
		return m
	}
	return api.RoutingModeDefault
}

func checkCgroupPath(v string) (string, error) {
	if v == "" {
		return attestation.CgroupV2Mount, nil
	}
	p := filepath.Clean(v)
	if !isUnder(p, attestation.CgroupV2Mount) {
		return "", fmt.Errorf("invalid --redirect-cgroup-path %q: must be a directory under %s", v, attestation.CgroupV2Mount)
	}
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("invalid --redirect-cgroup-path %q: %w", v, err)
	}
	if !fi.IsDir() {
		return "", fmt.Errorf("invalid --redirect-cgroup-path %q: not a directory", v)
	}
	self, err := selfCgroupDir()
	if err != nil {
		return "", fmt.Errorf("error resolving the emulator's own cgroup: %w", err)
	}
	if !isUnder(self, p) {
		return "", fmt.Errorf("invalid --redirect-cgroup-path %q: must contain the emulator's own Pod cgroup %s", v, self)
	}
	return p, nil
}

func isUnder(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, dir+"/")
}
