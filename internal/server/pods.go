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
	"fmt"
	"net"
	"net/http"
	"net/netip"

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/retry"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"

	corev1 "k8s.io/api/core/v1"
)

type (
	podContextKey                          struct{}
	podGoogleServiceAccountEmailContextKey struct{}
)

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
		const format = "error spliting host-port for %q: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, r.RemoteAddr, err)
		return nil, nil, fmt.Errorf(format, r.RemoteAddr, err)
	}
	clientIPAddr, err := netip.ParseAddr(clientHost)
	if err != nil {
		const format = "error parsing ip address client host %q: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, r.RemoteAddr, err)
		return nil, nil, fmt.Errorf(format, r.RemoteAddr, err)
	}
	clientIP := clientIPAddr.String()
	l := logging.FromContext(ctx).WithField("client_ip", clientIP)
	ctx = logging.IntoContext(ctx, l)
	r = r.WithContext(ctx)
	getPodFailures := s.metrics.getPodFailures.WithLabelValues(clientIP)

	// fetch pod by ip
	var pod *corev1.Pod
	err = retry.Do(ctx, retry.Operation{
		Description:    "fetch the pod associated with the request",
		FailureCounter: getPodFailures,
		Func: func() error {
			var err error
			pod, err = s.opts.Pods.GetByIP(ctx, clientIP)
			if err != nil {
				return fmt.Errorf("error looking up pod by ip address: %w", err)
			}
			return nil
		},
	})
	if err != nil {
		const format = "error getting pod associated with request: %w"
		pkghttp.RespondErrorf(w, r, retry.HTTPStatusCode(err), format, err)
		return nil, nil, fmt.Errorf(format, err)
	}

	// update context and logger
	ctx = context.WithValue(ctx, podContextKey{}, pod)
	l = l.WithField("pod", logging.Pod(pod))
	r = logging.IntoRequest(r.WithContext(ctx), l)

	return pod, r, nil
}

// getPodGoogleServiceAccountEmail gets the Google Service Account email associated with the given pod.
// If there's an error this function sends the response to the client.
func (s *Server) getPodGoogleServiceAccountEmail(w http.ResponseWriter, r *http.Request) (string, *http.Request, error) {
	ctx := r.Context()
	if v := ctx.Value(podGoogleServiceAccountEmailContextKey{}); v != nil {
		return v.(string), r, nil
	}
	pod, r, err := s.getPod(w, r)
	if err != nil {
		return "", nil, err
	}
	sa, err := s.opts.ServiceAccounts.Get(ctx, pod.Namespace, pod.Spec.ServiceAccountName)
	if err != nil {
		const format = "error getting pod service account: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, err)
		return "", nil, fmt.Errorf(format, err)
	}
	email, err := serviceaccounts.GoogleEmail(sa)
	if err != nil {
		pkghttp.RespondError(w, r, http.StatusBadRequest, err)
		return "", nil, err
	}
	ctx = context.WithValue(ctx, podGoogleServiceAccountEmailContextKey{}, email)
	l := logging.FromRequest(r).WithField("pod_google_service_account_email", email)
	r = logging.IntoRequest(r.WithContext(ctx), l)
	return email, r, nil
}

// listPodGoogleServiceAccounts lists the available Google Service Accounts for the requesting Pod.
// If there's an error this function sends the response to the client.
func (s *Server) listPodGoogleServiceAccounts(w http.ResponseWriter, r *http.Request) ([]string, *http.Request, error) {
	podGoogleServiceAccountEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
	if err != nil {
		return nil, nil, err
	}
	return []string{"default", podGoogleServiceAccountEmail}, r, nil
}

// getPodServiceAccountToken creates a ServiceAccount Token for the
// given Pod's ServiceAccount.
// If there's an error this function sends the response to the client.
func (s *Server) getPodServiceAccountToken(w http.ResponseWriter, r *http.Request) (string, *http.Request, error) {
	pod, r, err := s.getPod(w, r)
	if err != nil {
		return "", nil, err
	}
	token, _, err := s.opts.ServiceAccountTokens.GetServiceAccountToken(r.Context(), pod.Namespace, pod.Spec.ServiceAccountName)
	if err != nil {
		const format = "error getting token for pod service account: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, err)
		return "", nil, fmt.Errorf(format, err)
	}
	return token, r, nil
}
