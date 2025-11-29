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

TEST_IMAGE := ghcr.io/matheuscscp/gke-metadata-server/test

.PHONY: dev
dev: tidy gen-ebpf dev-cluster build dev-test

.PHONY: clean
clean:
	rm -rf *.json *.txt *.logs *.tgz

.PHONY: tidy
tidy:
	go mod tidy
	go fmt ./...
	make gen
	terraform fmt -recursive ./
	terraform fmt -recursive ./.*/**/*.tf
	./scripts/license.sh
	git status

.PHONY: gen
gen: gen-timoni

.PHONY: gen-timoni
gen-timoni:
	cd timoni/gke-metadata-server; cue get go k8s.io/api/core/v1
	cd timoni/gke-metadata-server; cue get go k8s.io/api/apps/v1
	cd timoni/gke-metadata-server; cue get go k8s.io/api/rbac/v1

.PHONY: gen-ebpf
gen-ebpf:
	go generate ./internal/redirect

.PHONY: dev-cluster
dev-cluster:
	kind delete cluster --name gke-metadata-server || true
	make cluster TEST_ID=test PROVIDER_COMMAND=update
	kubens kube-system

.PHONY: ci-cluster
ci-cluster:
	echo "test-$$(openssl rand -hex 12)" | tee ci-test-id.txt
	make cluster TEST_ID=$$(cat ci-test-id.txt) PROVIDER_COMMAND=create

.PHONY: dev-test
dev-test:
	make test-unit
	make test TEST_ID=test

.PHONY: ci-test
ci-test:
	make test-unit
	make test TEST_ID=$$(cat ci-test-id.txt)

.PHONY: cluster
cluster:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	@if [ "${PROVIDER_COMMAND}" == "" ]; then echo "PROVIDER_COMMAND variable is required."; exit -1; fi
	kind create cluster --name gke-metadata-server --config testdata/kind.yaml
	kubectl --context kind-gke-metadata-server get nodes -o custom-columns="NODE:.metadata.name,ROUTING MODE:.metadata.labels['node\.gke-metadata-server\.matheuscscp\.io/routingMode']"
	make create-or-update-provider TEST_ID=${TEST_ID} PROVIDER_COMMAND=${PROVIDER_COMMAND}
	make install-cilium

.PHONY: create-or-update-provider
create-or-update-provider:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	@if [ "${PROVIDER_COMMAND}" == "" ]; then echo "PROVIDER_COMMAND variable is required."; exit -1; fi
	kubectl --context kind-gke-metadata-server get --raw /openid/v1/jwks > jwks.json
	gcloud iam workload-identity-pools providers ${PROVIDER_COMMAND}-oidc ${TEST_ID} \
		--project=gke-metadata-server \
		--location=global \
		--workload-identity-pool=test-kind-cluster \
		--issuer-uri=$$(kubectl --context kind-gke-metadata-server get --raw /.well-known/openid-configuration | jq -r .issuer) \
		--attribute-mapping=google.subject=assertion.sub \
		--jwk-json-path=jwks.json

.PHONY: install-cilium
install-cilium:
	sudo sysctl fs.inotify.max_user_watches=524288
	sudo sysctl fs.inotify.max_user_instances=512
	helm repo add cilium https://helm.cilium.io/
	docker pull quay.io/cilium/cilium:v1.17.1
	kind load docker-image --name gke-metadata-server quay.io/cilium/cilium:v1.17.1
	helm install cilium cilium/cilium --version 1.17.1 \
		--namespace kube-system \
		--set image.pullPolicy=IfNotPresent \
		--set ipam.mode=kubernetes

.PHONY: build-dev
build-dev:
	docker build . -t ${TEST_IMAGE}:${TAG} --push

.PHONY: build
build:
	docker build . -t ${TEST_IMAGE}:container
	docker push ${TEST_IMAGE}:container | tee docker-push.logs
	cat docker-push.logs | grep digest: | awk '{print $$3}' > container-digest.txt

	mv .dockerignore .dockerignore.bkp
	mv .dockerignore.test .dockerignore
	docker build . -t ${TEST_IMAGE}:go-test -f Dockerfile.test
	mv .dockerignore .dockerignore.test
	mv .dockerignore.bkp .dockerignore
	docker push ${TEST_IMAGE}:go-test | tee docker-push.logs
	cat docker-push.logs | grep digest: | awk '{print $$3}' > go-test-digest.txt

	sed "s|<HELM_VERSION>|$$(yq .helm versions.yaml)|g" helm/gke-metadata-server/Chart.tpl.yaml | \
		sed "s|<CONTAINER_VERSION>|$$(yq .container versions.yaml)|g" > helm/gke-metadata-server/Chart.yaml
	helm lint helm/gke-metadata-server
	helm package helm/gke-metadata-server
	helm push gke-metadata-server-helm-$$(yq .helm versions.yaml).tgz oci://${TEST_IMAGE} 2>&1 | tee helm-push.logs
	cat helm-push.logs | grep Digest: | awk '{print $$NF}' > helm-digest.txt

	sed "s|<CONTAINER_VERSION>|$$(yq .container versions.yaml)|g" \
		timoni/gke-metadata-server/templates/config.tpl.cue > timoni/gke-metadata-server/templates/config.cue
	timoni mod vet timoni/gke-metadata-server/ \
		--name gke-metadata-server \
		--namespace kube-system \
		--values timoni/gke-metadata-server/debug_values.cue
	timoni mod push timoni/gke-metadata-server/ oci://${TEST_IMAGE}/timoni \
		--version $$(yq .timoni versions.yaml) \
		--output yaml | yq .digest > timoni-digest.txt

.PHONY: test-unit
test-unit:
	go test -v ${TEST_ARGS} $$(go list ./... | grep -v github.com/matheuscscp/gke-metadata-server/internal/server | \
		grep -vE github.com/matheuscscp/gke-metadata-server$$)

.PHONY: test
test:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	TEST_ID=${TEST_ID} TEST_IMAGE=${TEST_IMAGE} HELM_VERSION=$$(yq .helm versions.yaml) go test -v ${TEST_ARGS}

.PHONY: update-branch
update-branch:
	git add .
	git stash
	git fetch --prune --all --force --tags
	git update-ref refs/heads/main origin/main
	git rebase main
	git stash pop || true

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
	cd .github/workflows/ && terraform init && terraform apply && terraform plan -detailed-exitcode

.PHONY: plan
plan:
	cd terraform/ && terraform init && terraform plan
