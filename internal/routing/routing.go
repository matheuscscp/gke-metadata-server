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
func LoadAndAttach(node *corev1.Node, emulatorIP netip.Addr, emulatorPort int) (string, string, func() error, error) {
	var loadAndAttach func() (func() error, error)

	mode := GetMode(node)
	switch mode {
	case api.RoutingModeBPF:
		loadAndAttach = redirect.LoadAndAttach(emulatorIP, emulatorPort)
	case api.RoutingModeLoopback:
		loadAndAttach = loopback.LoadAndAttach
	default:
		return "", "", nil, fmt.Errorf("invalid routing mode: %s", mode)
	}

	close, err := loadAndAttach()
	if err != nil {
		return "", "", nil, err
	}
	return mode, GetServerAddr(mode, emulatorPort), close, nil
}

func GetMode(node *corev1.Node) string {
	if m := node.Annotations[api.AnnotationRoutingMode]; m != "" {
		return m
	}
	if m := node.Labels[api.AnnotationRoutingMode]; m != "" {
		return m
	}
	return api.RoutingModeDefault
}

func GetServerAddr(mode string, serverPort int) string {
	serverAddr := fmt.Sprintf(":%d", serverPort)
	if mode == api.RoutingModeLoopback {
		serverAddr = loopback.GKEMetadataServerAddr
	}
	return serverAddr
}
