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
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
)

#webhookName: #apiGroup

#MutatingWebhook: admissionregistrationv1.#MutatingWebhookConfiguration & {
	#config:    #Config
	apiVersion: "admissionregistration.k8s.io/v1"
	kind:       "MutatingWebhookConfiguration"
	metadata: {
		name:   #config.metadata.name
		labels: #config.metadata.labels
		#certManagerAnnotation: {
			"cert-manager.io/inject-ca-from": "\(#config.metadata.namespace)/\(#config.#webhookCAName)"
		}
		if #config.metadata.annotations != _|_ {
			annotations: #config.metadata.annotations & #certManagerAnnotation
		}
		if #config.metadata.annotations == _|_ {
			annotations: #certManagerAnnotation
		}
		if #config.metadata.finalizers != _|_ {
			finalizers: #config.metadata.finalizers
		}
	}
	webhooks: [{
		name:                    #webhookName
		sideEffects:             "None"
		admissionReviewVersions: ["v1"]
		objectSelector:          matchLabels: {"\(#apiGroup)/webhook": "Mutate"}
		rules: [{
			apiGroups:   [""]
			apiVersions: ["v1"]
			operations:  ["CREATE"]
			resources:   ["pods"]
			scope:       "Namespaced"
		}]
		clientConfig: service: {
			name:      #config.metadata.name
			namespace: #config.metadata.namespace
			port:      #serviceWebhookPort
		}
	}]
}

#CAIssuer: certmanagerv1.#Issuer & {
	#config:    #Config
	apiVersion: "cert-manager.io/v1"
	kind:       "Issuer"
	metadata:   #config.#metadataWithoutName & {
		name:   #config.#webhookCAName
	}
	spec: {
		selfSigned: {}
	}
}

#CACertificate: certmanagerv1.#Certificate & {
	#config:    #Config
	apiVersion: "cert-manager.io/v1"
	kind:       "Certificate"
	metadata:   #config.#metadataWithoutName & {
		name:   #config.#webhookCAName
	}
	spec: {
		isCA:        true
		commonName:  #config.#webhookCAName
		secretName:  #config.#webhookCAName
		duration:    "8760h" // 1 year
		renewBefore: "720h" // 30 days
		privateKey:  {algorithm: "RSA", size: 2048}
		issuerRef: {
			group: "cert-manager.io"
			kind:  "Issuer"
			name:  #config.#webhookCAName
		}
	}
}

#TLSIssuer: certmanagerv1.#Issuer & {
	#config:    #Config
	apiVersion: "cert-manager.io/v1"
	kind:       "Issuer"
	metadata:   #config.#metadataWithoutName & {
		name:   #config.#webhookTLSName
	}
	spec: ca: secretName: #config.#webhookCAName
}

#TLSCertificate: certmanagerv1.#Certificate & {
	#config:    #Config
	apiVersion: "cert-manager.io/v1"
	kind:       "Certificate"
	metadata:   #config.#metadataWithoutName & {
		name:   #config.#webhookTLSName
	}
	spec: {
		usages:      ["server auth"]
		commonName:  #webhookName
		secretName:  #config.#webhookTLSName
		duration:    "8760h" // 1 year
		renewBefore: "5840h" // 8 months
		privateKey:  {algorithm: "RSA", size: 2048}
		issuerRef: {
			group: "cert-manager.io"
			kind:  "Issuer"
			name:  #config.#webhookTLSName
		}
		dnsNames: [
			#webhookName,
			"\(#config.metadata.name).\(#config.metadata.namespace).svc",
			"\(#config.metadata.name).\(#config.metadata.namespace).svc.cluster",
			"\(#config.metadata.name).\(#config.metadata.namespace).svc.cluster.local",
		]
	}
}
