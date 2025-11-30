# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

terraform {
  backend "gcs" {
    bucket = "gke-metadata-server-tf-state"
  }
}

provider "google" {
  project = local.project
}

locals {
  project      = "gke-metadata-server"
  wi_user_role = "roles/iam.workloadIdentityUser"
}

data "google_project" "gke_metadata_server" {
  project_id = "gke-metadata-server"
}
