// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package getnode

import (
	"context"

	"github.com/matheuscscp/gke-metadata-server/internal/node"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type (
	Provider struct {
		opts ProviderOptions
	}

	ProviderOptions struct {
		NodeName   string
		KubeClient *kubernetes.Clientset
	}
)

func NewProvider(opts ProviderOptions) node.Provider {
	return &Provider{opts}
}

func (p *Provider) Get(ctx context.Context) (*corev1.Node, error) {
	return p.opts.KubeClient.CoreV1().
		Nodes().
		Get(ctx, p.opts.NodeName, metav1.GetOptions{})
}
