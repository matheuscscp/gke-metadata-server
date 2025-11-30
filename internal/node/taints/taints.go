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

package taints

import (
	"context"
	"fmt"
	"strings"

	"github.com/matheuscscp/gke-metadata-server/api"
	"github.com/matheuscscp/gke-metadata-server/internal/retry"

	"github.com/prometheus/client_golang/prometheus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Remove removes any taints from the given node starting with the api.GroupNode prefix.
// Remove will block forever until it succeeds.
func Remove(ctx context.Context, client *kubernetes.Clientset, nodeName string, failureCounter prometheus.Counter) {
	retry.Do(ctx, retry.Operation{
		Description:    "remove taints from node",
		FailureCounter: failureCounter,
		Func: func() error {
			return removeTaints(ctx, client, nodeName)
		},
		IsRetryable: func(error) bool { return true },
	})
}

func removeTaints(ctx context.Context, client *kubernetes.Clientset, nodeName string) error {
	node, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("error getting node: %w", err)
	}

	var taints []corev1.Taint
	for _, taint := range node.Spec.Taints {
		if !strings.HasPrefix(taint.Key, api.GroupNode) {
			taints = append(taints, taint)
		}
	}

	if len(taints) == len(node.Spec.Taints) {
		return nil
	}

	node.Spec.Taints = taints
	_, err = client.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("error updating node: %w", err)
	}

	return nil
}
