// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

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
	serverPort: int & >0 & <65536 | *16321

 	// healthPort is the TCP port for the health server to listen HTTP on.
	healthPort: int & >0 & <65536 | *16322

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

		// maxTokenDuration is the maximum duration for cached service account tokens.
		maxTokenDuration?: time.Duration
	}

	// podLookup is the settings for looking up Pods by client connection IP address.
	podLookup: {
		// maxAttempts is the maximum number of attempts to try looking up a pod by the client connection IP address.
		maxAttempts?: int & >0

		// retryInitialDelay is the initial delay for retrying pod lookups upon failures.
		retryInitialDelay?: time.Duration

		// retryMaxDelay is the maximum delay for retrying pod lookups upon failures.
		retryMaxDelay?: time.Duration
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
