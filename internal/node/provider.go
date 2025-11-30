// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package node

import (
	"context"

	corev1 "k8s.io/api/core/v1"
)

type Provider interface {
	Get(ctx context.Context) (*corev1.Node, error)
}
