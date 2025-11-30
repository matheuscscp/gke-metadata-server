// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package pkgtime

import (
	"context"
	"time"
)

func SleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
