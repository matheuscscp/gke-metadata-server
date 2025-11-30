// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package templates

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

#ServiceAccount: corev1.#ServiceAccount & {
	#config:    #Config
	apiVersion: "v1"
	kind:       "ServiceAccount"
	metadata:   #config.#namespacedMetadata
}

#ClusterRole: rbacv1.#ClusterRole & {
	#config:    #Config
	apiVersion: "rbac.authorization.k8s.io/v1"
	kind:       "ClusterRole"
	metadata:   #config.#clusterScopedMetadata
	rules: [{
		apiGroups: [""]
		resources: ["pods", "nodes", "serviceaccounts"]
		verbs:     ["get", "list", "watch"]
	},{
		apiGroups: [""]
		resources: ["nodes"]
		verbs:     ["update"]
	},
	{
		apiGroups: [""]
		resources: ["serviceaccounts/token"]
		verbs:     ["create"]
	}]
}

#ClusterRoleBinding: rbacv1.#ClusterRoleBinding & {
	#config:    #Config
	apiVersion: "rbac.authorization.k8s.io/v1"
	kind:       "ClusterRoleBinding"
	metadata:   #config.#clusterScopedMetadata
	roleRef: {
		apiGroup: "rbac.authorization.k8s.io"
		kind:     "ClusterRole"
		name:     #config.#clusterScopedMetadata.name
	}
	subjects: [{
		kind:      "ServiceAccount"
		name:      #config.#namespacedMetadata.name
		namespace: #config.#namespacedMetadata.namespace
	}]
}
