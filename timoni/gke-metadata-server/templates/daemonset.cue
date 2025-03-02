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
	appsv1 "k8s.io/api/apps/v1"
)

#DaemonSet: appsv1.#DaemonSet & {
	#config:    #Config
	apiVersion: "apps/v1"
	kind:       "DaemonSet"
	metadata:   #config.#namespacedMetadata
	spec: {
		selector: matchLabels: #config.selector.labels
		template: {
			metadata: {
				labels: #config.selector.labels & {
					app: "gke-metadata-server"
				}
				if #config.pod.annotations != _|_ {
					annotations: #config.pod.annotations
				}
			}
			spec: {
				hostNetwork:        true
				serviceAccountName: #config.#namespacedMetadata.name
				priorityClassName:  "system-node-critical"
				nodeSelector: {
					"iam.gke.io/gke-metadata-server-enabled": "true"
					"kubernetes.io/os":                       "linux"
					"kubernetes.io/arch":                     "amd64"
				}
				tolerations: [{
					key:      "iam.gke.io/gke-metadata-server-enabled"
					operator: "Equal"
					value:    "true"
					effect:   "NoExecute"
				}]
				containers: [{
					name:            #config.#namespacedMetadata.name
					image:           #config.image.reference
					imagePullPolicy: #config.image.pullPolicy
					securityContext: {
						privileged: true
					}
					args: [
						"--service-account-name=\(#config.#namespacedMetadata.name)",
						"--service-account-namespace=\(#config.#namespacedMetadata.namespace)",
						"--project-id=\(#config.settings.projectID)",
						"--workload-identity-provider=\(#config.settings.workloadIdentityProvider)",
						if #config.settings.logLevel != _|_ {
							"--log-level=\(#config.settings.logLevel)"
						}
						if #config.settings.serverPort != _|_ {
							"--server-port=\(#config.settings.serverPort)"
						}
						if #config.settings.healthPort != _|_ {
							"--health-port=\(#config.settings.healthPort)"
						}
						if #config.settings.watchPods.enable {
							"--watch-pods"
						}
						if #config.settings.watchPods.enable && #config.settings.watchPods.disableFallback {
							"--watch-pods-disable-fallback"
						}
						if #config.settings.watchPods.enable && #config.settings.watchPods.resyncPeriod != _|_ {
							"--watch-pods-resync-period=\(#config.settings.watchPods.resyncPeriod)"
						}
						if #config.settings.watchNode.enable {
							"--watch-node"
						}
						if #config.settings.watchNode.enable && #config.settings.watchNode.disableFallback {
							"--watch-node-disable-fallback"
						}
						if #config.settings.watchNode.enable && #config.settings.watchNode.resyncPeriod != _|_ {
							"--watch-node-resync-period=\(#config.settings.watchNode.resyncPeriod)"
						}
						if #config.settings.watchServiceAccounts.enable {
							"--watch-service-accounts"
						}
						if #config.settings.watchServiceAccounts.enable && #config.settings.watchServiceAccounts.disableFallback {
							"--watch-service-accounts-disable-fallback"
						}
						if #config.settings.watchServiceAccounts.enable && #config.settings.watchServiceAccounts.resyncPeriod != _|_ {
							"--watch-service-accounts-resync-period=\(#config.settings.watchServiceAccounts.resyncPeriod)"
						}
						if #config.settings.cacheTokens.enable {
							"--cache-tokens"
						}
						if #config.settings.cacheTokens.enable && #config.settings.cacheTokens.concurrency != _|_ {
							"--cache-tokens-concurrency=\(#config.settings.cacheTokens.concurrency)"
						}
					]
					env: [
						{
							name:                           "NODE_NAME"
							valueFrom: fieldRef: fieldPath: "spec.nodeName"
						},
						{
							name:                           "POD_IP"
							valueFrom: fieldRef: fieldPath: "status.podIP"
						},
					]
					ports: [{
						name:          "health"
						containerPort: #config.settings.healthPort
						protocol:      "TCP"
					}]
					livenessProbe: {
						initialDelaySeconds: 3
						httpGet: {
							path: "/healthz"
							port: "health"
						}
					}
					readinessProbe: {
						initialDelaySeconds: 3
						httpGet: {
							path: "/readyz"
							port: "health"
						}
					}
					if #config.pod.resources != _|_ {
						resources: #config.pod.resources
					}
				}]
			}
		}
	}
}
