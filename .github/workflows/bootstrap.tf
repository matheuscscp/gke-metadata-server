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

locals {
  pool_name        = google_iam_workload_identity_pool.github_actions.name
  wi_member_prefix = "principal://iam.googleapis.com/${local.pool_name}/subject/repo:matheuscscp/gke-metadata-server:environment:terraform-"
}

data "google_project" "matheuspimenta_com" {
  project_id = "matheuspimenta-com"
}

resource "google_project" "gke_metadata_server" {
  name            = "gke-metadata-server"
  project_id      = "gke-metadata-server"
  billing_account = data.google_project.matheuspimenta_com.billing_account
}

resource "google_service_account" "plan" {
  project    = google_project.gke_metadata_server.name
  account_id = "tf-plan"
}

resource "google_project_iam_member" "plan_project_editor" {
  project = google_project.gke_metadata_server.name
  role    = "roles/viewer"
  member  = google_service_account.plan.member
}

resource "google_service_account_iam_member" "plan_workload_identity_user" {
  service_account_id = google_service_account.plan.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "${local.wi_member_prefix}plan"
}

resource "google_service_account" "tf_apply" {
  project    = google_project.gke_metadata_server.name
  account_id = "tf-apply"
}

resource "google_project_iam_member" "tf_apply_project_owner" {
  project = google_project.gke_metadata_server.name
  role    = "roles/owner"
  member  = google_service_account.tf_apply.member
}

resource "google_service_account_iam_member" "tf_apply_workload_identity_user" {
  service_account_id = google_service_account.tf_apply.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "${local.wi_member_prefix}apply"
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

resource "google_storage_bucket_iam_member" "tf_apply_state_manager" {
  bucket = google_storage_bucket.terraform_state.name
  role   = "roles/storage.objectAdmin"
  member = google_service_account.tf_apply.member
}

resource "google_iam_workload_identity_pool" "github_actions" {
  project                   = google_project.gke_metadata_server.name
  workload_identity_pool_id = "github-actions"
}

resource "google_iam_workload_identity_pool_provider" "github_actions" {
  depends_on                         = [google_iam_workload_identity_pool.github_actions]
  project                            = google_project.gke_metadata_server.name
  workload_identity_pool_id          = "github-actions"
  workload_identity_pool_provider_id = "github-actions"
  oidc {
    issuer_uri = "https://token.actions.githubusercontent.com"
  }
  attribute_mapping = {
    "google.subject" = "assertion.sub" # repo:{repo_org}/{repo_name}:environment:{env_name}
  }
}
