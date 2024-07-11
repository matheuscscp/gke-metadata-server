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

BASE_IMAGE := ghcr.io/matheuscscp/gke-metadata-server/test
HELM_IMAGE := oci://${BASE_IMAGE}/gke-metadata-server-helm

.PHONY: all
all: gke-metadata-server-linux-amd64 push

.PHONY: test-id.txt
test-id.txt:
	echo "test-$$(openssl rand -hex 12)" | tee test-id.txt

.PHONY: tidy
tidy:
	go mod tidy
	go fmt ./...
	terraform fmt -recursive ./
	terraform fmt -recursive ./.*/**/*.tf
	./scripts/license.sh
	git status

.PHONY: gke-metadata-server-linux-amd64
gke-metadata-server-linux-amd64:
	GOOS=linux GOARCH=amd64 go build -o $@ github.com/matheuscscp/gke-metadata-server/cmd
	sha256sum $@ > $@.sha256sum

.PHONY: kind-cluster
kind-cluster:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	@if [ "${PROVIDER_COMMAND}" == "" ]; then echo "PROVIDER_COMMAND variable is required."; exit -1; fi
	kind create cluster --config k8s/test-kind-config.yaml
	kubectl --context kind-kind get --raw /openid/v1/jwks > jwks.json
	gcloud iam workload-identity-pools providers ${PROVIDER_COMMAND}-oidc ${TEST_ID} \
		--project=gke-metadata-server \
		--location=global \
		--workload-identity-pool=test-kind-cluster \
		--issuer-uri=$$(kubectl --context kind-kind get --raw /.well-known/openid-configuration | jq -r .issuer) \
		--attribute-mapping=google.subject=assertion.sub \
		--jwk-json-path=jwks.json

.PHONY: cluster
cluster:
	kind delete cluster || true
	make kind-cluster TEST_ID=test PROVIDER_COMMAND=update
	kubens kube-system

.PHONY: ci-cluster
ci-cluster: test-id.txt
	make kind-cluster TEST_ID=$$(cat test-id.txt) PROVIDER_COMMAND=create

.PHONY: push
push:
	docker build . -t ${BASE_IMAGE}:container
	docker push ${BASE_IMAGE}:container | tee docker-push.logs
	cat docker-push.logs | grep digest: | awk '{print $$3}' > container-digest.txt

	mv .dockerignore .dockerignore.ignore
	docker build . -t ${BASE_IMAGE}:go-test -f Dockerfile.test
	mv .dockerignore.ignore .dockerignore
	docker push ${BASE_IMAGE}:go-test | tee docker-push.logs
	cat docker-push.logs | grep digest: | awk '{print $$3}' > go-test-digest.txt

	helm lint helm/gke-metadata-server
	helm package helm/gke-metadata-server | tee helm-package.logs
	basename $$(cat helm-package.logs | grep .tgz | awk '{print $$NF}') > helm-package.txt
	helm push $$(cat helm-package.txt) oci://${BASE_IMAGE} 2>&1 | tee helm-push.logs
	cat helm-push.logs | grep Pushed: | awk '{print $$NF}' | cut -d ":" -f2 > helm-version.txt
	cat helm-push.logs | grep Digest: | awk '{print $$NF}' > helm-digest.txt

.PHONY: test
test:
	make run-test TEST_ID=test HELM_TEST_CASE=no-watch
	make run-test TEST_ID=test HELM_TEST_CASE=watch

.PHONY: ci-test
ci-test:
	make run-test TEST_ID=$$(cat test-id.txt) HELM_TEST_CASE=no-watch
	make run-test TEST_ID=$$(cat test-id.txt) HELM_TEST_CASE=watch

.PHONY: run-test
run-test:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	@if [ "${HELM_TEST_CASE}" == "" ]; then echo "HELM_TEST_CASE variable is required."; exit -1; fi
	helm pull ${HELM_IMAGE} --version $$(cat helm-version.txt) 2>&1 | tee helm-pull.logs
	pull_digest=$$(cat helm-pull.logs | grep Digest: | awk '{print $$NF}'); \
	if [ "$$(cat helm-digest.txt)" != "$$pull_digest" ]; then \
		echo "Error: Helm OCI artifact digests are different. Digest on push: $$(cat helm-digest.txt), Digest on pull: $$pull_digest"; \
		exit 1; \
	fi
	sed "s|<TEST_ID>|${TEST_ID}|g" k8s/test-helm-values-${HELM_TEST_CASE}.yaml | \
		sed "s|<CONTAINER_DIGEST>|$$(cat container-digest.txt)|g" | \
		tee >(helm --kube-context kind-kind -n kube-system upgrade --install --wait gke-metadata-server ${HELM_IMAGE} --version $$(cat helm-version.txt) -f -)
	kubectl --context kind-kind -n default delete po test || true
	sed "s|<TEST_ID>|${TEST_ID}|g" k8s/test-pod.yaml | \
		sed "s|<CONTAINER_DIGEST>|$$(cat container-digest.txt)|g" | \
		sed "s|<GO_TEST_DIGEST>|$$(cat go-test-digest.txt)|g" | \
		tee >(kubectl --context kind-kind apply -f -)
	while : ; do \
		sleep_secs=10; \
		echo "Sleeping for $$sleep_secs secs and checking test Pod status..."; \
		sleep $$sleep_secs; \
		EXIT_CODE_1=$$(kubectl --context kind-kind -n default get po test -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}'); \
		EXIT_CODE_2=$$(kubectl --context kind-kind -n default get po test -o jsonpath='{.status.containerStatuses[1].state.terminated.exitCode}'); \
		if [ -n "$$EXIT_CODE_1" ] && [ -n "$$EXIT_CODE_2" ]; then \
			echo "Both containers exited"; \
			break; \
		fi; \
	done; \
	kubectl --context kind-kind -n kube-system describe $$(kubectl --context kind-kind -n kube-system get po -o name | grep gke); \
	kubectl --context kind-kind -n default describe po test; \
	kubectl --context kind-kind -n kube-system logs ds/gke-metadata-server; \
	kubectl --context kind-kind -n default logs test -c init-gke-metadata-server; \
	kubectl --context kind-kind -n default logs test -c test -f; \
	kubectl --context kind-kind -n default logs test -c test-gcloud -f; \
	echo "Container 'test'        exit code: $$EXIT_CODE_1"; \
	echo "Container 'test-gcloud' exit code: $$EXIT_CODE_2"; \
	if [ "$$EXIT_CODE_1" != "0" ] || [ "$$EXIT_CODE_2" != "0" ]; then \
		echo "Failure."; \
		exit 1; \
	fi; \
	echo "Success."

.PHONY: helm-diff
helm-diff:
	sed "s|<TEST_ID>|test|g" k8s/test-helm-values-watch.yaml | \
		sed "s|<CONTAINER_DIGEST>|$$(cat container-digest.txt)|g" | \
		helm --kube-context kind-kind -n kube-system diff upgrade gke-metadata-server helm/gke-metadata-server -f -

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
