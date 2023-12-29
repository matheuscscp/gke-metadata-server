// MIT License
//
// Copyright (c) 2023 Matheus Pimenta
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

package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	pkgtime "github.com/matheuscscp/gke-metadata-server/internal/time"

	"github.com/google/uuid"
	"golang.org/x/oauth2/google"
	authnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type (
	podContextKey                          struct{}
	podGoogleServiceAccountEmailContextKey struct{}
)

const (
	kubernetesServiceAccountAnnotation = "iam.gke.io/gcp-service-account"
	googleServiceAccountEmailPattern   = `^[a-zA-Z0-9-]+@[a-zA-Z0-9-]+\.iam\.gserviceaccount\.com$`
)

var googleServiceAccountEmailRegex = regexp.MustCompile(googleServiceAccountEmailPattern)

// getPod retrieves the Pod associated with the request by
// looking up the client IP address.
// If there's an error this function sends the response to the client.
func (s *Server) getPod(w http.ResponseWriter,
	r *http.Request) (*corev1.Pod, *http.Request, error) {
	// check if pod is already in the request
	ctx := r.Context()
	if v := ctx.Value(podContextKey{}); v != nil {
		return v.(*corev1.Pod), r, nil
	}

	// get client ip address. **ATTENTION** this IP address **NEEDS**
	// to be retrieved from the connection. this information **CANNOT**
	// be retrieved from any input in the request that could've easily
	// been specified by an attacker trying to impersonate a legit pod.
	// in particular, we cannot trust the "X-Forwarded-For" de-facto
	// HTTP standard header that proxies use to describe the proxy
	// chain the original request came from, or any other HTTP header
	// for that matter. to make it very clear, the metadata server
	// **MUST** only trust source IP addresses as reported in the IP
	// datagrams/TCP segments the network interface delivered to the
	// server. if this gets compromised, game over. this is the core
	// security assumption of the project.
	clientHost, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		const format = "error spliting host-port for '%s': %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, r.RemoteAddr, err)
		return nil, nil, fmt.Errorf(format, r.RemoteAddr, err)
	}
	clientIPAddr, err := netip.ParseAddr(clientHost)
	if err != nil {
		const format = "error parsing ip address client host '%s': %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, r.RemoteAddr, err)
		return nil, nil, fmt.Errorf(format, r.RemoteAddr, err)
	}
	clientIP := clientIPAddr.String()
	l := logging.FromRequest(r).WithField("client_ip", clientIP)
	r = logging.IntoRequest(r, l)
	getPodRetries := s.metrics.getPodRetries.WithLabelValues(clientIP)

	// fetch pod by ip
	var pod *corev1.Pod
	for i, maxAttempts := 1, 3; i <= maxAttempts; i++ {
		var err error
		pod, err = s.tryGetPod(ctx, clientIP)
		if err == nil {
			break
		}

		// it's fairly common for the first tryGetPod() call to fail for
		// pods that were created very recently, but it cant take too long.
		// if after a little exp. back-off the client IP is not showing up
		// on the k8s API then yes, it could be just temporarily control
		// plane unavailable, but it could also be a malicious DDoS attempt.
		// in order to give some protection against this hopefully unlikely
		// case we limit each request to only a few tryGetPod() call attempts
		// and emphasize the potential urgency of the situation through this
		// exponential retry counter.
		expRetriesCount := (1 << (i + 1)) - 4 // 0, 4, 12...
		getPodRetries.Add(float64(expRetriesCount))

		// check max attempts reached
		if i == maxAttempts {
			const format = "reached max attempts while trying to fetch the pod associated with the request. " +
				"last attempt error: %w"
			pkghttp.RespondErrorf(w, r, http.StatusTooManyRequests, format, err)
			return nil, nil, fmt.Errorf(format, err)
		}

		// exponential back-off
		expDelay := 500 * (1 << i) * time.Millisecond // 1s, 2s...
		logf := l.WithError(err).Warnf
		// do not warn about the first attempt failure since it's fairly common
		if i == 1 {
			logf = l.WithError(err).Debugf
		}
		logf("error getting pod associated with the request. retrying after %v...", expDelay)
		if err := pkgtime.SleepContext(ctx, expDelay); err != nil {
			const format = "request context canceled while sleeping before get pod retry attempt: %w"
			pkghttp.RespondErrorf(w, r, pkghttp.StatusClientClosedRequest, format, err)
			return nil, nil, fmt.Errorf(format, err)
		}
	}

	// update context and logger
	ctx = context.WithValue(ctx, podContextKey{}, pod)
	l = l.WithField("pod", logging.Pod(pod))
	r = logging.IntoRequest(r.WithContext(ctx), l)

	return pod, r, nil
}

