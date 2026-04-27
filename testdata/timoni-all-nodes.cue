// Copyright 2026 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package main

values: requireNodeLabel: false

values: settings: {
	projectID:                "gke-metadata-server"
	workloadIdentityProvider: "projects/637293746831/locations/global/workloadIdentityPools/test-kind-cluster/providers/<TEST_ID>"
	testProxyUpstream:        true
}

values: image: {
	repository: "<LOCAL_IMAGE>"
	tag:        "<LOCAL_TAG_DAEMON>"
	pullPolicy: "Never"
}
