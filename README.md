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
1. Install [cert-manager](https://cert-manager.io/docs/installation/) in the cluster.
This dependency is used for bootstrapping self-signed CA and TLS certificates for a `MutatingWebhook`
that adds the required networking configuration to the user Pods.
2. Configure GCP Workload Identity Federation for Kubernetes.
3. Deploy `gke-metadata-server` in the cluster using the Workload Identity Provider full name,
obtained after step 2.
4. See [`./k8s/test-pod.yaml`](./k8s/test-pod.yaml) for an example of how to configure your Pods
and their ServiceAccounts.
5. (Optional but highly recommended) Verify the image signatures to make sure you are
deploying authentic artifacts distributed by this project.

### Configure GCP Workload Identity Federation for Kubernetes

Steps:
1. Create a Workload Identity Pool and Provider pair for the cluster.
2. Grant Kubernetes ServiceAccounts permission to impersonate Google Service Accounts.

Official docs and examples are
[here](https://cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes#kubernetes).

More examples for all the configuration described in this section are available here:
[`./terraform/test.tf`](./terraform/test.tf). This is where we provision the
infrastructure required for testing this project in CI and development.

#### Create a Workload Identity Pool and Provider pair for the cluster

Before granting ServiceAccounts of a Kubernetes cluster permission to impersonate Google
Service Accounts, a Workload Identity Pool and Provider pair must be created for the cluster
in Google Cloud Platform.

A Pool is a namespace for Subjects and Providers, where Subjects represent the identities to
whom permission to impersonate Google Service Accounts are granted, i.e. the Kubernetes
ServiceAccounts in this case.

A Provider stores the OpenID Connect configuration parameters retrieved from the cluster for
allowing Google to verify ServiceAccount Tokens issued by the cluster. Specifically for the
creation of a Provider, see an example in the [`./Makefile`](./Makefile), target
`create-or-update-provider`.

The ServiceAccount Tokens issued by Kubernetes are JWT tokens. Each such token is exchanged
for an OAuth 2.0 Access Token that impersonates a Google Service Account. During a token
exchange, the Subject is mapped from the `sub` claim of the JWT (achieved in the `Makefile`
example by `--attribute-mapping=google.subject=assertion.sub`). In this case the Subject
has the following format:

`system:serviceaccount:{k8s_namespace}:{k8s_sa_name}`

The Subject total length cannot exceed 127 characters
([docs](https://cloud.google.com/iam/docs/reference/rest/v1/projects.locations.workloadIdentityPools.providers#WorkloadIdentityPoolProvider.FIELDS.attribute_mapping)).

**Attention 1:** If you update the private keys of the ServiceAccount Issuer of the cluster
you must update the Provider with the new OpenID Connect configuration, *otherwise service
will be disrupted*.

**Attention 2:** Please make sure not to specify any audiences when creating the Provider.
The emulator uses the *default audience* when issuing the ServiceAccount Tokens. The default
audience contains the full name of the Provider, which is a strong restriction:

`//iam.googleapis.com/{provider_full_name}`

where `{provider_full_name}` has the form:

`{pool_full_name}/providers/{provider_short_name}`

and `{pool_full_name}` has the form:

`projects/{gcp_project_number}/locations/global/workloadIdentityPools/{pool_short_name}`

#### Grant Kubernetes ServiceAccounts permission to impersonate Google Service Accounts

For allowing the Kubernetes ServiceAccount `{k8s_sa_name}` from the namespace `{k8s_namespace}`
to impersonate the Google Service Account `{gcp_service_account}@{gcp_project_id}.iam.gserviceaccount.com`,
grant the IAM Role `roles/iam.workloadIdentityUser` on this Service Account to the following
principal:

`principal://iam.googleapis.com/{pool_full_name}/subject/system:serviceaccount:{k8s_namespace}:{k8s_sa_name}`

This principal will be reflected as a Subject in the Google Cloud Console webpage of the Pool.

If you plan to use the `GET /computeMetadata/v1/instance/service-accounts/default/identity`
API for issuing Google OpenID Connect Tokens to use in external systems, you must also grant
the IAM Role `roles/iam.serviceAccountOpenIdTokenCreator` on the Google Service Account to
the Google Service Account itself, i.e. the following principal:

`serviceAccount:{gcp_service_account}@{gcp_project_id}.iam.gserviceaccount.com`

This "self-impersonation" permission is necessary because the `gke-metadata-server` emulator
retrieves the Google OpenID Connect Token in a 2-step process: first it retrieves the Google
Service Account OAuth 2.0 Access Token using the Kubernetes ServiceAccount Token, and then it
retrieves the Google OpenID Connect Token using the Google Service Account OAuth 2.0 Access
Token.

### Deploy `gke-metadata-server` in your cluster

A Helm Chart is available in the following [Helm OCI Repository](https://helm.sh/docs/topics/registries/):

`ghcr.io/matheuscscp/gke-metadata-server-helm:{helm_version}` (GitHub Container Registry)

Here `{helm_version}` is a Helm Chart SemVer, i.e. the field `.version` at
[`./helm/gke-metadata-server/Chart.yaml`](./helm/gke-metadata-server/Chart.yaml). Check available releases
in the [GitHub Releases Page](https://github.com/matheuscscp/gke-metadata-server/releases).

See the Helm Values API at [`./helm/gke-metadata-server/values.yaml`](./helm/gke-metadata-server/values.yaml).
Make sure to specify at least the full name of the Workload Identity Provider.

Alternatively, you can write your own Kubernetes manifests and consume only the container image:

`ghcr.io/matheuscscp/gke-metadata-server:{container_version}` (GitHub Container Registry)

Here `{container_version}` is the app version, i.e. the field `.appVersion` at
[`./helm/gke-metadata-server/Chart.yaml`](./helm/gke-metadata-server/Chart.yaml). Check available releases
in the [GitHub Releases Page](https://github.com/matheuscscp/gke-metadata-server/releases).

### Verify the image signatures

For verifying the images above use the [`cosign`](https://github.com/sigstore/cosign) CLI tool.

For verifying the image of a given Container GitHub Release (tags `v{container_version}`), fetch the
digest file `container-digest.txt` attached to the Github Release and use it with `cosign`:

```bash
cosign verify ghcr.io/matheuscscp/gke-metadata-server@$(cat container-digest.txt) \
    --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
    --certificate-identity=https://github.com/matheuscscp/gke-metadata-server/.github/workflows/release.yml@refs/heads/main
```

For verifying the image of a given Helm Chart GitHub Release (tags `helm-v{helm_version}`), fetch the
digest file `helm-digest.txt` attached to the Github Release and use it with `cosign`:

```bash
cosign verify ghcr.io/matheuscscp/gke-metadata-server-helm@$(cat helm-digest.txt) \
    --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
    --certificate-identity=https://github.com/matheuscscp/gke-metadata-server/.github/workflows/release.yml@refs/heads/main
```

#### Automatic verification

If you are using *Kyverno* for enforcing policies you can automate the container verification using
[Keyless Verification](https://kyverno.io/docs/writing-policies/verify-images/sigstore/#keyless-signing-and-verification).

If you are using *FluxCD* for deploying Helm Charts you can automate the chart verification using
[Keyless Verification](https://fluxcd.io/flux/components/source/helmcharts/#keyless-verification).

## Security Risks and Limitations

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
field `spec.hostNetwork` set to `true`. Such Pods do not fork a new network namespace,
i.e. they share the network namespace of the Kubernetes Node where they are running on.

The emulator Pods themselves, for example, need to run on the host network in order
to listen to TCP/IP connections coming from Pods running on the same Kubernetes Node,
which are the ones this emulator Pod will serve. Just like in the GKE implementation,
this design choice makes it such that the (unencrypted) communication between the
emulator and a client Pod never leaves the Node.

Because Pods running on the host network use a shared IP address, i.e. the IP address
of the Node itself where they are running on, they cannot be uniquely identified by the
server using the client IP address reported in the HTTP request. This is a limitation
of the Kubernetes API, which does not provide a way to identify Pods running on the
host network by IP address.

To work around this limitation, Pods running on the host network are allowed to use a
***shared*** Kubernetes ServiceAccount associated with the Node where they are running
on. This ServiceAccount can be configured in the annotations or labels of the Node, and
it defaults to the ServiceAccount of the emulator (or to a ServiceAccount specified in
the emulator's CLI flags if not using the official Helm Chart distributed here, check
the chart manifest). The syntax is:

```yaml
annotations: # or labels
  gke-metadata-server.matheuscscp.io/serviceAccountName: <k8s_sa_name>
  gke-metadata-server.matheuscscp.io/serviceAccountNamespace: <k8s_namespace>
```

Prefer using annotations since they are less impactful than labels to the cluster.
Unfortunately, as of July 2024, most cloud providers support customizing only labels
in node pool templates, and some don't even support this kind of customization at all.
It's up to you how you annotate/label your Nodes.

You may also simply assign a Google Service Account to the Kubernetes ServiceAccount
of the emulator and use it for all the Pods of the cluster that are running on the
host network. This can be done through the Helm Chart value `config.googleServiceAccount`.
*But be careful and try to avoid using shared identities like this! This is obviously
dangerous!*

### The `iptables` rules

***Attention:*** The `iptables` rules installed in the network namespace of mutated
Pods will redirect outbound traffic targeting `169.254.169.254:80` to the emulator port
on the Node. If you are using similar tools or equivalent Workload Identity features
of managed Kubernetes from other clouds, *this configuration may have a direct conflict
with other such tools or features.* It's a common practice among cloud providers using
this endpoint to implement such features. Especially when mutating Pods that will run
on the host network, *the rules will be installed on the network namespace of the Node!*
Please be sure to know what you are doing when using this tool inside complex environments.

## Disclaimer

This project was not created by Google. Enterprise support from Google is
not available. **Use this tool at your own risk.**
*(But please do feel free to report bugs and CVEs, request help, new features and
[contribute](https://github.com/matheuscscp/gke-metadata-server/issues?q=is%3Aopen+is%3Aissue+label%3A%22good+first+issue%22).)*

Furthermore, this tool is *not necessary* for using GCP Workload Identity Federation inside
non-GKE Kubernetes clusters. This is just a facilitator. Kubernetes and GCP Workload Identity
Federation work together by themselves. This tool just makes your Pods need much less configuration
to use GCP Workload Identity Federation, by making the configuration as close as possible to
how Workload Identity is configured in a native GKE cluster.
