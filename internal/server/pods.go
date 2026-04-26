// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/matheuscscp/gke-metadata-server/api"
	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/retry"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounts"
	"github.com/matheuscscp/gke-metadata-server/internal/serviceaccounttokens"

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
	if v := r.Context().Value(podGoogleServiceAccountEmailContextKey{}); v != nil {
		return v.(*string), r, nil
	}
	saRef, r, err := s.getPodServiceAccountReference(w, r)
	if err != nil {
		return nil, nil, err
	}
	sa, err := s.opts.ServiceAccounts.Get(r.Context(), saRef)
	if err != nil {
		const format = "error getting pod service account: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, err)
		return nil, nil, fmt.Errorf(format, err)
	}
	email, err := serviceaccounts.GoogleServiceAccountEmail(sa)
	if err != nil {
		pkghttp.RespondError(w, r, http.StatusBadRequest, err)
		return nil, nil, err
	}
	ctx := context.WithValue(r.Context(), podGoogleServiceAccountEmailContextKey{}, email)
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
	googleEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
	if err != nil {
		return "", nil, err
	}
	email := s.opts.WorkloadIdentityPool
	if googleEmail != nil {
		email = *googleEmail
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

// getPodGoogleAccessTokens creates a pair of Google Access Tokens for the
// given Pod's ServiceAccount, one for direct access and another one for
// impersonation.
// If there's an error this function sends the response to the client.
func (s *Server) getPodGoogleAccessTokens(w http.ResponseWriter, r *http.Request,
	scopes []string) (*serviceaccounttokens.AccessTokens, time.Time, *http.Request, error) {
	saRef, r, err := s.getPodServiceAccountReference(w, r)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	saToken, _, err := s.opts.ServiceAccountTokens.GetServiceAccountToken(r.Context(), saRef)
	if err != nil {
		const format = "error getting token for pod service account: %w"
		pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, format, err)
		return nil, time.Time{}, nil, fmt.Errorf(format, err)
	}
	googleEmail, r, err := s.getPodGoogleServiceAccountEmail(w, r)
	if err != nil {
		return nil, time.Time{}, nil, err
	}
	tokens, expiresAt, err := s.opts.ServiceAccountTokens.GetGoogleAccessTokens(
		r.Context(), saToken, googleEmail, scopes)
	if err != nil {
		respondGoogleAPIErrorf(w, r, "error getting google access token: %w", err)
		return nil, time.Time{}, nil, err
	}
	return tokens, expiresAt, r, nil
}

