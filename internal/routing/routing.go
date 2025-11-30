// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package routing

import (
	"fmt"
	"net/netip"

	"github.com/matheuscscp/gke-metadata-server/api"
	"github.com/matheuscscp/gke-metadata-server/internal/loopback"
	"github.com/matheuscscp/gke-metadata-server/internal/redirect"

	corev1 "k8s.io/api/core/v1"
)

// LoadAndAttach looks up the routing mode from the Node's annotations
// or labels and loads and attaches the routing mechanism accordingly.
func LoadAndAttach(node *corev1.Node, emulatorIP netip.Addr, emulatorPort int) (string, func() error, error) {
	var loadAndAttach func() (func() error, error)

	mode := getMode(node)
	switch mode {
	case api.RoutingModeBPF:
		loadAndAttach = redirect.LoadAndAttach(emulatorIP, emulatorPort)
	case api.RoutingModeLoopback:
		loadAndAttach = loopback.LoadAndAttach
	case api.RoutingModeNone:
		loadAndAttach = func() (func() error, error) {
			return func() error { return nil }, nil
		}
	default:
		return "", nil, fmt.Errorf("invalid routing mode: %s", mode)
	}

	close, err := loadAndAttach()
	if err != nil {
		return "", nil, err
	}
	return mode, close, nil
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
