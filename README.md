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
Federation work together by themselves. This tool just makes your Pods need less configuration
to use GCP Workload Identity Federation (some level of configuration is still required, but the
full Workload Identity Provider resource name is hidden from the client Pods and kept only as part
of the emulator, see a full example at [`./k8s/test-pod.yaml`](./k8s/test-pod.yaml)), and the
integration is closer to how native GKE Workload Identity works (but still not perfect, as the
impersonation IAM settings are still slightly different, see the section
[Configure GCP Workload Identity Federation for Kubernetes](#configure-gcp-workload-identity-federation-for-kubernetes)
below).

## Limitations and Caveats

### Pod identification by IP address

The server uses the source IP address reported in the HTTP request to identify the
requesting Pod in the Kubernetes API.

If an attacker can easily perform IP address impersonation attacks in your cluster, e.g.
[ARP spoofing](https://cloud.hacktricks.xyz/pentesting-cloud/kubernetes-security/kubernetes-network-attacks),
then they will most likely exploit this design choice to steal your credentials.
**Please evaluate the risk of such attacks in your cluster before choosing this tool.**

*(Please also note that the attack explained in the link above requires Pods configured
with very high privileges, which should normally not be allowed in sensitive/production
clusters. If the security of your cluster is really important, then you should be
[enforcing restrictions](https://github.com/open-policy-agent/gatekeeper) for preventing
Pods from being created with such high privileges, except in special/trusted cases.)*

### Pods running on the host network

In a cluster there may also be Pods running on the *host network*, i.e. Pods with the
field `spec.hostNetwork` set to `true`. The emulator Pods themselves, for example, need
to run on the host network in order to listen to TCP/IP connections coming from Pods
running on the same Kubernetes Node, such that their unecrypted communication never
leaves that Node. Because Pods running on the host network use a shared IP address, i.e.
the IP address of the Node itself where they are running on, the solution implemented
here is not be able to uniquely identify such Pods. Therefore, *Pods running on the host
network are not supported*.

*(Please also note that most Pods should not run on the host network in the first place,
as there is usually no need for this. Most applications are, and should be able to fulfill
their purposes without running on the host network. This is only required for very special
cases, like in the case of the emulator itself.)*

## Usage

Steps:
1. Configure Kubernetes DNS
2. Configure GCP Workload Identity Federation for Kubernetes
3. Deploy `gke-metadata-server` in your cluster
4. Verify supply chain authenticity

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

### Configure GCP Workload Identity Federation for Kubernetes

Steps:
1. Create a Workload Identity Pool and Provider pair for your cluster
2. Grant Kubernetes ServiceAccounts permissions to impersonate Google Service Accounts

Official docs and examples:
[link](https://cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes).

More examples for all the configuration described in this section are available here:
[`./terraform/test.tf`](./terraform/test.tf). This is where we provision the
infrastructure required for testing this project in CI and development.

#### Create a Workload Identity Pool and Provider pair for your cluster

In order to map Kubernetes ServiceAccounts to Google Service Accounts, one must first create
a Workload Identity Pool and Provider. A Pool is a namespace for Subjects and Providers,
where Subjects represent the identities to whom permissions to impersonate Google Service
Accounts will be granted, i.e. the Kubernetes ServiceAccounts in this case. The Provider
stores the OIDC configuration parameters retrieved from the cluster for allowing Google to
verify the ServiceAccount JWT Tokens issued by Kubernetes. The Subject is mapped from the
`sub` claim of the JWT. For enforcing a strong authentication system, be sure to create
exactly one Provider per Pool, i.e. create a single Pool + Provider pair for each Kubernetes
cluster.

Specifically for the creation of the Provider, see an example in the
[`./Makefile`](./Makefile), target `kind-cluster`.

**Attention 1**: The Google Subject can have at most 127 characters
([docs](https://registry.terraform.io/providers/hashicorp/google/latest/docs/resources/iam_workload_identity_pool_provider#google.subject)). The format of the `sub` claim in
the ServiceAccount JWT Tokens is `system:serviceaccount:{k8s_namespace}:{k8s_sa_name}`.

**Attention 2**: Please make sure not to specify any *audiences* when creating the Pool
Provider. This project uses the default audience assigned by Google to a Provider when
creating the ServiceAccount Tokens. This default audience contains the full resource
name of the Provider, which is a good/strict security rule:

`//iam.googleapis.com/{pool_full_name}/providers/{pool_provider_short_name}`

Where `{pool_full_name}` has the form:

`projects/{gcp_project_number}/locations/global/workloadIdentityPools/{pool_short_name}`

The Pool full name can be retrieved on its Google Cloud Console webpage.

#### Grant Kubernetes ServiceAccounts permissions to impersonate Google Service Accounts

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

This "self-impersonation" IAM Policy Binding is optional, and only necessary for the
`GET /computeMetadata/v1/instance/service-accounts/*/identity` API to work.

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

### Verify supply chain authenticity

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