func (s *Server) tryGetPod(ctx context.Context, clientIP string) (*corev1.Pod, error) {
	podList, err := s.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.podIP=%s", s.opts.NodeName, clientIP),
	})
	if err != nil {
		return nil, fmt.Errorf("error listing pods in the node matching the client ip address: %w", err)
	}
	var podsNotInTheHostNetwork []int
	for i := range podList.Items {
		// pods in the host network are not supported, see README.md
		if !podList.Items[i].Spec.HostNetwork {
			podsNotInTheHostNetwork = append(podsNotInTheHostNetwork, i)
		}
	}
	if nPods := len(podsNotInTheHostNetwork); nPods != 1 {
		podRefs := make([]string, nPods)
		for i, podIdx := range podsNotInTheHostNetwork {
			pod := podList.Items[podIdx]
			podRefs[i] = fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		}
		return nil, fmt.Errorf("the number of pods matching the client ip address is not exactly one: %v [%s]",
			nPods, strings.Join(podRefs, ", "))
	}
	pod := podList.Items[podsNotInTheHostNetwork[0]]
	return &pod, nil
}

// getPodGoogleServiceAccountEmail gets the Google Service Account email associated with the given pod.
// If there's an error this function sends the response to the client.
func (s *Server) getPodGoogleServiceAccountEmail(w http.ResponseWriter, r *http.Request) (string, *http.Request, error) {
	ctx := r.Context()
	if v := ctx.Value(podGoogleServiceAccountEmailContextKey{}); v != nil {
		return v.(string), r, nil
	}

	// get pod and service account
	pod, r, err := s.getPod(w, r)
	if err != nil {
		return "", nil, err
	}
	sa, err := s.clientset.CoreV1().
		ServiceAccounts(pod.Namespace).
		Get(ctx, pod.Spec.ServiceAccountName, metav1.GetOptions{})
	if err != nil {
		const format = "error getting pod service account: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, err)
		return "", nil, fmt.Errorf(format, err)
	}

	// get and parse google service account email from pod service account annotation
	podGoogleServiceAccountEmail, ok := sa.Annotations[kubernetesServiceAccountAnnotation]
	if !ok {
		const format = "annotation %s is missing for pod service account"
		pkghttp.RespondErrorf(w, r, http.StatusBadRequest, format,
			kubernetesServiceAccountAnnotation)
		return "", nil, fmt.Errorf(format, kubernetesServiceAccountAnnotation)
	}
	podGoogleServiceAccountEmail = strings.TrimSpace(podGoogleServiceAccountEmail)
	if !googleServiceAccountEmailRegex.MatchString(podGoogleServiceAccountEmail) {
		const format = "annotation %s does not contain a valid Google Service Account Email (%s)"
		pkghttp.RespondErrorf(w, r, http.StatusBadRequest, format,
			kubernetesServiceAccountAnnotation, googleServiceAccountEmailPattern)
		return "", nil, fmt.Errorf(format, kubernetesServiceAccountAnnotation,
			googleServiceAccountEmailPattern)
	}
	ctx = context.WithValue(ctx, podGoogleServiceAccountEmailContextKey{},
		podGoogleServiceAccountEmail)
	l := logging.FromRequest(r).WithField("pod_google_service_account_email",
		podGoogleServiceAccountEmail)
	r = logging.IntoRequest(r.WithContext(ctx), l)
	return podGoogleServiceAccountEmail, r, nil
}

