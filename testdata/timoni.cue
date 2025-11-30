// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package main

values: settings: {
	projectID:                "gke-metadata-server"
	workloadIdentityProvider: "projects/637293746831/locations/global/workloadIdentityPools/test-kind-cluster/providers/<TEST_ID>"
}

values: image: {
	repository: "ghcr.io/matheuscscp/gke-metadata-server/test"
	digest:     "<CONTAINER_DIGEST>"
}
