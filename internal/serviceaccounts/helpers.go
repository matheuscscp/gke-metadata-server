// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package serviceaccounts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/matheuscscp/gke-metadata-server/api"

	"github.com/golang-jwt/jwt/v5"
	corev1 "k8s.io/api/core/v1"
)

type Reference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

var ErrGKEAnnotationInvalid = fmt.Errorf(
	"gke annotation %q has invalid google service account email",
	api.GKEAnnotationServiceAccount)

var googleEmailRegex = regexp.MustCompile(`^[a-zA-Z0-9-]+@[a-zA-Z0-9-]+\.iam\.gserviceaccount\.com$`)

// ReferenceFromObject returns a ServiceAccount reference from a ServiceAccount object.
func ReferenceFromObject(sa *corev1.ServiceAccount) *Reference {
	return &Reference{
		Name:      sa.Name,
		Namespace: sa.Namespace,
	}
}

// ReferenceFromPod returns a ServiceAccount reference from a Pod object.
func ReferenceFromPod(pod *corev1.Pod) *Reference {
	return &Reference{
		Name:      pod.Spec.ServiceAccountName,
		Namespace: pod.Namespace,
	}
}

// ReferenceFromNode returns a ServiceAccount reference from the Node object annotations or labels.
// Annotations take precedence over labels because we encourage users to use annotations instead of
// labels in this case since. Labels are more impactful to etcd since they are indexed, and we don't
// need indexing here so we prefer annotations. However, we support labels because not all cloud
// providers support customizing annotations on Node pools/groups. Not even KinD supports it.
//
// The ServiceAccount reference is retrieved from the following pair of annotations or labels:
//
//	{nodeAPIGroup}/serviceAccountName
//
//	{nodeAPIGroup}/serviceAccountNamespace
//
// Only Pods running on the host network should use this ServiceAccount.
func ReferenceFromNode(node *corev1.Node) *Reference {
	if ref := getServiceAccountReference(node.Annotations); ref != nil {
		return ref
	}
	if ref := getServiceAccountReference(node.Labels); ref != nil {
		return ref
	}
	return nil
}

// ReferenceFromToken returns a ServiceAccount reference from a ServiceAccount Token.
func ReferenceFromToken(token string) *Reference {
	tok, _, _ := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	sub, _ := tok.Claims.GetSubject()
	s := strings.Split(sub, ":") // system:serviceaccount:{namespace}:{name}
	return &Reference{Namespace: s[2], Name: s[3]}
}

// GoogleServiceAccountEmail returns the Google service account email from the same annotation
// used in native GCP Workload Identity Federation for GKE. The annotation is:
//
//	iam.gke.io/gcp-service-account
func GoogleServiceAccountEmail(sa *corev1.ServiceAccount) (*string, error) {
	v, ok := sa.Annotations[api.GKEAnnotationServiceAccount]
	if !ok {
		return nil, nil
	}
	if !googleEmailRegex.MatchString(v) {
		return nil, ErrGKEAnnotationInvalid
	}
	return &v, nil
}

func getServiceAccountReference(m map[string]string) *Reference {
	if m == nil {
		return nil
	}
	name, ok := m[api.AnnotationServiceAccountName]
	if !ok {
		return nil
	}
	namespace, ok := m[api.AnnotationServiceAccountNamespace]
	if !ok {
		return nil
	}
	if name == "" || namespace == "" {
		return nil
	}
	return &Reference{
		Name:      name,
		Namespace: namespace,
	}
}
