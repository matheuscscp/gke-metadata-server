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
	bpftool btf dump file /sys/kernel/btf/vmlinux format c > ebpf/vmlinux.h
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
	make test TEST_ID=test TEST_CASE=k8s/test-helm-values-no-watch.yaml
	make test TEST_ID=test TEST_CASE=k8s/test-helm-values-watch.yaml
	make test TEST_ID=test TEST_CASE=k8s/test-timoni-values-no-watch.cue
	make test TEST_ID=test TEST_CASE=k8s/test-timoni-values-watch.cue

.PHONY: ci-test
ci-test:
	make test TEST_ID=$$(cat ci-test-id.txt) TEST_CASE=k8s/test-helm-values-no-watch.yaml
	make test TEST_ID=$$(cat ci-test-id.txt) TEST_CASE=k8s/test-helm-values-watch.yaml
	make test TEST_ID=$$(cat ci-test-id.txt) TEST_CASE=k8s/test-timoni-values-no-watch.cue
	make test TEST_ID=$$(cat ci-test-id.txt) TEST_CASE=k8s/test-timoni-values-watch.cue

.PHONY: cluster
cluster:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	@if [ "${PROVIDER_COMMAND}" == "" ]; then echo "PROVIDER_COMMAND variable is required."; exit -1; fi
	kind create cluster -n gke-metadata-server --config k8s/test-kind-config.yaml
	make create-or-update-provider TEST_ID=${TEST_ID} PROVIDER_COMMAND=${PROVIDER_COMMAND}
	kubectl --context kind-gke-metadata-server apply -f k8s/test-anon-oidc-rbac.yaml

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
	@if [ "${TEST_CASE}" == "" ]; then echo "TEST_CASE variable is required."; exit -1; fi
	helm --kube-context kind-gke-metadata-server -n kube-system uninstall gke-metadata-server --wait || true
	timoni --kube-context kind-gke-metadata-server -n kube-system delete gke-metadata-server --wait || true
	if [[ "${TEST_CASE}" == *"helm"* ]]; then \
		sed "s|<TEST_ID>|${TEST_ID}|g" ${TEST_CASE} | \
			sed "s|<CONTAINER_DIGEST>|$$(cat container-digest.txt)|g" | \
			tee >(helm --kube-context kind-gke-metadata-server -n kube-system upgrade --install gke-metadata-server $$(cat helm-package.txt) --wait -f -); \
	else \
		sed "s|<TEST_ID>|${TEST_ID}|g" ${TEST_CASE} | \
			sed "s|<CONTAINER_DIGEST>|$$(cat container-digest.txt)|g" | \
			tee >(timoni --kube-context kind-gke-metadata-server -n kube-system apply gke-metadata-server oci://${TEST_IMAGE}/timoni --digest $$(cat timoni-digest.txt) --wait -f -); \
	fi
	while : ; do \
		sleep_secs=10; \
		echo "Sleeping for $$sleep_secs secs and checking DaemonSet status..."; \
		sleep $$sleep_secs; \
		if [ $$(kubectl --context kind-gke-metadata-server -n kube-system get ds gke-metadata-server | grep gke | awk '{print $$4}') -eq 1 ]; then \
			echo "DaemonSet is ready"; \
			break; \
		fi; \
	done
	make start-test-pod POD_NAME=test-direct-access HOST_NETWORK=false SERVICE_ACCOUNT=test
	make start-test-pod POD_NAME=test-impersonation HOST_NETWORK=false SERVICE_ACCOUNT=test-impersonated
	make start-test-pod POD_NAME=test-host-network  HOST_NETWORK=true  SERVICE_ACCOUNT=test-impersonated
	while : ; do \
		sleep_secs=10; \
		echo "Sleeping for $$sleep_secs secs and checking test Pod status..."; \
		sleep $$sleep_secs; \
		EXIT_CODE_1=$$(kubectl --context kind-gke-metadata-server -n default get po test-direct-access -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}'); \
		EXIT_CODE_2=$$(kubectl --context kind-gke-metadata-server -n default get po test-impersonation -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}'); \
		EXIT_CODE_3=$$(kubectl --context kind-gke-metadata-server -n default get po test-host-network  -o jsonpath='{.status.containerStatuses[0].state.terminated.exitCode}'); \
		if [ -n "$$EXIT_CODE_1" ] && [ -n "$$EXIT_CODE_2" ] && [ -n "$$EXIT_CODE_3" ]; then \
			echo "All containers exited"; \
			break; \
		fi; \
	done; \
	kubectl --context kind-gke-metadata-server -n kube-system describe $$(kubectl --context kind-gke-metadata-server -n kube-system get po -o name | grep gke); \
	kubectl --context kind-gke-metadata-server -n kube-system logs ds/gke-metadata-server; \
	kubectl --context kind-gke-metadata-server -n default describe po test-direct-access; \
	kubectl --context kind-gke-metadata-server -n default logs test-direct-access -c init-gke-metadata-server; \
	kubectl --context kind-gke-metadata-server -n default logs test-direct-access -c test -f; \
	kubectl --context kind-gke-metadata-server -n default describe po test-impersonation; \
	kubectl --context kind-gke-metadata-server -n default logs test-impersonation -c init-gke-metadata-server; \
	kubectl --context kind-gke-metadata-server -n default logs test-impersonation -c test -f; \
	kubectl --context kind-gke-metadata-server -n default describe po test-host-network; \
	kubectl --context kind-gke-metadata-server -n default logs test-host-network -c init-gke-metadata-server; \
	kubectl --context kind-gke-metadata-server -n default logs test-host-network -c test -f; \
	echo "Pod 'test-direct-access' exit code: $$EXIT_CODE_1"; \
	echo "Pod 'test-impersonation' exit code: $$EXIT_CODE_2"; \
	echo "Pod 'test-host-network'  exit code: $$EXIT_CODE_3"; \
	if [ "$$EXIT_CODE_1" != "0" ] || [ "$$EXIT_CODE_2" != "0" ] || [ "$$EXIT_CODE_3" != "0" ]; then \
		echo "Failure."; \
		exit 1; \
	fi; \
	echo "Success."

.PHONY: start-test-pod
start-test-pod:
	@if [ "${POD_NAME}" == "" ]; then echo "POD_NAME variable is required."; exit -1; fi
	@if [ "${HOST_NETWORK}" == "" ]; then echo "HOST_NETWORK variable is required."; exit -1; fi
	@if [ "${SERVICE_ACCOUNT}" == "" ]; then echo "SERVICE_ACCOUNT variable is required."; exit -1; fi
	kubectl --context kind-gke-metadata-server -n default delete po ${POD_NAME} || true
	sed "s|<POD_NAME>|${POD_NAME}|g" k8s/test-pod.yaml | \
		sed "s|<HOST_NETWORK>|${HOST_NETWORK}|g" | \
		sed "s|<SERVICE_ACCOUNT>|${SERVICE_ACCOUNT}|g" | \
		sed "s|<GO_TEST_DIGEST>|$$(cat go-test-digest.txt)|g" | \
		tee >(kubectl --context kind-gke-metadata-server apply -f -)

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
