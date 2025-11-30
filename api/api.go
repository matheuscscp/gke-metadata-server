// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package api

const (
	GroupCore = "gke-metadata-server.matheuscscp.io"
	GroupNode = "node." + GroupCore

	GroupGKE = "iam.gke.io"

	AnnotationRoutingMode             = GroupNode + "/routingMode"
	AnnotationServiceAccountName      = GroupNode + "/serviceAccountName"
	AnnotationServiceAccountNamespace = GroupNode + "/serviceAccountNamespace"

	RoutingModeDefault  = RoutingModeBPF
	RoutingModeBPF      = "eBPF"
	RoutingModeLoopback = "Loopback"
	RoutingModeNone     = "None"

	GKEAnnotationServiceAccount = GroupGKE + "/gcp-service-account"
	GKELabelNodeEnabled         = GroupGKE + "/gke-metadata-server-enabled"
)
