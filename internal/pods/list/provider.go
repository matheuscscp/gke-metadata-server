// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package listpods

import (
	"context"
	"fmt"
	"strings"

	"github.com/matheuscscp/gke-metadata-server/internal/pods"

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

func NewProvider(opts ProviderOptions) pods.Provider {
	return &Provider{opts}
}

func (p *Provider) GetByIP(ctx context.Context, ipAddr string) (*corev1.Pod, error) {
	fieldSelector := strings.Join([]string{
		"spec.nodeName=" + p.opts.NodeName,
		"spec.hostNetwork=false",
		"status.podIP=" + ipAddr,
	}, ",")
	podList, err := p.opts.KubeClient.
		CoreV1().
		Pods(corev1.NamespaceAll).
		List(ctx, metav1.ListOptions{FieldSelector: fieldSelector})
	if err != nil {
		return nil, fmt.Errorf("error listing pods in the node matching cluster ip %s: %w", ipAddr, err)
	}
	podList.Items = pods.FilterPods(podList.Items)

	if n := len(podList.Items); n != 1 {
		if n == 0 {
			return nil, fmt.Errorf("no pods found in the node matching cluster ip %s", ipAddr)
		}

		refs := make([]string, n)
		for i, pod := range podList.Items {
			refs[i] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		}
		return nil, fmt.Errorf("multiple pods found in the node matching cluster ip %s (%v pods): %s",
			ipAddr, n, strings.Join(refs, ", "))
	}

	return &podList.Items[0], nil
}
