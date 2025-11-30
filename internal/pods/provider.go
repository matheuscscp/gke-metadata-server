// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package pods

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

type Provider interface {
	GetByIP(ctx context.Context, ipAddr string) (*corev1.Pod, error)
}

// FilterPods removes pods that are not running.
func FilterPods[T any](pods []T) []T {
	var filtered []T
	for _, v := range pods {
		switch pod := any(v).(type) {
		case corev1.Pod:
			if isPodRunning(&pod) {
				filtered = append(filtered, v)
			}
		case *corev1.Pod:
			if isPodRunning(pod) {
				filtered = append(filtered, v)
			}
		}
	}
	return filtered
}

func isPodRunning(pod *corev1.Pod) bool {
	switch {
	case pod.DeletionTimestamp != nil,
		pod.Status.Phase == corev1.PodSucceeded,
		pod.Status.Phase == corev1.PodFailed:
		return false
	default:
		return true
	}
}
