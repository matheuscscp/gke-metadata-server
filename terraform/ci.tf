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

resource "google_service_account" "release" {
  account_id = "release"
}

resource "google_service_account_iam_member" "release_workload_identity_user" {
  service_account_id = google_service_account.release.name
  role               = local.wi_user_role
  member             = "${local.gh_wi_member_prefix}:release"
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
    google_service_account.release.member,
  ]
}

resource "google_project_iam_custom_role" "continuous_integration" {
  title       = "Continuous Integration"
  role_id     = "continuousIntegration"
  permissions = ["iam.workloadIdentityPoolProviders.create"]
}

resource "google_storage_bucket_iam_binding" "ci_cluster_issuer_creators" {
  bucket = local.cluster_issuer_bucket
  role   = "roles/storage.objectCreator"
  members = [
    google_service_account.pull_request.member,
    google_service_account.release.member,
  ]
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
