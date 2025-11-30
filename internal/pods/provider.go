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
