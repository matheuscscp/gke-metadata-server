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

package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"cloud.google.com/go/storage"
	"github.com/spf13/cobra"
)

func newPublishCommand() *cobra.Command {
	var bucket string
	var keyPrefix string

	cmd := &cobra.Command{
		Use:   "publish",
		Short: "Publish the Kubernetes ServiceAccount OIDC Discovery configuration to a GCS bucket",
		Long:  "Publish the Kubernetes ServiceAccount OIDC Discovery configuration to a GCS bucket for use with GCP Workload Identity",
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			bucket, keyPrefix = strings.TrimSpace(bucket), strings.TrimSpace(keyPrefix)
			if bucket == "" {
				return fmt.Errorf("bucket not specified")
			}

			ctx := cmd.Context()
			l := logging.FromContext(ctx)
			defer func() {
				if runtimeErr := err; err != nil {
					err = nil
					l.WithError(runtimeErr).Fatal("runtime error")
				}
			}()

			storageClient, err := storage.NewClient(ctx)
			if err != nil {
				return fmt.Errorf("error creating storage client: %w", err)
			}
			defer storageClient.Close()
			bkt := storageClient.Bucket(bucket)

			_, kubeConfig, err := createKubernetesClient(ctx)
			if err != nil {
				return fmt.Errorf("error creating k8s client: %w", err)
			}

			// create kube http client
			var certificates []tls.Certificate
			if len(kubeConfig.TLSClientConfig.CertData) > 0 {
				cert, err := tls.X509KeyPair(
					kubeConfig.TLSClientConfig.CertData,
					kubeConfig.TLSClientConfig.KeyData,
				)
				if err != nil {
					return fmt.Errorf("error creating tls cert for talking to k8s: %w", err)
				}
				certificates = append(certificates, cert)
			}
			caCertPool := x509.NewCertPool()
			caCertPool.AppendCertsFromPEM(kubeConfig.TLSClientConfig.CAData)
			httpClient := &http.Client{
				Transport: &http.Transport{
					TLSClientConfig: &tls.Config{
						RootCAs:      caCertPool,
						Certificates: certificates,
					},
				},
			}

			fmt.Println()

			const jwksURI = "openid/v1/jwks"
			for _, key := range []string{".well-known/openid-configuration", jwksURI} {
				// request document to control plane
				url := fmt.Sprintf("%s/%s", kubeConfig.Host, key)
				req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
				if err != nil {
					return fmt.Errorf("error creating request for url '%s' to k8s control plane: %w", url, err)
				}
				if token := kubeConfig.BearerToken; token != "" {
					req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
				}
				resp, err := httpClient.Do(req)
				if err != nil {
					return fmt.Errorf("error performing request for url '%s' to k8s control plane: %w", url, err)
				}
				defer resp.Body.Close()
				var body any
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					return fmt.Errorf("error unmarshaling response for url '%s': %w", url, err)
				}
				if resp.StatusCode != http.StatusOK {
					l = l.WithField("api_error", body)
					return fmt.Errorf("unexpected status code from k8s api server for url '%s': %w", url, err)
				}

				// upload to GCS
				objKey := filepath.Join(keyPrefix, key)
				w := bkt.Object(objKey).NewWriter(ctx)
				enc := json.NewEncoder(w)
				enc.SetIndent("", "  ")
				if err := enc.Encode(body); err != nil {
					return fmt.Errorf("error uploading %s to bucket %s: %w", objKey, bucket, err)
				}
				if err := w.Close(); err != nil {
					return fmt.Errorf("error closing %s upload to bucket %s: %w", objKey, bucket, err)
				}
				fmt.Printf("Object gs://%s/%s sucessfully uploaded.\n", bucket, objKey)
			}

			bktURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, keyPrefix)
			fmt.Println()
			fmt.Println("Use the values below for configuring your Kubernetes API Server.")
			fmt.Println()
			fmt.Printf("Issuer URI (--service-account-issuer):   %s\n", bktURL)
			fmt.Printf("JWKS URI   (--service-account-jwks-uri): %s/%s\n", bktURL, jwksURI)
			fmt.Println()

			return nil
		},
	}

	cmd.Flags().StringVar(&bucket, "bucket", "", "Name of the bucket where the OIDC Discovery config will be publish")
	cmd.Flags().StringVar(&keyPrefix, "key-prefix", "", "Prefix to prepend to the object keys")

	return cmd
}
