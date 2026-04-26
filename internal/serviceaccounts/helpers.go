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
