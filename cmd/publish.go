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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"cloud.google.com/go/storage"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
)

const jwksURI = "openid/v1/jwks"

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

			kubeClient, err := createKubernetesClient(ctx)
			if err != nil {
				return fmt.Errorf("error creating k8s client: %w", err)
			}

			fmt.Println()

			for _, key := range []string{".well-known/openid-configuration", jwksURI} {
				if err := publishDocument(ctx, kubeClient, storageClient, bucket, keyPrefix, key, os.Stdout); err != nil {
					return err
				}
			}

			bktURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", bucket, keyPrefix)
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

func publishDocument(ctx context.Context, kubeClient *kubernetes.Clientset,
	storageClient *storage.Client, bucket, keyPrefix, key string, stdout io.Writer) error {
	// request document to control plane
	b, err := kubeClient.RESTClient().Get().AbsPath(key).Do(ctx).Raw()
	if err != nil {
		return fmt.Errorf("error requesting document '%s' to k8s control plane: %w", key, err)
	}
	var body any
	if err := json.Unmarshal(b, &body); err != nil {
		return fmt.Errorf("error unmarshaling response for document '%s': %w", key, err)
	}

	// upload to GCS
	objKey := filepath.Join(keyPrefix, key)
	w := storageClient.Bucket(bucket).Object(objKey).NewWriter(ctx)
	enc := json.NewEncoder(io.MultiWriter(w, stdout))
	enc.SetIndent("", "  ")
	fmt.Fprintf(stdout, "Document %s successfully retrieved from the Kubernetes API Server:\n\n", key)
	if err := enc.Encode(body); err != nil {
		return fmt.Errorf("error uploading %s to bucket %s: %w", objKey, bucket, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("error closing %s upload to bucket %s: %w", objKey, bucket, err)
	}
	fmt.Fprintf(stdout, "\nObject gs://%s/%s sucessfully uploaded.\n\n", bucket, objKey)
	return nil
}
