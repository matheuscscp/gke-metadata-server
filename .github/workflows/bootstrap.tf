# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

terraform {
  backend "gcs" {
    bucket = "gke-metadata-server-bootstrap-tf-state"
  }
}

locals {
  project             = "gke-metadata-server"
  wi_user_role        = "roles/iam.workloadIdentityUser"
  gh_wi_pool_name     = google_iam_workload_identity_pool.github_actions.name
  gh_wi_sub_prefix    = "repo:matheuscscp/gke-metadata-server:environment"
  gh_wi_member_prefix = "principal://iam.googleapis.com/${local.gh_wi_pool_name}/subject/${local.gh_wi_sub_prefix}"
}

data "google_project" "matheuspimenta_com" {
  project_id = "matheuspimenta-com"
}

resource "google_project" "gke_metadata_server" {
  name            = local.project
  project_id      = local.project
  billing_account = data.google_project.matheuspimenta_com.billing_account
}

resource "google_project_service" "iam" {
  project = google_project.gke_metadata_server.name
  service = "iam.googleapis.com"
}

resource "google_project_service" "cloud_resource_manager" {
  project = google_project.gke_metadata_server.name
  service = "cloudresourcemanager.googleapis.com"
}

resource "google_service_account" "plan" {
  project    = google_project.gke_metadata_server.name
  account_id = "tf-plan"
}

resource "google_service_account_iam_member" "plan_workload_identity_user" {
  service_account_id = google_service_account.plan.name
  role               = local.wi_user_role
  member             = "${local.gh_wi_member_prefix}:terraform-plan"
}

resource "google_project_iam_member" "plan_project_viewer" {
  project = google_project.gke_metadata_server.name
  role    = "roles/viewer"
  member  = google_service_account.plan.member
}

resource "google_project_iam_member" "plan_project_security_reviewer" {
  project = google_project.gke_metadata_server.name
  role    = "roles/iam.securityReviewer"
  member  = google_service_account.plan.member
}

resource "google_service_account" "apply" {
  project    = google_project.gke_metadata_server.name
  account_id = "tf-apply"
}

resource "google_service_account_iam_member" "apply_workload_identity_user" {
  service_account_id = google_service_account.apply.name
  role               = local.wi_user_role
  member             = "${local.gh_wi_member_prefix}:terraform-apply"
}

resource "google_project_iam_member" "apply_project_owner" {
  project = google_project.gke_metadata_server.name
  role    = "roles/owner"
  member  = google_service_account.apply.member
}

resource "google_storage_bucket" "terraform_state" {
  project                  = google_project.gke_metadata_server.name
  name                     = "gke-metadata-server-tf-state"
  location                 = "us"
  public_access_prevention = "enforced"

  versioning {
    enabled = true
  }

  lifecycle_rule {
    action {
      type          = "SetStorageClass"
      storage_class = "ARCHIVE"
    }
    condition {
      num_newer_versions = 1
    }
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      num_newer_versions = 100
    }
  }
}

resource "google_storage_bucket_iam_binding" "tf_state_manager" {
  bucket = google_storage_bucket.terraform_state.name
  role   = "roles/storage.objectUser"
  members = [
    google_service_account.plan.member,
    google_service_account.apply.member,
  ]
}

resource "google_iam_workload_identity_pool" "github_actions" {
  project                   = google_project.gke_metadata_server.name
  workload_identity_pool_id = "github-actions"
}

resource "google_iam_workload_identity_pool_provider" "github_actions" {
  depends_on                         = [google_iam_workload_identity_pool.github_actions]
  project                            = google_project.gke_metadata_server.name
  workload_identity_pool_id          = google_iam_workload_identity_pool.github_actions.workload_identity_pool_id
  workload_identity_pool_provider_id = "github-actions"
  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
  attribute_mapping = {
    "google.subject" = "assertion.sub" # repo:{repo_org}/{repo_name}:environment:{env_name}
  }
}
