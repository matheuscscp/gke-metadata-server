# MIT License
#
# Copyright (c) 2024 Matheus Pimenta
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

# Reference:
# https://cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes

locals {
  test_bucket          = "gke-metadata-server-test"
  k8s_principal_prefix = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.test_kind_cluster.name}/subject/system:serviceaccount"
}

resource "google_iam_workload_identity_pool" "test_kind_cluster" {
  workload_identity_pool_id = "test-kind-cluster"
}

resource "google_service_account" "test" {
  account_id = "test-sa"
}

resource "google_service_account_iam_binding" "test_workload_identity_users" {
  service_account_id = google_service_account.test.name
  role               = local.wi_user_role
  members = [
    "${local.k8s_principal_prefix}:default:test-impersonated",
  ]
}

# this allows the emulator to issue Google Identity Tokens
resource "google_project_iam_member" "openid_token_creator" {
  project = data.google_project.gke_metadata_server.name
  role    = "roles/iam.serviceAccountOpenIdTokenCreator"
  member  = "${local.k8s_principal_prefix}:kube-system:gke-metadata-server"
}

resource "google_storage_bucket" "test" {
  name                        = local.test_bucket
  location                    = "us"
  public_access_prevention    = "enforced"
  uniform_bucket_level_access = true # required for direct resource access

  soft_delete_policy {
    retention_duration_seconds = 0
  }

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      age = 1
    }
  }
}

resource "google_storage_bucket_iam_binding" "test_bucket_object_admins" {
  bucket = google_storage_bucket.test.name
  role   = "roles/storage.objectAdmin"
  members = [
    google_service_account.test.member,
    "${local.k8s_principal_prefix}:default:test",
  ]
}
