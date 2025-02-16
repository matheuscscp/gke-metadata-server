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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/retry"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

type (
	podServiceAccountReferenceContextKey   struct{}
	podGoogleServiceAccountEmailContextKey struct{}
)

// getPodGoogleServiceAccountEmail gets the Google Service Account email associated with the given pod.
// If there's an error this function sends the response to the client.
func (s *Server) getPodGoogleServiceAccountEmail(w http.ResponseWriter, r *http.Request) (*string, *http.Request, error) {
	ctx := r.Context()
	if v := ctx.Value(podGoogleServiceAccountEmailContextKey{}); v != nil {
		return v.(*string), r, nil
	}
	saRef, r, err := s.getPodServiceAccountReference(w, r)
	if err != nil {
		return nil, nil, err
	}
	sa, err := s.opts.ServiceAccounts.Get(ctx, saRef)
	if err != nil {
		const format = "error getting pod service account: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, err)
		return nil, nil, fmt.Errorf(format, err)
	}
	email, err := serviceaccounts.GoogleEmail(sa)
	if err != nil {
		pkghttp.RespondError(w, r, http.StatusBadRequest, err)
		return nil, nil, err
	}
	ctx = context.WithValue(ctx, podGoogleServiceAccountEmailContextKey{}, email)
	l := logging.FromRequest(r)
	if email != nil {
		l = l.WithField("pod_google_service_account_email", *email)
	}
	r = logging.IntoRequest(r.WithContext(ctx), l)
	return email, r, nil
}

// getPodGoogleServiceAccountEmailOrWorkloadIdentityPool gets the Google Service Account email associated with the given pod,
// or the Workload Identity Pool if the pod doesn't have a Google Service Account.
// If there's an error this function sends the response to the client.
func (s *Server) getPodGoogleServiceAccountEmailOrWorkloadIdentityPool(w http.ResponseWriter, r *http.Request) (string, *http.Request, error) {
	podGoogleServiceAccountEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
	if err != nil {
		return "", nil, err
	}
	email := s.opts.WorkloadIdentityPool
	if podGoogleServiceAccountEmail != nil {
		email = *podGoogleServiceAccountEmail
	}
	return email, r, nil
}

// listPodGoogleServiceAccounts lists the available Google Service Accounts for the requesting Pod.
// If there's an error this function sends the response to the client.
func (s *Server) listPodGoogleServiceAccounts(w http.ResponseWriter, r *http.Request) ([]string, *http.Request, error) {
	email, r, err := s.getPodGoogleServiceAccountEmailOrWorkloadIdentityPool(w, r)
	if err != nil {
		return nil, nil, err
	}
	return []string{"default", email}, r, nil
}

// getPodServiceAccountToken creates a ServiceAccount Token for the
// given Pod's ServiceAccount.
// If there's an error this function sends the response to the client.
func (s *Server) getPodServiceAccountToken(w http.ResponseWriter, r *http.Request) (string, *http.Request, error) {
	saRef, r, err := s.getPodServiceAccountReference(w, r)
	if err != nil {
		return "", nil, err
	}
	token, _, err := s.opts.ServiceAccountTokens.GetServiceAccountToken(r.Context(), saRef)
	if err != nil {
		const format = "error getting token for pod service account: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, err)
		return "", nil, fmt.Errorf(format, err)
	}
	return token, r, nil
}

