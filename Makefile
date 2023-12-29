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

.PHONY: all
all: dev-push helm-upgrade

.PHONY: tidy
tidy:
	go mod tidy
	go fmt ./...
	terraform fmt -recursive ./
	terraform fmt -recursive ./.*/**/*.tf
	./scripts/license.sh
	git status

.PHONY: cli
cli:
	go build -o cli github.com/matheuscscp/gke-metadata-server/cmd

DEV_IMAGE=matheuscscp/gke-metadata-server:dev
CI_IMAGE=ghcr.io/matheuscscp/gke-metadata-server/container:ci
.PHONY: test
test:
	@if [ "${IMAGE}" = "" ]; then echo "IMAGE variable is required."; exit -1; fi
	kubectl --context kind-kind -n default delete po test || true
	sed 's|GKE_METADATA_SERVER_IMAGE|${IMAGE}|g' k8s/test.yaml | kubectl --context kind-kind apply -f -
	sleep 6
	kubectl --context kind-kind -n default logs test -c test-gcloud -f
	kubectl --context kind-kind -n default logs test -c test-go -f

.PHONY: dev-test
dev-test:
	make test IMAGE=${DEV_IMAGE}

.PHONY: ci-test
ci-test:
	make test IMAGE=${CI_IMAGE}

.PHONY: push
push:
	@if [ "${IMAGE}" = "" ]; then echo "IMAGE variable is required."; exit -1; fi
	docker build . -t ${IMAGE}
	docker push ${IMAGE}

.PHONY: dev-push
dev-push:
	make push IMAGE=${DEV_IMAGE}

.PHONY: ci-push
ci-push:
	docker pull ${CI_IMAGE} || true
	make push IMAGE=${CI_IMAGE}

.PHONY: cluster
cluster:
	@if [ "${CLUSTER_ENV}" = "" ]; then echo "CLUSTER_ENV variable is required."; exit -1; fi
	kind create cluster --config k8s/${CLUSTER_ENV}-kind-config.yaml
	helm -n kube-system install --wait gke-metadata-server helm/gke-metadata-server/ -f k8s/${CLUSTER_ENV}-helm-values.yaml
	go run ./cmd publish --bucket gke-metadata-server-issuer-test --key-prefix ${CLUSTER_ENV}

.PHONY: dev-cluster
dev-cluster:
	kind delete cluster
	make cluster CLUSTER_ENV=dev
	kubens kube-system
	helm repo add prometheus-community https://prometheus-community.github.io/helm-charts
	helm repo update
	helm -n prometheus install --wait prometheus prometheus-community/prometheus --create-namespace
	@echo "\nDevelopment cluster created successfully."

.PHONY: ci-cluster
ci-cluster:
	make cluster CLUSTER_ENV=ci

.PHONY: helm-upgrade
helm-upgrade:
	helm -n kube-system upgrade --wait gke-metadata-server helm/gke-metadata-server/ -f k8s/dev-helm-values.yaml

.PHONY: helm-diff
helm-diff:
	helm -n kube-system diff upgrade gke-metadata-server helm/gke-metadata-server/ -f k8s/dev-helm-values.yaml

.PHONY: bootstrap
bootstrap:
	cd .github/workflows/ && terraform init && terraform apply
