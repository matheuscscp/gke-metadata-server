# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

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

resource "google_service_account_iam_member" "test_workload_identity_users" {
  service_account_id = google_service_account.test.name
  role               = local.wi_user_role
  member             = "${local.k8s_principal_prefix}:default:test-impersonated"
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
