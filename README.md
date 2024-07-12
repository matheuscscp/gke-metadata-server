# gke-metadata-server

![release](https://img.shields.io/github/v/release/matheuscscp/gke-metadata-server?style=flat-square&color=blue)
[![release](https://github.com/matheuscscp/gke-metadata-server/actions/workflows/release.yml/badge.svg?branch=main)](https://github.com/matheuscscp/gke-metadata-server/actions/workflows/release.yml)

A GKE Metadata Server *emulator* for making it easier to use GCP Workload Identity Federation
inside *non-GKE* Kubernetes clusters, e.g. KinD, on-prem, managed Kubernetes from other
clouds, etc. This implementation tries to mimic the `gke-metadata-server` `DaemonSet` deployed
automatically by Google in the `kube-system` namespace of GKE clusters that have the feature
*Workload Identity* enabled. See how it [works](https://cloud.google.com/kubernetes-engine/docs/concepts/workload-identity#metadata_server).

## Usage

Steps:
1. Install [cert-manager](https://cert-manager.io/docs/installation/) in the cluster. This dependency is used for bootstrapping a self-signed CA and TLS certificate for a `MutatingWebhook` that adds networking configurations to the user Pods.
2. Configure GCP Workload Identity Federation for Kubernetes.
3. Deploy `gke-metadata-server` in the cluster using the Workload Identity Provider full name, obtained after step 2.
4. (Optional, Recommended) Verify the supply chain authenticity.
5. See [./k8s/test-pod.yaml](./k8s/test-pod.yaml) for an example of how to configure your Pods.

### Configure GCP Workload Identity Federation for Kubernetes

Steps:
1. Create a Workload Identity Pool and Provider pair for the cluster.
2. Grant Kubernetes ServiceAccounts permissions to impersonate Google Service Accounts.

Official docs and examples:
[link](https://cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes).

More examples for all the configuration described in this section are available here:
[`./terraform/test.tf`](./terraform/test.tf). This is where we provision the
infrastructure required for testing this project in CI and development.

#### Create a Workload Identity Pool and Provider pair for the cluster

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
[`./helm/gke-metadata-server/Chart.yaml`](./helm/gke-metadata-server/Chart.yaml). Check available releases
in the [GitHub Releases Page](https://github.com/matheuscscp/gke-metadata-server/releases).

See the Helm Values API at [`./helm/gke-metadata-server/values.yaml`](./helm/gke-metadata-server/values.yaml).

Alternatively, you can write your own Kubernetes manifests and consume only the container image:

`ghcr.io/matheuscscp/gke-metadata-server:{container_version}` (GitHub Container Registry)

Where `{container_version}` is the app version, i.e. the field `.appVersion` at
[`./helm/gke-metadata-server/Chart.yaml`](./helm/gke-metadata-server/Chart.yaml). Check available releases
in the [GitHub Releases Page](https://github.com/matheuscscp/gke-metadata-server/releases).

### Verify the supply chain authenticity

For verifying the images above use the [`cosign`](https://github.com/sigstore/cosign) CLI tool.

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

If you are using *FluxCD* for deploying Helm Charts you can automate the verification using
[Keyless Verification](https://fluxcd.io/flux/components/source/helmcharts/#keyless-verification).

If you are using *Kyverno* for enforcing policies you can automate the verification using
[Keyless Verification](https://kyverno.io/docs/writing-policies/verify-images/sigstore/#keyless-signing-and-verification).

## Limitations and Security Risks

### Pod identification by IP address

The server uses the client IP address reported in the HTTP request to identify the
requesting Pod in the Kubernetes API (just like in the native GKE implementation).

If an attacker can easily perform IP address impersonation attacks in your cluster, e.g.
[ARP spoofing](https://cloud.hacktricks.xyz/pentesting-cloud/kubernetes-security/kubernetes-network-attacks),
then they will most likely exploit this design choice to steal your credentials.
**Please evaluate the risk of such attacks in your cluster before choosing this tool.**

*(Please also note that the attack explained in the link above requires Pods configured
with very high privileges, which should normally not be allowed in sensitive/production
clusters. If the security of your cluster is really important, then you should be
[enforcing restrictions](https://github.com/open-policy-agent/gatekeeper) for preventing
Pods from being created with such high privileges in the majority of cases.)*

### Pods running on the host network

In a cluster there may also be Pods running on the *host network*, i.e. Pods with the
field `spec.hostNetwork` set to `true`. The emulator Pods themselves, for example, need
to run on the host network in order to listen to TCP/IP connections coming from Pods
running on the same Kubernetes node, which are the ones they will serve. Just like in
the GKE implementation, this design choice makes it such that the (unencrypted)
communication never leaves the Node.

Because Pods running on the host network use a shared IP address, i.e. the IP address of
the Node itself where they are running on, they cannot be uniquely identified by the
server using the client IP address reported in the HTTP request. This is a limitation
of the Kubernetes API, which does not provide a way to identify Pods running on the host
network by IP address.

## Disclaimer

This project was not created by Google. Enterprise support from Google is
not available. **Use this tool at your own risk.**
*(But please do feel free to report bugs and CVEs, to request help and new features, and to
[contribute](https://github.com/matheuscscp/gke-metadata-server/issues?q=is%3Aopen+is%3Aissue+label%3A%22good+first+issue%22).)*

Furthermore, this tool is *not necessary* for using GCP Workload Identity Federation inside
non-GKE Kubernetes clusters. This is just a facilitator. Kubernetes and GCP Workload Identity
Federation work together by themselves. This tool just makes your Pods need much less configuration
to use GCP Workload Identity Federation by making the configuration as close as possible to
how Workload Identity is configured in a native GKE cluster.
