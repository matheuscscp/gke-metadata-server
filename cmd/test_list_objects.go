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
	"fmt"

	"github.com/matheuscscp/gke-metadata-server/internal/logging"

	"cloud.google.com/go/storage"
	"github.com/spf13/cobra"
	"google.golang.org/api/iterator"
)

func newTestListObjectsCommand() *cobra.Command {
	var bucket string

	cmd := &cobra.Command{
		Use:   "test-list-objects",
		Short: "Tool for testing the project in CI",
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			ctx := cmd.Context()
			l := logging.FromContext(ctx)
			defer func() {
				if runtimeErr := err; err != nil {
					err = nil
					l.WithError(runtimeErr).Fatal("runtime error")
				}
			}()

			client, err := storage.NewClient(ctx)
			if err != nil {
				return fmt.Errorf("error creating gcs client: %w", err)
			}
			defer client.Close()

			it := client.Bucket(bucket).Objects(ctx, &storage.Query{})
			for o, err := it.Next(); err != iterator.Done; o, err = it.Next() {
				if err != nil {
					return fmt.Errorf("error listing objects in bucket '%s': %w", bucket, err)
				}
				fmt.Println(o.Name)
			}

			return nil
		},
	}

	cmd.Flags().StringVar(&bucket, "bucket", "", "Bucket to be described.")

	return cmd
}
