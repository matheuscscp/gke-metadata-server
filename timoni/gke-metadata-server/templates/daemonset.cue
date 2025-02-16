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
	metadata:   #config.metadata
	spec: {
		selector: matchLabels: #config.selector.labels
		template: {
			metadata: {
				labels: #config.selector.labels & {podAntiAffinity: "gke-metadata-server"}
				if #config.pod.annotations != _|_ {
					annotations: #config.pod.annotations
				}
			}
			spec: {
				if #config.settings.nodePool.enable {
					nodeSelector: {
						"gke-metadata-server.matheuscscp.io/nodePoolName":      #config.metadata.name
						"gke-metadata-server.matheuscscp.io/nodePoolNamespace": #config.metadata.namespace
					}
					tolerations: [
						{
							key:      "gke-metadata-server.matheuscscp.io/nodePoolName"
							operator: "Equal"
							value:    #config.metadata.name
							effect:   "NoExecute"
						},
						{
							key:      "gke-metadata-server.matheuscscp.io/nodePoolNamespace"
							operator: "Equal"
							value:    #config.metadata.namespace
							effect:   "NoExecute"
						},
					]
				}
				affinity: {
					podAntiAffinity: requiredDuringSchedulingIgnoredDuringExecution: [{
						labelSelector:     matchLabels: {podAntiAffinity: "gke-metadata-server"}
						namespaceSelector: {}
						topologyKey:       "kubernetes.io/hostname"
					}]
					if !#config.settings.nodePool.enable {
						nodeAffinity: requiredDuringSchedulingIgnoredDuringExecution: nodeSelectorTerms: [{
							matchExpressions: [
								{
									key:      "gke-metadata-server.matheuscscp.io/nodePoolName"
									operator: "DoesNotExist"
								},
								{
									key:      "gke-metadata-server.matheuscscp.io/nodePoolNamespace"
									operator: "DoesNotExist"
								},
							]
						}]
					}
				}
				if #config.pod.nodeSelector != _|_ {
					nodeSelector: #config.pod.nodeSelector
				}
				if #config.pod.affinity != _|_ {
					affinity: #config.pod.affinity
				}
				if #config.pod.tolerations != _|_ {
					tolerations: #config.pod.tolerations
				}
				serviceAccountName: #config.metadata.name
				priorityClassName:  #config.pod.priorityClass
				containers: [{
					name:            #config.metadata.name
					image:           #config.image.reference
					imagePullPolicy: #config.image.pullPolicy
					securityContext: {
						privileged: true
					}
					args: [
						"server",
						"--project-id=\(#config.settings.projectID)",
						"--workload-identity-provider=\(#config.settings.workloadIdentityProvider)",
						if #config.settings.nodePool.enable {
							"--node-pool-service-account-name=\(#config.metadata.name)"
						}
						if #config.settings.nodePool.enable {
							"--node-pool-service-account-namespace=\(#config.metadata.namespace)"
						}
						if #config.settings.logLevel != _|_ {
							"--log-level=\(#config.settings.logLevel)"
						}
						if #config.settings.serverPort != _|_ {
							"--server-port=\(#config.settings.serverPort)"
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
						name:          "http"
						containerPort: #config.settings.serverPort
						protocol:      "TCP"
					}]
					#probes: {
						initialDelaySeconds: 3
						httpGet: {
							path: "/healthz"
							port: "http"
						}
					}
					readinessProbe: #probes
					livenessProbe:  #probes
					volumeMounts: [{
						name:      "tmpfs"
						mountPath: "/tmp"
					}]
					if #config.pod.resources != _|_ {
						resources: #config.pod.resources
					}
				}]
				volumes: [ {
					name:     "tmpfs"
					emptyDir: medium: "Memory"
				}]
			}
		}
	}
}
