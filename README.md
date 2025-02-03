# gke-metadata-server

![release](https://img.shields.io/github/v/release/matheuscscp/gke-metadata-server?style=flat-square&color=blue)
[![release](https://github.com/matheuscscp/gke-metadata-server/actions/workflows/release.yml/badge.svg?branch=main)](https://github.com/matheuscscp/gke-metadata-server/actions/workflows/release.yml)

A GKE Metadata Server *emulator* for making it easier to use GCP Workload Identity Federation
inside *non-GKE* Kubernetes clusters, e.g. KinD, on-prem, managed Kubernetes from other
clouds, etc. This implementation tries to mimic the `gke-metadata-server` `DaemonSet` deployed
automatically by Google in the `kube-system` namespace of GKE clusters that have the feature
*Workload Identity Federation for GKE* enabled. See how the GKE Metadata Server
[works](https://cloud.google.com/kubernetes-engine/docs/concepts/workload-identity#metadata_server).

## Usage

Steps:
1. Configure GCP Workload Identity Federation for Kubernetes.
2. Deploy `gke-metadata-server` in the cluster using the Workload Identity Provider full name,
obtained after step 2.
3. See [`./k8s/test-pod.yaml`](./k8s/test-pod.yaml) for an example of how to configure your Pods
and their ServiceAccounts.
4. (Optional but highly recommended) Verify gke-metadata-server's artifact signatures to make sure
you are deploying authentic artifacts distributed by this project.

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
API for issuing Google OpenID Connect Identity Tokens to use in external systems, you must
also grant the IAM Role `roles/iam.serviceAccountOpenIdTokenCreator` on the Google Service
Account to the Google Service Account itself, i.e. the following principal:

`serviceAccount:{gcp_service_account}@{gcp_project_id}.iam.gserviceaccount.com`

This "self-impersonation" permission is necessary because the `gke-metadata-server` emulator
retrieves the Google OpenID Connect Identity Token in a 2-step process: first it retrieves
the Google Service Account OAuth 2.0 Access Token using the Kubernetes ServiceAccount Token,
and then it retrieves the Google OpenID Connect Token using the Google Service Account OAuth
2.0 Access Token.

#### Alternatively, grant direct resource access to the Kubernetes ServiceAccount

Workload Identity Federation for Kubernetes allows you to directly grant Kubernetes
ServiceAccounts access to Google resources, without the need to impersonate a Google
Service Account. This is done by granting the given IAM Roles directly to principals
of the form described above for impersonation. See [docs](https://cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes#use-wlif).

Some specific GCP services do not support this method. See [docs](https://cloud.google.com/iam/docs/federated-identity-supported-services#list).

The `GET /computeMetadata/v1/instance/service-accounts/default/identity` API is not
supported by this method. If you plan to use this API, you must use the impersonation
method described above.

### Deploy `gke-metadata-server` in your cluster

#### Using the Helm Chart

A Helm Chart is available in the following [Helm OCI Repository](https://helm.sh/docs/topics/registries/):

`ghcr.io/matheuscscp/gke-metadata-server-helm:{helm_version}` (GitHub Container Registry)

Here `{helm_version}` is a Helm Chart version, i.e. the field `.helm` in the file
[`./versions.yaml`](./versions.yaml). Check available releases
in the [GitHub Releases Page](https://github.com/matheuscscp/gke-metadata-server/releases).

See the Helm Values API in the file [`./helm/gke-metadata-server/values.yaml`](./helm/gke-metadata-server/values.yaml).
Make sure to specify at least the full name of the Workload Identity Provider.

#### Using the Timoni Module

If you prefer something newer and strongly typed, a Timoni Module is available in the following
[Timoni OCI Repository](https://timoni.sh/concepts/#artifact):

`ghcr.io/matheuscscp/gke-metadata-server-timoni:{timoni_version}` (GitHub Container Registry)

Here `{timoni_version}` is a Timoni Module version, i.e. the field `.timoni` in the file
[`./versions.yaml`](./versions.yaml). Check available releases
in the [GitHub Releases Page](https://github.com/matheuscscp/gke-metadata-server/releases).

See the Timoni Values API in the files [`./timoni/gke-metadata-server/templates/config.tpl.cue`](./timoni/gke-metadata-server/templates/config.tpl.cue)
and [`./timoni/gke-metadata-server/templates/settings.cue`](./timoni/gke-metadata-server/templates/settings.cue).
Make sure to specify at least the full name of the Workload Identity Provider.

#### Using only the container image (with your own Kubernetes manifests)

Alternatively, you can write your own Kubernetes manifests and consume only the container image:

`ghcr.io/matheuscscp/gke-metadata-server:{container_version}` (GitHub Container Registry)

Here `{container_version}` is an app container version, i.e. the field `.container` in the file
[`./versions.yaml`](./versions.yaml). Check available releases
in the [GitHub Releases Page](https://github.com/matheuscscp/gke-metadata-server/releases).

### Verify the image signatures

For verifying the images above use the [`cosign`](https://github.com/sigstore/cosign) CLI tool.

#### Verify the container image

For verifying the image of a given Container GitHub Release (tags `v{container_version}`), download the
digest file `container-digest.txt` attached to the Github Release and use it with `cosign`:

```bash
cosign verify ghcr.io/matheuscscp/gke-metadata-server@$(cat container-digest.txt) \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity=https://github.com/matheuscscp/gke-metadata-server/.github/workflows/release.yml@refs/heads/main
```

##### Automatic verification

If you are using Sigstore's *Policy Controller* for enforcing policies you can automate the image
verification using [Keyless Authorities](https://docs.sigstore.dev/policy-controller/overview/#configuring-keyless-authorities).

If you are using *Kyverno* for enforcing policies you can automate the image verification using
[Keyless Verification](https://kyverno.io/docs/writing-policies/verify-images/sigstore/#keyless-signing-and-verification).

#### Verify the Helm Chart image

For verifying the image of a given Helm Chart GitHub Release (tags `helm-v{helm_version}`), download the
digest file `helm-digest.txt` attached to the Github Release and use it with `cosign`:

```bash
cosign verify ghcr.io/matheuscscp/gke-metadata-server-helm@$(cat helm-digest.txt) \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity=https://github.com/matheuscscp/gke-metadata-server/.github/workflows/release.yml@refs/heads/main
```

##### Automatic verification

If you are using *Flux* for deploying Helm Charts you can automate the image verification using
[Keyless Verification](https://fluxcd.io/flux/components/source/helmcharts/#keyless-verification).

#### Verify the Timoni Module image

For verifying the image of a given Timoni Module GitHub Release (tags `timoni-v{timoni_version}`), download the
digest file `timoni-digest.txt` attached to the Github Release and use it with `cosign`:

```bash
cosign verify ghcr.io/matheuscscp/gke-metadata-server-timoni@$(cat timoni-digest.txt) \
  --certificate-oidc-issuer=https://token.actions.githubusercontent.com \
  --certificate-identity=https://github.com/matheuscscp/gke-metadata-server/.github/workflows/release.yml@refs/heads/main
```

##### Automatic verification

If you are using the Timoni CLI to deploy Modules/Bundles you can automate the image verification
using [Keyless Verification](https://timoni.sh/cue/module/signing/#sign-with-cosign-keyless).
*(Timoni will have a Flux controller in the future.)*

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
the emulator CLI flags if not using the official Helm Chart or Timoni Module
distributed here). The syntax is:

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
host network. This can be done through the Helm Chart value `config.defaultNodeServiceAccount`,
or the Timoni Module value `values.settings.defaultNodeServiceAccount`.

*Be careful and try to avoid using shared identities! This is obviously dangerous!*

### eBPF magic 🐝

***Attention:*** The emulator installs eBPF magic 🐝 in the Node kernel. The `connect()`
syscalls are modified to target the emulator address when the original destination is
`169.254.169.254:80`. If you are using similar tools or equivalent workload identity
features of managed services from other clouds, *this configuration may have a direct
conflict with other such tools or features.* It's a common practice among cloud providers
using this endpoint to implement workload identity. If your use case requires, the Helm
Chart and Timoni Module distributed here allow customizing the Node scheduling rules
for the DaemonSet Pods, i.e. `nodeSelector`, `tolerations`, `affinity`, etc. These
allow you to configure a specific Node pool for Pods where you want to use workload
identity for Google, while other Nodes can be used for other cloud providers or tools.

### Token Cache

When the emulator is configured to cache tokens, the issued Google OAuth 2.0 Access and
OpenID Connect Identity Tokens are cached and returned to client Pods on every request
until their expiration.

This means that even if you revoke the required permissions for the Kubernetes ServiceAccount
to issue those tokens, the client Pods will still get those tokens from the emulator until
they expire. This is a limitation of how the permissions are evaluated: they are evaluated
only when the tokens are issued, which is what caching tries to avoid. If your use case
requires immediate revocation of permissions, then you should not use token caching.

Tokens usually expire in 1 hour.

## Disclaimer

This project was not created by Google. Enterprise support from Google is
not available. **Use this tool at your own risk.**
*(But please do feel free to report bugs and CVEs, request help, new features and
[contribute](https://github.com/matheuscscp/gke-metadata-server/issues?q=is%3Aopen+is%3Aissue+label%3A%22good+first+issue%22).)*

Furthermore, this tool is *not necessary* for using GCP Workload Identity Federation
inside non-GKE Kubernetes clusters. This is just a facilitator. Kubernetes and GCP
Workload Identity Federation work together by themselves. This tool just makes your
Pods need much less configuration to use GCP Workload Identity Federation for Kubernetes,
by making the configuration as close as possible to how Workload Identity Federation
for GKE is configured.
