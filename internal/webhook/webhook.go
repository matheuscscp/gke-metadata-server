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

package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	pkghttp "github.com/matheuscscp/gke-metadata-server/internal/http"
	"github.com/matheuscscp/gke-metadata-server/internal/logging"
	"github.com/matheuscscp/gke-metadata-server/internal/metrics"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/sirupsen/logrus"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
)

type ServerOptions struct {
	ServerAddr       string
	InitNetworkImage string
	DaemonSetPort    string
	MetricsRegistry  *prometheus.Registry
}

type Server struct {
	httpServer *http.Server
}

const (
	certsDir = "/etc/gke-metadata-server/certs"
	certFile = certsDir + "/tls.crt"
	keyFile  = certsDir + "/tls.key"
)

func New(ctx context.Context, opts ServerOptions) *Server {
	l := logging.FromContext(ctx).WithField("server_addr", opts.ServerAddr)

	httpServer := &http.Server{
		Addr:    opts.ServerAddr,
		Handler: mutateHandler(opts),
		BaseContext: func(net.Listener) context.Context {
			return logging.IntoContext(context.Background(), l)
		},
	}

	go func() {
		l.Info("starting webhook...")
		if err := httpServer.ListenAndServeTLS(certFile, keyFile); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.WithError(err).Fatal("error listening and serving webhook")
		}
	}()

	return &Server{httpServer}
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.httpServer.Shutdown(ctx)
}

func mutateHandler(opts ServerOptions) http.HandlerFunc {
	const subsystem = "webhook"
	labelNames := []string{"status"}
	latencyMillis := metrics.NewLatencyMillis(subsystem, labelNames...)
	opts.MetricsRegistry.MustRegister(latencyMillis)
	observeLatencyMillis := func(r *http.Request, statusCode int, latencyMs float64) {
		latencyMillis.WithLabelValues(fmt.Sprint(statusCode)).Observe(latencyMs)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		r = pkghttp.InitRequest(r, observeLatencyMillis)

		admissionReview := &admissionv1.AdmissionReview{}
		if err := json.NewDecoder(r.Body).Decode(admissionReview); err != nil {
			pkghttp.RespondErrorf(w, r, http.StatusBadRequest, "error decoding admission review: %w", err)
			return
		}

		var pod struct {
			Metadata struct {
				Name      string `json:"name"`
				Namespace string `json:"namespace"`
			} `json:"metadata"`
			Spec struct {
				HostAliases    *[]corev1.HostAlias `json:"hostAliases"`
				InitContainers *[]corev1.Container `json:"initContainers"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(admissionReview.Request.Object.Raw, &pod); err != nil {
			pkghttp.RespondErrorf(w, r, http.StatusBadRequest, "error unmarshaling pod: %w", err)
			return
		}

		// The settings below emulate the HTTP endpoint 169.254.169.254:80, which is
		// hardcoded across Google libraries as the endpoint for detecting whether
		// or not the program is running inside the Google Cloud. The hostAliases
		// field configures the DNS entry. The init container initializes the pod
		// network namespace to route traffic from 169.254.169.254:80 to the
		// DAEMONSET_IP:DAEMONSET_PORT network address, where the emulator will
		// be listening on, on the same node of this pod (hence why the init
		// container requires the NET_ADMIN security capability).
		isHostAliasesPresent := pod.Spec.HostAliases != nil
		isInitContainersPresent := pod.Spec.InitContainers != nil
		patchObject := []struct {
			Op    string `json:"op"`
			Path  string `json:"path"`
			Value any    `json:"value"`
		}{
			{
				Op:   "add",
				Path: getPath(isHostAliasesPresent, "/spec/hostAliases"),
				Value: getValue(isHostAliasesPresent, corev1.HostAlias{
					Hostnames: []string{"metadata.google.internal"},
					IP:        "169.254.169.254",
				}),
			},
			{
				Op:   "add",
				Path: getPath(isInitContainersPresent, "/spec/initContainers"),
				Value: getValue(isInitContainersPresent, corev1.Container{
					Name:  "init-gke-metadata-server",
					Image: opts.InitNetworkImage,
					Args:  []string{"init-network"},
					SecurityContext: &corev1.SecurityContext{
						Capabilities: &corev1.Capabilities{
							Add: []corev1.Capability{"NET_ADMIN"},
						},
					},
					Env: []corev1.EnvVar{
						{
							Name: "DAEMONSET_IP",
							ValueFrom: &corev1.EnvVarSource{
								FieldRef: &corev1.ObjectFieldSelector{
									FieldPath: "status.hostIP",
								},
							},
						},
						{
							Name:  "DAEMONSET_PORT",
							Value: opts.DaemonSetPort,
						},
					},
				}),
			},
		}

		patch, err := json.Marshal(patchObject)
		if err != nil {
			pkghttp.RespondErrorf(w, r, http.StatusInternalServerError, "error marshaling patch: %w", err)
			return
		}

		jsonPatch := admissionv1.PatchTypeJSONPatch
		admissionReview.Response = &admissionv1.AdmissionResponse{
			UID:       admissionReview.Request.UID,
			Allowed:   true,
			PatchType: &jsonPatch,
			Patch:     patch,
		}

		pkghttp.RespondJSON(w, r, http.StatusOK, admissionReview)

		podRef := logrus.Fields{
			"name":      pod.Metadata.Name,
			"namespace": pod.Metadata.Namespace,
		}
		logging.FromRequest(r).WithField("pod", podRef).Info("pod mutated")
	}
}

func getPath(isListPresent bool, listPath string) string {
	if isListPresent {
		return listPath + "/-"
	}
	return listPath
}

func getValue(isListPresent bool, listItem any) any {
	if isListPresent {
		return listItem
	}
	return []any{listItem}
}
