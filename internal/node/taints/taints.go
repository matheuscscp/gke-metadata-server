// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

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
