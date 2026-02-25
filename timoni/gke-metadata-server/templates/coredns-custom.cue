// Copyright 2026 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package templates

import (
	corev1 "k8s.io/api/core/v1"
)

#CoreDNSConfigMap: corev1.#ConfigMap & {
	#config:    #Config
	apiVersion: "v1"
	kind:       "ConfigMap"
	metadata: {
		name:      "coredns-custom"
		namespace: "kube-system"
		labels:    #config.metadata.labels
	}
	data: {
		"metadata.override": """
			template IN A metadata.google.internal {
			  answer "{{ .Name }} 60 IN A 169.254.169.254"
			}
			"""
	}
}
