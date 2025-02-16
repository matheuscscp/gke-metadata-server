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

package templates

import (
	"time"
)

// #Settings is the schema for the gke-metadata-server application settings.
#Settings: {
	// projectID is the mandatory GCP project ID.
	projectID: string

	// workloadIdentityProvider is the mandatory fully-qualified name of the GCP Workload Identity Provider.
	// This full name can be retrieved on the Google Cloud Console webpage for the provider.
	workloadIdentityProvider: string & =~"^projects/\\d+/locations/global/workloadIdentityPools/[^/]+/providers/[^/]+$"

	// logLevel is the log level for gke-metadata-server.
	logLevel?: string & ("panic" | "fatal" | "error" | "warning" | "info" | "debug" | "trace")

 	// serverPort is the TCP port for gke-metadata-server to listen HTTP on.
	serverPort: int & >0 & <65536 | *8080

	// watchPods is the watch settings for gke-metadata-server to watch Pods running on the same Node.
	watchPods: #watchSettings

	// watchNode is the watch settings for gke-metadata-server to watch the Node where it is running on.
	watchNode: #watchSettings

	// watchServiceAccounts is the watch settings for gke-metadata-server to watch all the ServiceAccounts in the cluster.
	watchServiceAccounts: #watchSettings

	// cacheTokens is the settings for caching the GCP tokens.
	cacheTokens: {
		// enable is a flag to enable the cache tokens feature.
		enable: bool | *true

		// concurrency is the number of concurrent workers to cache the GCP tokens.
		concurrency?: int & >0
	}

	// Helper definitions.
	#watchSettings: {
		// enable is a flag to enable the watch feature.
		enable: bool | *true

		// disableFallback disables the "get" fallback method for a "watch" feature.
		disableFallback: bool | *false

		// resyncPeriod is the resync period for the watch feature.
		resyncPeriod?: time.Duration
	}
}
