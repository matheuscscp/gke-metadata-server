# MIT License
#
# Copyright (c) 2023 Matheus Pimenta
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in all
# copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

terraform {
  backend "gcs" {
    bucket = "gke-metadata-server-bootstrap-tf-state"
  }
}

locals {
  wi_pool_name     = google_iam_workload_identity_pool.github_actions.name
  gh_sub_prefix    = "repo:matheuscscp/gke-metadata-server:environment"
  wi_member_prefix = "principal://iam.googleapis.com/${local.wi_pool_name}/subject/${local.gh_sub_prefix}"
}

data "google_project" "matheuspimenta_com" {
  project_id = "matheuspimenta-com"
}

resource "google_project" "gke_metadata_server" {
  name            = "gke-metadata-server"
  project_id      = "gke-metadata-server"
  billing_account = data.google_project.matheuspimenta_com.billing_account
}

resource "google_project_service" "iam" {
  project = google_project.gke_metadata_server.name
  service = "iam.googleapis.com"
}

resource "google_service_account" "plan" {
  project    = google_project.gke_metadata_server.name
  account_id = "tf-plan"
}

resource "google_service_account_iam_member" "plan_workload_identity_user" {
  service_account_id = google_service_account.plan.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "${local.wi_member_prefix}:terraform-plan"
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
  role               = "roles/iam.workloadIdentityUser"
  member             = "${local.wi_member_prefix}:terraform-apply"
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
  role   = "roles/storage.objectAdmin"
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

resource "google_service_account" "pull_request" {
  project    = google_project.gke_metadata_server.name
  account_id = "pull-request"
}

resource "google_service_account_iam_member" "pull_request_workload_identity_user" {
  service_account_id = google_service_account.pull_request.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "${local.wi_member_prefix}:pull-request"
}

resource "google_service_account" "release" {
  project    = google_project.gke_metadata_server.name
  account_id = "release"
}

resource "google_service_account_iam_member" "release_workload_identity_user" {
  service_account_id = google_service_account.release.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "${local.wi_member_prefix}:release"
}

resource "google_project_iam_binding" "continuous_integration" {
  project = google_project.gke_metadata_server.name
  role    = google_project_iam_custom_role.continuous_integration.name
  members = [
    google_service_account.pull_request.member,
    google_service_account.release.member,
  ]
}

resource "google_project_iam_custom_role" "continuous_integration" {
  project     = google_project.gke_metadata_server.name
  title       = "Continuous Integration"
  role_id     = "continuousIntegration"
  permissions = ["iam.workloadIdentityPoolProviders.create"]
}

resource "google_storage_bucket_iam_binding" "ci_cluster_issuer_creators" {
  bucket = "gke-metadata-server-issuer-test"
  role   = "roles/storage.objectCreator"
  members = [
    google_service_account.pull_request.member,
    google_service_account.release.member,
  ]
}