// getPodServiceAccountReference retrieves the ServiceAccount reference
// for the Pod associated with the request by looking up the client IP
// address.
// If there's an error this function sends the response to the client.
func (s *Server) getPodServiceAccountReference(w http.ResponseWriter,
	r *http.Request) (*serviceaccounts.Reference, *http.Request, error) {

	// check if the pod service account reference is already in the request
	ctx := r.Context()
	if v := ctx.Value(podServiceAccountReferenceContextKey{}); v != nil {
		return v.(*serviceaccounts.Reference), r, nil
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

	// fetch pod by ip
	pod, err := s.lookupPodByIP(ctx, clientIP)
	if err != nil {
		if strings.Contains(err.Error(), "no pods found") {
			return s.getNodeServiceAccountReference(w, r, clientIP)
		}
		const format = "error looking up pod by ip address: %w"
		pkghttp.RespondErrorf(w, r, retry.HTTPStatusCode(err), format, err)
		return nil, nil, fmt.Errorf(format, err)
	}

	// update context, logger and request
	saRef := serviceaccounts.ReferenceFromPod(pod)
	ctx = context.WithValue(ctx, podServiceAccountReferenceContextKey{}, saRef)
	l = l.WithField("pod", logrus.Fields{
		"name":                 pod.Name,
		"namespace":            pod.Namespace,
		"service_account_name": pod.Spec.ServiceAccountName,
	})
	r = logging.IntoRequest(r.WithContext(ctx), l)

	return saRef, r, nil
}

// getNodeServiceAccountReference retrieves the reference of the ServiceAccount that should
// be used by the Node, but only if the client IP address is the same as the Node's IP address.
// If there's an error this function sends the response to the client.
func (s *Server) getNodeServiceAccountReference(w http.ResponseWriter,
	r *http.Request, clientIP string) (*serviceaccounts.Reference, *http.Request, error) {

	ctx := r.Context()

	// get current node
	node, err := s.getCurrentNode(ctx)
	if err != nil {
		const format = "error getting current node: %w"
		pkghttp.RespondErrorf(w, r, retry.HTTPStatusCode(err), format, err)
		return nil, nil, fmt.Errorf(format, err)
	}

	// check if client ip matches the current node's pod cidr
	var found bool
	var errs []error
	podIP := net.ParseIP(clientIP)
	podCIDRs := node.Spec.PodCIDRs
	if len(podCIDRs) == 0 {
		podCIDRs = []string{node.Spec.PodCIDR}
	}
	for _, c := range podCIDRs {
		if c == "" {
			continue
		}
		_, podCIDR, err := net.ParseCIDR(c)
		if err != nil {
			errs = append(errs, fmt.Errorf("error parsing pod cidr %s: %w", c, err))
			continue
		}
		if podCIDR.Contains(podIP) {
			found = true
			break
		}
	}
	if !found {
		if len(errs) == 0 {
			const format = "client ip address %s does not match any pods running on node %s"
			pkghttp.RespondErrorf(w, r, http.StatusForbidden, format, clientIP, node.Name)
			return nil, nil, fmt.Errorf(format, clientIP, node.Name)
		}
		err := errs[0]
		if len(errs) > 1 {
			err = errors.Join(errs...)
		}
		const format = "client ip address %s does not match any pods running on node %s (errors: %w)"
		pkghttp.RespondErrorf(w, r, http.StatusForbidden, format, clientIP, node.Name, err)
		return nil, nil, fmt.Errorf(format, clientIP, node.Name, err)
	}

	// get node service account reference
	saRef := serviceaccounts.ReferenceFromNode(node)
	if saRef == nil {
		const format = "node does not have the service account annotations/labels: %s"
		pkghttp.RespondErrorf(w, r, http.StatusForbidden, format, node.Name)
		return nil, nil, fmt.Errorf(format, node.Name)
	}

	// update context, logger and request
	ctx = context.WithValue(ctx, podServiceAccountReferenceContextKey{}, saRef)
	l := logging.FromContext(ctx).WithField("node", logrus.Fields{
		"name": node.Name,
		"service_account": logrus.Fields{
			"name":      saRef.Name,
			"namespace": saRef.Namespace,
		},
	})
	r = logging.IntoRequest(r.WithContext(ctx), l)

	return saRef, r, nil
}

func (s *Server) lookupPodByIP(ctx context.Context, clientIP string) (*corev1.Pod, error) {
	lookupPodFailures := s.metrics.lookupPodFailures.WithLabelValues(clientIP)

	var pod *corev1.Pod
	err := retry.Do(ctx, retry.Operation{
		Description:    "lookup pod by ip address",
		FailureCounter: lookupPodFailures,
		Func: func() error {
			var err error
			pod, err = s.opts.Pods.GetByIP(ctx, clientIP)
			return err
		},
		IsRetryable: func(err error) bool {
			s := err.Error()
			return !strings.Contains(s, "no pods found") && !strings.Contains(s, "multiple pods found")
		},
	})

	return pod, err
}

func (s *Server) getCurrentNode(ctx context.Context) (*corev1.Node, error) {
	getNodeFailures := s.metrics.getNodeFailures

	var node *corev1.Node
	err := retry.Do(ctx, retry.Operation{
		Description:    "get the current node",
		FailureCounter: getNodeFailures,
		Func: func() error {
			var err error
			node, err = s.opts.Node.Get(ctx)
			return err
		},
		IsRetryable: func(err error) bool {
			return true
		},
	})

	return node, err
}
