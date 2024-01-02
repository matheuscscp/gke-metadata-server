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

# Reference:
# https://cloud.google.com/iam/docs/workload-identity-federation-with-kubernetes

terraform {
  backend "gcs" {
    bucket = "gke-metadata-server-tf-state"
  }
}

provider "google" {
  project = "gke-metadata-server"
}

locals {
  cluster_issuer_bucket = "gke-metadata-server-issuer-test"
  test_bucket           = "gke-metadata-server-test"
}

resource "google_storage_bucket" "cluster_issuer_test" {
  name     = local.cluster_issuer_bucket
  location = "us"
}

resource "google_storage_bucket_iam_member" "all_users_object_viewer" {
  bucket = google_storage_bucket.cluster_issuer_test.name
  role   = "roles/storage.objectViewer"
  member = "allUsers"
}

resource "google_iam_workload_identity_pool" "test_kind_cluster" {
  workload_identity_pool_id = "test-kind-cluster"
}

resource "google_iam_workload_identity_pool_provider" "test" {
  workload_identity_pool_id          = google_iam_workload_identity_pool.test_kind_cluster.workload_identity_pool_id
  workload_identity_pool_provider_id = "test"
  oidc {
    issuer_uri = "https://storage.googleapis.com/${local.cluster_issuer_bucket}/test"
  }
  attribute_mapping = {
    "google.subject" = "assertion.sub" # system:serviceaccount:{namespace}:{name}
  }
}

resource "google_service_account" "test" {
  account_id = "test-sa"
}

resource "google_service_account_iam_member" "workload_identity_user" {
  service_account_id = google_service_account.test.name
  role               = "roles/iam.workloadIdentityUser"
  member             = "principal://iam.googleapis.com/${google_iam_workload_identity_pool.test_kind_cluster.name}/subject/system:serviceaccount:default:test"
}

# this allows an OAuth 2.0 Access Token for the Google Service Account to be exchanged
# for an OpenID Connect ID Token for the Google Service Account. this is necessary
# for the GET /computeMetadata/v1/instance/service-accounts/*/identity API to work
resource "google_service_account_iam_member" "openid_token_creator" {
  service_account_id = google_service_account.test.name
  role               = "roles/iam.serviceAccountOpenIdTokenCreator"
  member             = google_service_account.test.member
}

resource "google_storage_bucket" "test" {
  name                     = local.test_bucket
  location                 = "us"
  public_access_prevention = "enforced"

  lifecycle_rule {
    action {
      type = "Delete"
    }
    condition {
      age = 1
    }
  }
}

resource "google_storage_bucket_iam_member" "test_service_account_bucket_object_admin" {
  bucket = google_storage_bucket.test.name
  role   = "roles/storage.objectAdmin"
  member = google_service_account.test.member
}
