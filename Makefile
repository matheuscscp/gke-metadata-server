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
	kind delete cluster -n gke-metadata-server || true
	make cluster TEST_ID=test PROVIDER_COMMAND=update
	kubens kube-system

.PHONY: ci-cluster
ci-cluster:
	echo "test-$$(openssl rand -hex 12)" | tee ci-test-id.txt
	make cluster TEST_ID=$$(cat ci-test-id.txt) PROVIDER_COMMAND=create

.PHONY: dev-test
dev-test:
	make test TEST_ID=test

.PHONY: ci-test
ci-test:
	make test TEST_ID=$$(cat ci-test-id.txt)

.PHONY: cluster
cluster:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	@if [ "${PROVIDER_COMMAND}" == "" ]; then echo "PROVIDER_COMMAND variable is required."; exit -1; fi
	kind create cluster -n gke-metadata-server --config testdata/kind-config.yaml
	make create-or-update-provider TEST_ID=${TEST_ID} PROVIDER_COMMAND=${PROVIDER_COMMAND}
	kubectl --context kind-gke-metadata-server apply -f testdata/anon-oidc-rbac.yaml

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

.PHONY: build
build:
	docker build . -t ${TEST_IMAGE}:container
	docker push ${TEST_IMAGE}:container | tee docker-push.logs
	cat docker-push.logs | grep digest: | awk '{print $$3}' > container-digest.txt

	mv .dockerignore .dockerignore.ignore
	docker build . -t ${TEST_IMAGE}:go-test -f Dockerfile.test
	mv .dockerignore.ignore .dockerignore
	docker push ${TEST_IMAGE}:go-test | tee docker-push.logs
	cat docker-push.logs | grep digest: | awk '{print $$3}' > go-test-digest.txt

	sed "s|<HELM_VERSION>|$$(yq .helm versions.yaml)|g" helm/gke-metadata-server/Chart.tpl.yaml | \
		sed "s|<CONTAINER_VERSION>|$$(yq .container versions.yaml)|g" > helm/gke-metadata-server/Chart.yaml
	helm lint helm/gke-metadata-server
	helm package helm/gke-metadata-server | tee helm-package.logs
	basename $$(cat helm-package.logs | grep .tgz | awk '{print $$NF}') > helm-package.txt

	sed "s|<CONTAINER_VERSION>|$$(yq .container versions.yaml)|g" \
		timoni/gke-metadata-server/templates/config.tpl.cue > timoni/gke-metadata-server/templates/config.cue
	timoni mod vet timoni/gke-metadata-server/ \
		--name gke-metadata-server \
		--namespace kube-system \
		--values timoni/gke-metadata-server/debug_values.cue
	timoni mod push timoni/gke-metadata-server/ oci://${TEST_IMAGE}/timoni \
		--version $$(yq .timoni versions.yaml) \
		--output yaml | yq .digest > timoni-digest.txt

.PHONY: test
test:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	TEST_ID=${TEST_ID} \
		TEST_IMAGE=${TEST_IMAGE} \
		CONTAINER_DIGEST=$$(cat container-digest.txt) \
		TIMONI_DIGEST=$$(cat timoni-digest.txt) \
		HELM_PACKAGE=$$(cat helm-package.txt) \
		GO_TEST_DIGEST=$$(cat go-test-digest.txt) \
		go test -v

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
