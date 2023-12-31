# SLSA Compliance Documentation

This document outlines the [Supply-chain Levels for Software Artifacts (SLSA)](https://slsa.dev/) compliance for this project.

## Overview

This project has focus on security, and as such the developers have assessed the provenance
processes as [SLSA Level 2](https://slsa.dev/spec/v1.0/levels#build-l2-hosted-build-platform).

## Source Requirements

### Version Control and Change Management

The versions of the project are the Helm Chart version and the container image version.

New versions are released via a GitOps workflow: they are retrieved from
`./helm/gke-metadata-server/Chart.yaml`, respectively `.version` and `.appVersion`.
When a Pull Request changing those versions is merged, the new artifacts are built,
tested, published, signed and verified (using `cosign`
[Keyless Signing](https://docs.sigstore.dev/signing/overview/)), and a new GitHub
Release is published (updating the latest), tagging both versions as a Git tag.

#### Immutable Reference

The `release` GitHub Workflow checks the exitence of the versions inside
`./helm/gke-metadata-server/Chart.yaml` in the GHCR registries. If they
already exist then the workflow will consider the version as successfully
released previously and does not re-release it. GHCR and Docker Hub still
do not provide Immutable Tags, e.g. like the GCP Artifact Registry.

Migrating to the GCP Artifact Registry is left as future work required for
assessing the project as SLSA Level 3.

#### Verified History

WIP...

### Source Integrity

- **Access Control**: [Detail how access to the source code repository is controlled and monitored.]
- **Code Review**: [Outline your code review policies and how they enforce integrity.]

## Build Requirements

### Build Automation

- **Automated Build**: [Describe your build automation system and how it ensures that the build process requires no human intervention.]

### Reproducible Builds

- **Build Reproducibility**: [Explain how your builds are reproducible, including shared build scripts and environment configurations.]

### Isolated Build Environment

- **Environment Isolation**: [Detail the measures taken to ensure that the build environment is ephemeral and isolated from potential interference.]

## Provenance Requirements

### Provenance Creation

- **Provenance Generation**: [Describe how provenance is automatically generated for each build, including the information it contains.]

### Provenance Integrity

- **Authenticated Provenance**: [Explain the mechanisms in place to ensure the integrity and authenticity of provenance information.]

## Common Requirements

### Security

- **Platform Security**: [Detail the security controls and practices in place for the systems and platforms involved in the development and deployment process.]
- **Operations Security**: [Describe the operational security measures, such as regular patching, security monitoring, and incident response protocols.]

### Access

- **Access Control**: [List the access control mechanisms for critical systems and infrastructure.]
- **Superuser Access**: [Explain how superuser access is limited and monitored.]

## SLSA Level 3 Specifics

[Here, detail the specific practices, tools, and configurations that ensure your project meets all the requirements for SLSA Level 3. Refer to the requirements listed in the official SLSA documentation and provide a description or evidence for each.]

### How We Meet Source Requirements

- **Immutable Reference**:
- **Verified History**:

### How We Meet Build Requirements

- **Automated Build**:
- **Reproducible Builds**:
- **Isolated Build Environment**:

### How We Meet Provenance Requirements

- **Authenticated Provenance**:

### How We Meet Common Requirements

- **Platform Security**:
- **Operations Security**:
- **Access Control**:
- **Superuser Access**:

## Conclusion

[End with a conclusion about your commitment to security, the steps taken to ensure SLSA compliance, and any future plans for maintaining or increasing the security level.]
