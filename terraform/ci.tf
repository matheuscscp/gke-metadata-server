# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

locals {
  gh_wi_pool_name     = "projects/637293746831/locations/global/workloadIdentityPools/github-actions"
  gh_wi_sub_prefix    = "repo:matheuscscp/gke-metadata-server:environment"
  gh_wi_member_prefix = "principal://iam.googleapis.com/${local.gh_wi_pool_name}/subject/${local.gh_wi_sub_prefix}"
}

resource "google_service_account" "pull_request" {
  account_id = "pull-request"
}

resource "google_service_account_iam_member" "pull_request_workload_identity_user" {
  service_account_id = google_service_account.pull_request.name
  role               = local.wi_user_role
  member             = "${local.gh_wi_member_prefix}:pull-request"
}

resource "google_service_account" "clean_resources" {
  account_id = "clean-resources"
}

resource "google_service_account_iam_member" "clean_resources_workload_identity_user" {
  service_account_id = google_service_account.clean_resources.name
  role               = local.wi_user_role
  member             = "${local.gh_wi_member_prefix}:clean-resources"
}

resource "google_project_iam_binding" "continuous_integration" {
  project = local.project
  role    = google_project_iam_custom_role.continuous_integration.name
  members = [
    google_service_account.pull_request.member,
  ]
}

resource "google_project_iam_custom_role" "continuous_integration" {
  title       = "Continuous Integration"
  role_id     = "continuousIntegration"
  permissions = ["iam.workloadIdentityPoolProviders.create"]
}

resource "google_project_iam_member" "resource_cleaner" {
  project = local.project
  role    = google_project_iam_custom_role.resource_cleaner.name
  member  = google_service_account.clean_resources.member
}

resource "google_project_iam_custom_role" "resource_cleaner" {
  title   = "Resource Cleaner"
  role_id = "resourceCleaner"
  permissions = [
    "iam.workloadIdentityPoolProviders.list",
    "iam.workloadIdentityPoolProviders.delete",
  ]
}