// getPodServiceAccountReference retrieves the ServiceAccount reference
// for the Pod associated with the request by looking up the client IP
// address.
// If there's an error this function sends the response to the client.
func (s *Server) getPodServiceAccountReference(w http.ResponseWriter,
	r *http.Request) (*serviceaccounts.Reference, *http.Request, error) {

	// check if the pod service account reference is already in the request
	if v := r.Context().Value(podServiceAccountReferenceContextKey{}); v != nil {
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
	clientHost, clientPortStr, err := net.SplitHostPort(r.RemoteAddr)
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
	l := logging.FromRequest(r).WithField("client_ip", clientIP)
	r = logging.IntoRequest(r, l)

	// Pick the resolution strategy by (routing mode, pod kind). Each
	// (mode, kind) has exactly one strategy — no fallback between paths.
	//
	//   eBPF mode:     kernel attestation for both pod kinds (sockops map).
	//   Loopback/None: source IP for non-hostNetwork; sockdiag for
	//                  hostNetwork (the only way to disambiguate pods that
	//                  share the node IP without an eBPF map).
	//
	// hostNetwork pods on Loopback/None modes don't have a single canonical
	// source IP — the kernel picks one based on the route used to reach the
	// listener (the link-local 169.254.169.254 for Loopback's lo bind, the
	// node IP for None's wildcard bind, possibly 127.0.0.1 if the operator
	// set GCE_METADATA_HOST that way). Anything that isn't a regular
	// routable pod IP is treated as a host-source connection.
	useAttestation := s.opts.RoutingMode == api.RoutingModeBPF || isHostSourceIP(clientIPAddr, s.opts.PodIP)

	var pod *corev1.Pod
	if useAttestation {
		pod, err = s.attestByConnTuple(r, clientIPAddr, clientPortStr)
		if err != nil {
			pkghttp.RespondErrorf(w, r, http.StatusForbidden, "kernel attestation failed: %w", err)
			return nil, nil, fmt.Errorf("kernel attestation failed: %w", err)
		}
	} else {
		pod, err = s.lookupPodByIP(r.Context(), clientIP)
		if err != nil {
			const format = "error looking up pod by ip address: %w"
			pkghttp.RespondErrorf(w, r, retry.HTTPStatusCode(err), format, err)
			return nil, nil, fmt.Errorf(format, err)
		}
	}

	return s.assignPodServiceAccount(r, pod)
}

// assignPodServiceAccount stores the resolved pod's ServiceAccount reference
// on the request context and enriches the logger. Shared between the
// attestation and source-IP resolution paths.
func (s *Server) assignPodServiceAccount(r *http.Request, pod *corev1.Pod) (*serviceaccounts.Reference, *http.Request, error) {
	saRef := serviceaccounts.ReferenceFromPod(pod)
	ctx := context.WithValue(r.Context(), podServiceAccountReferenceContextKey{}, saRef)
	l := logging.FromRequest(r).WithField("pod", logrus.Fields{
		"name":                 pod.Name,
		"namespace":            pod.Namespace,
		"service_account_name": pod.Spec.ServiceAccountName,
	})
	r = logging.IntoRequest(r.WithContext(ctx), l)
	return saRef, r, nil
}

// isHostSourceIP reports whether clientIP looks like a connection from the
// host's network namespace rather than from a pod-network IP. hostNetwork
// pods share their node's network stack, so the kernel-chosen source IP for
// their connections to the daemon is whatever the route picks: the node IP
// (when the listener is on a node-IP-reachable address), the lo-bound
// link-local 169.254.169.254 (Loopback mode), or 127.0.0.1 (when an operator
// points GCE_METADATA_HOST at loopback). None of these are pod-network IPs,
// so this distinguishes hostNetwork from non-hostNetwork callers reliably
// enough for routing-mode dispatch.
func isHostSourceIP(clientIP netip.Addr, podIP string) bool {
	if clientIP.IsLoopback() || clientIP.IsLinkLocalUnicast() {
		return true
	}
	if pip, err := netip.ParseAddr(podIP); err == nil && clientIP == pip {
		return true
	}
	return false
}

// attestByConnTuple resolves the connecting process to its pod via the
// configured kernel-attestation lookuper (BPF sockops map in eBPF mode,
// netlink SOCK_DIAG + /proc walk in Loopback/None) and a UID-based pod
// lookup. Any failure is returned to the caller; there is no fallback by
// design — each (routing mode, pod kind) has exactly one resolution
// strategy.
func (s *Server) attestByConnTuple(r *http.Request, clientIP netip.Addr, clientPortStr string) (*corev1.Pod, error) {
	if s.opts.Attestation == nil {
		return nil, errors.New("attestation lookuper not configured for this routing mode")
	}

	clientPort, err := strconv.ParseUint(clientPortStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("parsing client port %q: %w", clientPortStr, err)
	}

	// Use the kernel-chosen LocalAddr of this connection as the destination
	// of the 4-tuple lookup. This is what the kernel saw when it accepted
	// the connection, so it matches what sockops recorded (eBPF mode) and
	// what netlink SOCK_DIAG sees in the live socket table (Loopback/None).
	// PodIP plus a configured port wouldn't work on Loopback mode where the
	// listener is on 169.254.169.254:80, not on PodIP.
	local := LocalAddrFromRequest(r)
	if local == nil {
		return nil, errors.New("local addr not captured for this connection")
	}
	dstIP, ok := netip.AddrFromSlice(local.IP.To4())
	if !ok {
		return nil, fmt.Errorf("local addr %v is not an IPv4 address", local.IP)
	}

	uid, err := s.opts.Attestation.Lookup(clientIP, dstIP.Unmap(), uint16(clientPort), uint16(local.Port))
	if err != nil {
		return nil, fmt.Errorf("attestation lookup: %w", err)
	}

	pod, err := s.opts.Pods.GetByUID(r.Context(), uid)
	if err != nil {
		return nil, fmt.Errorf("getting pod with uid %s: %w", uid, err)
	}
	return pod, nil
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

		// options
		MaxAttempts:  s.opts.PodLookup.MaxAttempts,
		InitialDelay: s.opts.PodLookup.RetryInitialDelay,
		MaxDelay:     s.opts.PodLookup.RetryMaxDelay,
	})

	return pod, err
}
