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

package templates

import (
	corev1 "k8s.io/api/core/v1"
	timoniv1 "timoni.sh/core/v1alpha1"
)

#apiGroup: "gke-metadata-server.matheuscscp.io"

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

	// The pod allows setting the Kubernetes Pod annotations, resources and
	// priority class. The default priority class is "system-node-critical".
	pod: {
		annotations?:  timoniv1.#Annotations
		resources?:    timoniv1.#ResourceRequirements
		priorityClass: string | *"system-node-critical"
		nodeSelector?: {[string]: string}
		affinity?:     corev1.#Affinity
		tolerations?:  [...corev1.#Toleration]
	}

	// The application settings.
	settings: #Settings

	// Helper definitions.
	#webhookCAName:  "\(metadata.name)-ca"
	#webhookTLSName: "\(metadata.name)-tls"
	#metadataWithoutName: {
		namespace: metadata.namespace
		labels:    metadata.labels
		if metadata.annotations != _|_ {
			annotations: metadata.annotations
		}
		if metadata.finalizers != _|_ {
			finalizers: metadata.finalizers
		}
		...
	}
	#clusterMetadata: {
		name:   metadata.name
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
		service:   #Service & {#config: config}

		// mutatingwebhook.cue
		mutatingWebhook: #MutatingWebhook & {#config: config}
		caIssuer:        #CAIssuer & {#config: config}
		caCertificate:   #CACertificate & {#config: config}
		tlsIssuer:       #TLSIssuer & {#config: config}
		tlsCertificate:  #TLSCertificate & {#config: config}

		// rbac.cue
		serviceAccount:     #ServiceAccount & {#config: config}
		clusterRole:        #ClusterRole & {#config: config}
		clusterRoleBinding: #ClusterRoleBinding & {#config: config}
	}
}
