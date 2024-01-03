# gke-metadata-server

![release](https://img.shields.io/github/v/release/matheuscscp/gke-metadata-server?style=flat-square&color=blue)
[![release](https://github.com/matheuscscp/gke-metadata-server/actions/workflows/release.yml/badge.svg?branch=main)](https://github.com/matheuscscp/gke-metadata-server/actions/workflows/release.yml)

A GKE Metadata Server *emulator* for making it easier to use GCP Workload Identity Federation
inside non-GKE Kubernetes clusters, e.g. on-prem, bare-metal, managed Kubernetes from other
clouds, etc. This implementation is heavily inspired by, and deployed in the same fashion of
the Google-managed `gke-metadata-server` `DaemonSet` present in the `kube-system` Namespace of
GKE clusters with GKE Workload Identity enabled. See how GKE Workload Identity
[works](https://cloud.google.com/kubernetes-engine/docs/concepts/workload-identity#metadata_server).

**Disclaimer 1:** This project was not created by Google or by anybody related to Google.
Google enterprise support is not available. **Use this tool at your own risk.**
*(But please do feel free to open issues for reporting bugs or vulnerabilities, or for asking
questions, help or requesting new features. Feel also free to
[contribute](https://github.com/matheuscscp/gke-metadata-server/issues?q=is%3Aopen+is%3Aissue+label%3A%22good+first+issue%22).)*

**Disclaimer 2:** This tool is *not necessary* for using GCP Workload Identity Federation inside
non-GKE Kubernetes clusters. This is just a facilitator. Kubernetes and GCP Workload Identity
Federation work together by themselves. This tool just makes it so your Pods need less
configuration to use Workload Identity Federation (some level of configuration is still required,
but, for example, the full Workload Identity Provider resource name is hidden from the
client Pods and kept only as part of the emulator, see a full example at
[`./k8s/test-pod.yaml`](./k8s/test-pod.yaml)), and the integration is closer to how native GKE
Workload Identity works (but still not perfect, as the impersonation IAM settings are still slightly
different, see the
[Configure GCP Workload Identity Federation for Kubernetes](#configure-gcp-workload-identity-federation-for-kubernetes)
section below).

## Limitations and Caveats

### Pod identification by IP address

The server uses the source IP address reported in the HTTP request to identify the
requesting Pod in the Kubernetes API through the following `client-go` API call:

```go
func (s *Server) tryGetPod(...) {
    // clientIP is extracted and parsed from r.RemoteAddr
    podList, err := s.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{
        FieldSelector: fmt.Sprintf("spec.nodeName=%s,status.podIP=%s", s.opts.NodeName, clientIP),
    })
    // now filter Pods with spec.hostNetwork==false (not supported in the FieldSelector above)
    // and check if exactly one pod remains. if yes, then serve the requested credentials
}
```

If your cluster has an easy surface for an attacker to impersonate a Pod IP address, maybe via [ARP
spoofing](https://cloud.hacktricks.xyz/pentesting-cloud/kubernetes-security/kubernetes-network-attacks),
then the attacker may exploit this behavior to steal credentials from the emulator.
**Please evaluate the risk of such attacks in your cluster before choosing this tool.**

*(Please also note that the attack explained in the link above requires Pods configured
with very high privileges. If you are currently
[capable of enforcing restrictions](https://github.com/open-policy-agent/gatekeeper)
and preventing that kind of configuration then you should be able to greatly reduce the
attack surface.)*

### Pods running on the host network

In a cluster there can also be Pods running on the *host network*, i.e. Pods with the
field `spec.hostNetwork` set to `true`. For example, the emulator itself needs to run in
that mode in order to listen to TCP/IP connections coming from Pods running on the same
Kubernetes Node the emulator Pod is running on. Since Pods running on the host network
share the same IP address, i.e. the IP address of the Node itself where they are running
on, the solution implemented here would not be able to uniquely and securely identify
such Pods by IP address. Therefore, *Pods running on the host network are not supported*.

## Usage

Steps:
1. Configure Kubernetes DNS
2. Configure Kubernetes ServiceAccount OIDC Discovery
3. Configure GCP Workload Identity Federation for Kubernetes
4. Deploy `gke-metadata-server` in your cluster
5. Verify image signatures

### Configure Kubernetes DNS

Add the following DNS entry to your cluster:

`metadata.google.internal` ---> `169.254.169.254`

Google libraries and `gcloud` query these well-known endpoints for retrieving Google OAuth 2.0
access tokens, which are short-lived (1h-long) authorization codes granting access to resources
in the GCP APIs.

#### CoreDNS

If your cluster uses CoreDNS, here's a StackOverflow [tutorial](https://stackoverflow.com/a/65338650)
for adding custom cluster-level DNS entries.

Adding an entry to CoreDNS does not work seamlessly for all cases. Depending on how the
application code resolves DNS, the Pod-level DNS configuration mentioned in the link
above may be the only feasible choice.

*(Google's Go libraries target the `169.254.169.254` IP address directly. If you are running mostly
Go applications *authenticating through Google's Go libraries* then this DNS configuration may not
be required. Test it!)*

### Configure Kubernetes ServiceAccount OIDC Discovery

Kubernetes supports configuring the OIDC discovery endpoints for the ServiceAccount OIDC Identity Provider
([docs](https://kubernetes.io/docs/tasks/configure-pod-container/configure-service-account/#service-account-issuer-discovery)).

A relying party interested in validating ID tokens for Kubernetes ServiceAccounts (e.g. `sts.googleapis.com`)
first requests the OIDC configuration from `$ISSUER_URI/.well-known/openid-configuration`. The returned JSON
has a field called `jwks_uri` containing a URI for the *JSON Web Key Sets* document, usually of the form
`$ISSUER_URI/openid/v1/jwks`. This second JSON document has the public cryptographic keys that must be used
for verifying the signatures of ServiceAccount Tokens issued by Kubernetes.

The Kubernetes API serves these two documents, but since both can be publicly available it's much safer
to store and serve them from a reliable, publicly and highly available endpoint where GCP will guaranteed
be able to discover the authentication parameters from. For example, public GCS/S3 buckets/objects, etc.

The CLI of the project offers the command `publish` for automatically fetching and uploading the two required
JSON documents to a GCS bucket. This command will try to retrieve the Kubernetes and Google credentials from
the environment where it runs on, e.g. `make dev-cluster` uses the local credentials of a developer of the
project, while `make ci-cluster` uses the kubeconfig created by KinD in the GitHub Workflow
[`./.github/workflows/pull-request.yml`](./.github/workflows/pull-request.yml) and the Google credentials
obtained via Workload Identity Federation for this GitHub repository
([`./.github/workflows/bootstrap.tf`](./.github/workflows/bootstrap.tf)).

Alternatively, this is how you could retrieve these two JSON documents from inside a Pod using `curl`:

```bash
curl -s --cacert /var/run/secrets/kubernetes.io/serviceaccount/ca.crt \
    -H "Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" \
    "https://kubernetes.default.svc.cluster.local/.well-known/openid-configuration"
curl -s --cacert /var/run/secrets/kubernetes.io/serviceaccount/ca.crt \
    -H "Authorization: Bearer $(cat /var/run/secrets/kubernetes.io/serviceaccount/token)" \
    "https://kubernetes.default.svc.cluster.local/openid/v1/jwks"
```

Copy the outputs of the two `curl` commands above into files named respectively `openid-config.json`
and `openid-keys.json`, then run the following commands to upload them to GCS:

```bash
gcloud storage cp openid-config.json gs://$ISSUER_GCS_URI/.well-known/openid-configuration
gcloud storage cp openid-keys.json   gs://$ISSUER_GCS_URI/openid/v1/jwks
```

Where `$ISSUER_GCS_URI` is either just a GCS bucket name, or `$BUCKET_NAME/$KEY_PREFIX` where `$KEY_PREFIX`
is a prefix for the GCS object keys, useful for example for storing multiple OIDC configurations (e.g. for
multiple Kubernetes clusters) together in a single GCS bucket. The public HTTPS URLs of the upload objects
will be respectively:

```bash
echo "https://storage.googleapis.com/$ISSUER_GCS_URI/.well-known/openid-configuration"
echo "https://storage.googleapis.com/$ISSUER_GCS_URI/openid/v1/jwks"
```

Configuring the OIDC Issuer and JWKS URIs usually implies restarting the Kubernetes Control Plane for
specifying the required API server binary arguments (e.g. see the KinD development configuration at
[`./k8s/test-kind-config.yaml`](./k8s/test-kind-config.yaml)).

```bash
# the --service-account-issuer k8s API server CLI flag will be the following (short URL form,
# no need for the /.well-known/openid-configuration suffix):
echo "https://storage.googleapis.com/$ISSUER_GCS_URI"

# the --service-account-jwks-uri k8s API server CLI flag will be the following (full URL form):
echo "https://storage.googleapis.com/$ISSUER_GCS_URI/openid/v1/jwks"
```

### Configure GCP Workload Identity Federation for Kubernetes

Steps:
1. Configure Pool and Provider
2. Configure Service Account Impersonation for Kubernetes

Docs: [link](https://cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes).

Examples for all the configurations described in this section are available here:
[`./terraform/test.tf`](./terraform/test.tf). This is where we provision the
infrastructure required for testing this project in CI and development.

#### Pool and Provider

In order to map Kubernetes ServiceAccounts to Google Service Accounts, one must first create
a Workload Identity Pool and Provider. A Pool is a set of Subjects and a set of Providers,
with each Subject being visible to all the Providers in the set. For enforcing a strict
authentication system, be sure to create exactly one Provider per Pool, i.e. create a single
Pool + Pool Provider pair for each Kubernetes cluster. This Provider must reflect the Kubernetes
ServiceAccounts OIDC Identity provider configured in the previous step. The Issuer URI will
be required (e.g. the HTTPS URL of a publicly available GCS bucket containing an object at
key `.well-known/openid-configuration`), and the following *attribute mapping* rule must be
created:

`google.subject = assertion.sub`

If this configuration is correct, then the Subject mapped by Google from a Kubernetes ServiceAccount
Token will have the following syntax:

`system:serviceaccount:{k8s_namespace}:{k8s_sa_name}`

**Attention 1**: A `google.subject` can have at most 127 characters
([docs](https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/iam_workload_identity_pool_provider#google.subject)).

**Attention 2**: Please make sure not to specify any *audiences*. This project uses the
default audience when creating Kubernetes ServiceAccount Tokens (which contains the full
resource name of the Pool Provider, which is a good, strict security rule):

`//iam.googleapis.com/{pool_full_name}/providers/{pool_provider_name}`

Where `{pool_full_name}` assumes the form:

`projects/{gcp_project_number}/locations/global/workloadIdentityPools/{pool_name}`

The Pool full name can be retrieved on its Google Cloud Console webpage.

#### Service Account Impersonation for Kubernetes

For allowing the `{gcp_service_account}@{gcp_project_id}.iam.gserviceaccount.com` Google Service Account
to be impersonated by the Kubernetes ServiceAccount `{k8s_sa_name}` of Namespace `{k8s_namespace}`,
create an IAM Policy Binding for the IAM Role `roles/iam.workloadIdentityUser` and the following membership
string on the respective Google Service Account:

`principal://iam.googleapis.com/{pool_full_name}/subject/system:serviceaccount:{k8s_namespace}:{k8s_sa_name}`

This membership encoding the Kubernetes ServiceAccount will be reflected as a Subject in the Google Cloud
Console webpage of the Pool. It allows a Kubernetes ServiceAccount Token issued for the *audience* of the
Pool Provider (see previous section) to be exchanged for an OAuth 2.0 Access Token for the Google Service
Account.

Add also a second IAM Policy Binding for the IAM Role `roles/iam.serviceAccountOpenIdTokenCreator` and the
membership string below *on the Google Service Account itself*:

`serviceAccount:{gcp_service_account}@{gcp_project_id}.iam.gserviceaccount.com`

This "self-impersonation" IAM Policy Binding is necessary for the
`GET /computeMetadata/v1/instance/service-accounts/*/identity` API to work.
This is because our implementation first exchanges the Kubernetes ServiceAccount
Token for a Google Service Account OAuth 2.0 Access Token, and then exchanges
this Access Token for an OpenID Connect ID Token. *(All these tokens are short-lived.)*

Finally, add the following annotation to the Kuberentes ServiceAccount (just like you would in GKE):

`iam.gke.io/gcp-service-account: {gcp_service_account}@{gcp_project_id}.iam.gserviceaccount.com`

If this bijection between the Google Service Account and the Kubernetes ServiceAccount is correctly
configured and `gke-metadata-server` is properly deployed in your cluster, you're good to go.

### Deploy `gke-metadata-server` in your cluster

A Helm Chart is available in the following [Helm OCI Repository](https://helm.sh/docs/topics/registries/):

`ghcr.io/matheuscscp/gke-metadata-server-helm:{helm_version}` (GitHub Container Registry)

Where `{helm_version}` is a Helm Chart SemVer, i.e. the field `.version` at
[`./helm/gke-metadata-server/Chart.yaml`](./helm/gke-metadata-server/Chart.yaml).

See the Helm values API at [`./helm/gke-metadata-server/values.yaml`](./helm/gke-metadata-server/values.yaml).

Alternatively, you can write your own Kubernetes manifests and consume only the container image:

`ghcr.io/matheuscscp/gke-metadata-server:{container_version}` (GitHub Container Registry)

Where `{container_version}` is the app version, i.e. the field `.appVersion` at
[`./helm/gke-metadata-server/Chart.yaml`](./helm/gke-metadata-server/Chart.yaml).

### Verify image signatures

For manually verifying the images above use the [`cosign`](https://github.com/sigstore/cosign)
CLI tool.

For verifying the image of a given Container GitHub Release (tags `v{container_version}`), fetch the
digest file `container-digest.txt` attached to the release and use it with `cosign`:

```bash
cosign verify ghcr.io/matheuscscp/gke-metadata-server@$(cat container-digest.txt) \
    --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
    --certificate-identity=https://github.com/matheuscscp/gke-metadata-server/.github/workflows/release.yml@refs/heads/main
```

For verifying the image of a given Helm Chart GitHub Release (tags `helm-v{helm_version}`), fetch the
digest file `helm-digest.txt` attached to the release and use it with `cosign`:

```bash
cosign verify ghcr.io/matheuscscp/gke-metadata-server-helm@$(cat helm-digest.txt) \
    --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
    --certificate-identity=https://github.com/matheuscscp/gke-metadata-server/.github/workflows/release.yml@refs/heads/main
```

If you are using FluxCD for deploying Helm Charts, use
[Keyless Verification](https://fluxcd.io/flux/components/source/helmcharts/#keyless-verification).

If you are using Kyverno for enforcing policies, use
[Keyless Verification](https://kyverno.io/docs/writing-policies/verify-images/sigstore/#keyless-signing-and-verification).