// listPodGoogleServiceAccounts lists the available GCP Service Accounts for the requesting Pod.
// If there's an error this function sends the response to the client.
func (s *Server) listPodGoogleServiceAccounts(w http.ResponseWriter, r *http.Request) ([]string, *http.Request, error) {
	podGoogleServiceAccountEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
	if err != nil {
		return nil, nil, err
	}
	return []string{"default", podGoogleServiceAccountEmail}, r, nil
}

// getPodServiceAccountToken creates a Service Account Token for the
// given Pod's Service Account.
// If there's an error this function sends the response to the client.
func (s *Server) getPodServiceAccountToken(w http.ResponseWriter, r *http.Request,
	audience string) (string, *http.Request, error) {
	pod, r, err := s.getPod(w, r)
	if err != nil {
		return "", nil, err
	}

	expSeconds := int64(tokenExpirationSeconds)
	tokenResp, err := s.clientset.
		CoreV1().
		ServiceAccounts(pod.Namespace).
		CreateToken(r.Context(), pod.Spec.ServiceAccountName, &authnv1.TokenRequest{
			Spec: authnv1.TokenRequestSpec{
				Audiences:         []string{audience},
				ExpirationSeconds: &expSeconds,
			},
		}, metav1.CreateOptions{})
	if err != nil {
		const format = "error creating token for pod service account: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, err)
		return "", nil, fmt.Errorf(format, err)
	}
	return tokenResp.Status.Token, r, nil
}

// runWithGoogleCredentialsFromPodServiceAccountToken creates
// a *google.Credentials object from a Kubernetes ServiceAccount
// Token created for the ServiceAccount currently being used by
// the requesting Pod. The function internally writes the token
// to a temporary file and runs the given callback f() passing
// a *google.Credentials object configured to use this temporary
// file. The temporary file is removed before the function
// returns (hence why a callback is used).
// If there's an error this function sends the response to the
// client (and does not call the callback f()).
func (s *Server) runWithGoogleCredentialsFromPodServiceAccountToken(
	w http.ResponseWriter, r *http.Request, f func(*google.Credentials)) {
	podGoogleServiceAccountEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
	if err != nil {
		return
	}

	saToken, r, err := s.getPodServiceAccountToken(w, r, s.workloadIdentityProviderAudience())
	if err != nil {
		return
	}

	// write k8s sa token to tmp file
	var tokenFile string
	for {
		tokenFile = filepath.Join(os.TempDir(), fmt.Sprintf("%s.json", uuid.NewString()))
		file, err := os.OpenFile(tokenFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			pkghttp.RespondErrorf(w, r, http.StatusInternalServerError,
				"error creating temporary file '%s': %w", tokenFile, err)
			return
		}
		defer os.Remove(tokenFile)
		if _, err := file.Write([]byte(saToken)); err != nil {
			pkghttp.RespondErrorf(w, r, http.StatusInternalServerError,
				"error writing pod sa token to temporary file '%s': %w", tokenFile, err)
			return
		}
		if err := file.Close(); err != nil {
			pkghttp.RespondErrorf(w, r, http.StatusInternalServerError,
				"error closing pod sa token temporary file: %w", err)
			return
		}
		break
	}

	// get the credential config with k8s sa token file as the credential source
	b, err := json.Marshal(s.getGoogleCredentialConfig(podGoogleServiceAccountEmail, map[string]any{
		"format": map[string]string{"type": "text"},
		"file":   tokenFile,
	}))
	if err != nil {
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError,
			"error marshaling google credential config to json: %w", err)
		return
	}
	creds, err := google.CredentialsFromJSON(r.Context(), b, gkeAccessScopes()...)
	if err != nil {
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError,
			"error getting google credentials for pod service account token: %w", err)
		return
	}

	// run callback with creds, then defer will remove the sa token file
	f(creds)
}
