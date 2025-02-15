@if(debug)

package main

// Values used by debug_tool.cue.
// Debug example 'cue cmd -t debug -t name=test -t namespace=test -t mv=1.0.0 -t kv=1.28.0 build'.
values: settings: {
	projectID:                "my-gcp-project-id"
	workloadIdentityProvider: "projects/123456789012/locations/global/workloadIdentityPools/debug-pool/providers/debug-provider"
}
