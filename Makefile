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

SHELL := /bin/bash

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
	@if [ "${IMAGE}" == "" ]; then echo "IMAGE variable is required."; exit -1; fi
	sed 's|<GKE_METADATA_SERVER_IMAGE>|${IMAGE}|g' k8s/test.yaml | tee >(kubectl --context kind-kind apply -f -)
	@while : ; do \
		sleep 10; \
		EXIT_CODE_1=$$(kubectl --context kind-kind -n default get po test -o jsonpath='{.status.containerStatuses[1].state.terminated.exitCode}'); \
		EXIT_CODE_2=$$(kubectl --context kind-kind -n default get po test -o jsonpath='{.status.containerStatuses[2].state.terminated.exitCode}'); \
		if [ -n "$$EXIT_CODE_1" ] && [ -n "$$EXIT_CODE_2" ]; then \
			break; \
		fi; \
	done; \
	echo "Container 'test'        exited with code $$EXIT_CODE_1"; \
	echo "Container 'test-gcloud' exited with code $$EXIT_CODE_2"; \
	kubectl --context kind-kind -n default logs test -c init-gke-metadata-proxy | jq; \
	kubectl --context kind-kind -n default logs test -c test -f; \
	kubectl --context kind-kind -n default logs test -c test-gcloud -f; \
	kubectl --context kind-kind -n kube-system describe $$(kubectl --context kind-kind -n kube-system get po -o name | grep gke); \
	kubectl --context kind-kind -n default describe po test; \
	kubectl --context kind-kind -n kube-system logs ds/gke-metadata-server | jq; \
	kubectl --context kind-kind -n default logs test -c gke-metadata-proxy | jq; \
	if [ "$$EXIT_CODE_1" != "0" ] || [ "$$EXIT_CODE_2" != "0" ]; then \
		exit 1; \
	fi

.PHONY: dev-test
dev-test:
	kubectl --context kind-kind -n default delete po test || true
	make test IMAGE=${DEV_IMAGE}

.PHONY: ci-test
ci-test:
	make test IMAGE=${CI_IMAGE}

.PHONY: push
push:
	@if [ "${IMAGE}" == "" ]; then echo "IMAGE variable is required."; exit -1; fi
	docker build . -t ${IMAGE}
	docker push ${IMAGE}
	mv .dockerignore .dockerignore.ignore
	docker build . -t ${IMAGE}-test -f Dockerfile.test
	mv .dockerignore.ignore .dockerignore
	docker push ${IMAGE}-test

.PHONY: dev-push
dev-push:
	make push IMAGE=${DEV_IMAGE}

.PHONY: ci-push
ci-push:
	docker pull ${CI_IMAGE} || true
	docker pull ${CI_IMAGE}-test || true
	make push IMAGE=${CI_IMAGE}

.PHONY: cluster
cluster:
	@if [ "${CLUSTER_ENV}" == "" ]; then echo "CLUSTER_ENV variable is required."; exit -1; fi
	kind create cluster --config k8s/${CLUSTER_ENV}-kind-config.yaml
	go run ./cmd publish --bucket gke-metadata-server-issuer-test --key-prefix ${CLUSTER_ENV}
	helm -n kube-system install --wait gke-metadata-server helm/gke-metadata-server/ -f k8s/${CLUSTER_ENV}-helm-values.yaml

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

.PHONY: update-branch
update-branch:
	git add .
	git stash
	git fetch --prune --all --force --tags
	git update-ref refs/heads/main origin/main
	git rebase main
	git stash pop

.PHONY: drop-branch
drop-branch:
	if [ $$(git status --porcelain=v1 2>/dev/null | wc -l) -ne 0 ]; then \
		git status; \
		echo ""; \
		echo "Are you sure? You have uncommitted changes, consider using scripts/update-branch.sh."; \
		exit 1; \
	fi
	git fetch --prune --all --force --tags
	git update-ref refs/heads/main origin/main
	BRANCH=$$(git branch --show-current); git checkout main; git branch -D $$BRANCH

.PHONY: bootstrap
bootstrap:
	cd .github/workflows/ && terraform init && terraform apply
