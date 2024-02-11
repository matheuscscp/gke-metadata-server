// MIT License
//
// Copyright (c) 2024 Matheus Pimenta
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
	podList, err := p.opts.KubeClient.CoreV1().Pods(corev1.NamespaceAll).List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.podIP=%s", p.opts.NodeName, ipAddr),
	})
	if err != nil {
		return nil, fmt.Errorf("error listing pods in the node matching cluster ip address %q: %w", ipAddr, err)
	}
	if n := len(podList.Items); n != 1 || podList.Items[0].Spec.HostNetwork {
		if n == 1 { // pods on the host network are not supported, see README.md
			podList.Items = nil
			n = 0
		}
		refs := make([]string, n)
		for i, pod := range podList.Items {
			refs[i] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		}
		return nil, fmt.Errorf("error listing pods in the node matching cluster ip address %q: %v pods found instead of 1 [%s]",
			ipAddr, n, strings.Join(refs, ", "))
	}
	return &podList.Items[0], nil
}
