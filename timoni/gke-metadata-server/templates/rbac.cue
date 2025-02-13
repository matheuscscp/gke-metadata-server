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
	rbacv1 "k8s.io/api/rbac/v1"
)

#ServiceAccount: corev1.#ServiceAccount & {
	#config:    #Config
	apiVersion: "v1"
	kind:       "ServiceAccount"
	metadata: {
		name:      #config.metadata.name
		namespace: #config.metadata.namespace
		labels:    #config.metadata.labels
		if #config.metadata.annotations != _|_ && #config.settings.nodePool.enable && #config.settings.nodePool.googleServiceAccount != _|_ {
			annotations: #config.metadata.annotations & {"iam.gke.io/gcp-service-account": #config.settings.nodePool.googleServiceAccount}
		}
		if #config.metadata.annotations != _|_ && !(#config.settings.nodePool.enable && #config.settings.nodePool.googleServiceAccount != _|_) {
			annotations: #config.metadata.annotations
		}
		if #config.metadata.annotations == _|_ && #config.settings.nodePool.enable && #config.settings.nodePool.googleServiceAccount != _|_ {
			annotations: {"iam.gke.io/gcp-service-account": #config.settings.nodePool.googleServiceAccount}
		}
		if #config.metadata.finalizers != _|_ {
			finalizers: #config.metadata.finalizers
		}
	}
}

#ClusterRole: rbacv1.#ClusterRole & {
	#config:    #Config
	apiVersion: "rbac.authorization.k8s.io/v1"
	kind:       "ClusterRole"
	metadata:   #config.#clusterMetadata
	rules: [
		{
			apiGroups: [""]
			resources: ["pods", "nodes", "serviceaccounts"]
			verbs:     ["get", "list", "watch"]
		},
		{
			apiGroups: [""]
			resources: ["serviceaccounts/token"]
			verbs:     ["create"]
		},
	]
}

#ClusterRoleBinding: rbacv1.#ClusterRoleBinding & {
	#config:    #Config
	apiVersion: "rbac.authorization.k8s.io/v1"
	kind:       "ClusterRoleBinding"
	metadata:   #config.#clusterMetadata
	subjects: [{
		kind:      "ServiceAccount"
		name:      #config.metadata.name
		namespace: #config.metadata.namespace
	}]
	roleRef: {
		apiGroup: "rbac.authorization.k8s.io"
		kind:     "ClusterRole"
		name:     #config.#clusterMetadata.name
	}
}
