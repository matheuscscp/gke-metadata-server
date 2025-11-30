// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package getserviceaccount

import (
	"context"

	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type (
	Provider struct {
		opts ProviderOptions
	}

	ProviderOptions struct {
		KubeClient *kubernetes.Clientset
	}
)

func NewProvider(opts ProviderOptions) serviceaccounts.Provider {
	return &Provider{opts}
}

func (p *Provider) Get(ctx context.Context, ref *serviceaccounts.Reference) (*corev1.ServiceAccount, error) {
	return p.opts.KubeClient.CoreV1().
		ServiceAccounts(ref.Namespace).
		Get(ctx, ref.Name, metav1.GetOptions{})
}
