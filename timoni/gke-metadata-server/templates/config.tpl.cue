// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package templates

import (
	timoniv1 "timoni.sh/core/v1alpha1"
)

// Config defines the schema and defaults for the Instance values.
#Config: {
	// The kubeVersion is a required field, set at apply-time
	// via timoni.cue by querying the user's Kubernetes API.
	kubeVersion!: string

	// Using the kubeVersion you can enforce a minimum Kubernetes minor version.
	// By default, the minimum Kubernetes version is set to 1.20.
	clusterVersion: timoniv1.#SemVer & {#Version: kubeVersion, #Minimum: "1.20.0"}

	// The moduleVersion is set from the user-supplied module version.
	// This field is used for the `app.kubernetes.io/version` label.
	moduleVersion!: string

	// The Kubernetes metadata common to all resources.
	// The `metadata.name` and `metadata.namespace` fields are
	// set from the user-supplied instance name and namespace.
	metadata: timoniv1.#Metadata & {#Version: moduleVersion}

	// The labels allows adding `metadata.labels` to all resources.
	// The `app.kubernetes.io/name` and `app.kubernetes.io/version` labels
	// are automatically generated and can't be overwritten.
	metadata: labels: timoniv1.#Labels

	// The annotations allows adding `metadata.annotations` to all resources.
	metadata: annotations?: timoniv1.#Annotations

	// The selector allows adding label selectors to Deployments and Services.
	// The `app.kubernetes.io/name` label selector is automatically generated
	// from the instance name and can't be overwritten.
	selector: timoniv1.#Selector & {#Name: metadata.name}

	// The image allows setting the container image repository,
	// tag, digest and pull policy.
	image: timoniv1.#Image & {
		repository: string | *"ghcr.io/matheuscscp/gke-metadata-server"
		tag:        string | *"<CONTAINER_VERSION>"
		digest:     string | *""
	}

	// The pod allows setting the Kubernetes Pod annotations and resources.
	pod: {
		annotations?:  timoniv1.#Annotations
		resources?:    timoniv1.#ResourceRequirements
	}

	// requireNodeLabel controls whether the iam.gke.io/gke-metadata-server-enabled: "true"
	// label is required in the DaemonSet node affinity. Set to false to run on all nodes.
	requireNodeLabel: bool | *true

	// dns applies DNS configurations based on a DNS provider for apps to
	// resolve metadata.google.internal to 169.254.169.254 cluster-wide.
	//   - None: No DNS configuration. The default.
	//   - CoreDNSCustom: Applies a `coredns-custom` ConfigMap with "metadata.override".
	dns: {
		provider: ("None" | "CoreDNSCustom") | *"None"
	}

	// The application settings.
	settings: #Settings

	// Helper definitions.
	#namespacedMetadata: {
		name:      "gke-metadata-server"
		namespace: "kube-system"
		labels:    metadata.labels
		if metadata.annotations != _|_ {
			annotations: metadata.annotations
		}
		if metadata.finalizers != _|_ {
			finalizers: metadata.finalizers
		}
	}
	#clusterScopedMetadata: {
		name:   "gke-metadata-server"
		labels: metadata.labels
		if metadata.annotations != _|_ {
			annotations: metadata.annotations
		}
		if metadata.finalizers != _|_ {
			finalizers: metadata.finalizers
		}
	}
}

// Instance takes the config values and outputs the Kubernetes objects.
#Instance: {
	config: #Config

	objects: {
		daemonSet: #DaemonSet & {#config: config}

		// rbac.cue
		serviceAccount:     #ServiceAccount & {#config: config}
		clusterRole:        #ClusterRole & {#config: config}
		clusterRoleBinding: #ClusterRoleBinding & {#config: config}

		// coredns-custom.cue
		if config.dns.provider == "CoreDNSCustom" {
			coreDnsConfigMap: #CoreDNSConfigMap & {#config: config}
		}
	}
}
