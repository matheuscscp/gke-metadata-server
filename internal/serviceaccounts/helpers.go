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

package serviceaccounts

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	corev1 "k8s.io/api/core/v1"
)

type Reference struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

var ErrGKEAnnotationInvalid = fmt.Errorf("gke annotation %q has invalid google service account email", gkeAnnotation)

const (
	gkeAnnotation = "iam.gke.io/gcp-service-account"

	emulatorAPIGroup        = "gke-metadata-server.matheuscscp.io"
	serviceAccountName      = emulatorAPIGroup + "/serviceAccountName"
	serviceAccountNamespace = emulatorAPIGroup + "/serviceAccountNamespace"
)

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
// providers support customizing annotations on Nodes.
//
// The ServiceAccount reference is retrieved from the following pair of annotations or labels:
//
// gke-metadata-server.matheuscscp.io/serviceAccountName
//
// gke-metadata-server.matheuscscp.io/serviceAccountNamespace
//
// If the annotations or labels are not found, defaultRef is returned.
func ReferenceFromNode(node *corev1.Node, defaultRef *Reference) *Reference {
	if ref := getServiceAccountReference(node.Annotations); ref != nil {
		return ref
	}
	if ref := getServiceAccountReference(node.Labels); ref != nil {
		return ref
	}
	return defaultRef
}

// ReferenceFromToken returns a ServiceAccount reference from a ServiceAccount Token.
func ReferenceFromToken(token string) *Reference {
	tok, _, _ := jwt.NewParser().ParseUnverified(token, jwt.MapClaims{})
	sub, _ := tok.Claims.GetSubject()
	s := strings.Split(sub, ":") // system:serviceaccount:{namespace}:{name}
	return &Reference{Namespace: s[2], Name: s[3]}
}

// GoogleEmail returns the Google service account email from the same annotation
// used in native GCP Workload Identity Federation for GKE. The annotation is:
//
// iam.gke.io/gcp-service-account
func GoogleEmail(sa *corev1.ServiceAccount) (*string, error) {
	v, ok := sa.Annotations[gkeAnnotation]
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
	name, ok := m[serviceAccountName]
	if !ok {
		return nil
	}
	namespace, ok := m[serviceAccountNamespace]
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
