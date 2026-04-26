# Copyright 2025 Matheus Pimenta.
# SPDX-License-Identifier: AGPL-3.0

SHELL := /bin/bash

# Exported so recursive sub-make recipes see CNI in their shell environment
# (Make doesn't auto-export command-line variables otherwise).
export CNI

# Local-only image refs used by CI and dev builds. Pods reference these by
# tag with imagePullPolicy: Never; images are loaded into the kind cluster
# via `kind load docker-image`. No registry push happens for PR validation
# — release.yaml has its own inline OCI push to the production registry.
LOCAL_IMAGE := gke-metadata-server-test
LOCAL_TAG_DAEMON := container
LOCAL_TAG_GOTEST := go-test
# Helm/Timoni need a valid SemVer for chart/module metadata; the value is
# only used as a string label in PR-mode applies (filesystem-based, not OCI).
LOCAL_VERSION := 0.0.0-dev
PLATFORMS ?= linux/amd64
CILIUM_VERSION ?= 1.19.3

.PHONY: dev
dev: tidy gen-ebpf dev-cluster build build-go-test kind-load-images dev-test

.PHONY: clean
clean:
	rm -rf *.json *.logs *.tgz ci-test-id.txt jwks.json
	rm -f helm/gke-metadata-server/Chart.yaml timoni/gke-metadata-server/templates/config.cue

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
	go generate ./internal/attestation/bpf

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
	# CNI selects the kind config and whether to install Cilium:
	#   cilium-default (default): testdata/kind.yaml + Cilium 1.19.x default settings.
	#   cilium-kpr:               testdata/kind-cilium-kpr.yaml + Cilium with
	#                             kubeProxyReplacement=true and bpf.masquerade=true.
	#   kindnet:                  testdata/kind-kindnet.yaml (default kind CNI), no Cilium.
	CNI=$${CNI:-cilium-default}; \
	case "$$CNI" in \
		cilium-default) KIND_CONFIG=testdata/kind.yaml ;; \
		cilium-kpr)     KIND_CONFIG=testdata/kind-cilium-kpr.yaml ;; \
		kindnet)        KIND_CONFIG=testdata/kind-kindnet.yaml ;; \
		*) echo "unknown CNI=$$CNI"; exit 1 ;; \
	esac; \
	kind create cluster --name gke-metadata-server --config $$KIND_CONFIG
	kubectl --context kind-gke-metadata-server get nodes -o custom-columns="NODE:.metadata.name,ROUTING MODE:.metadata.labels['node\.gke-metadata-server\.matheuscscp\.io/routingMode']"
	make create-or-update-provider TEST_ID=${TEST_ID} PROVIDER_COMMAND=${PROVIDER_COMMAND}
	@CNI=$${CNI:-cilium-default}; \
	if [ "$$CNI" != "kindnet" ]; then \
		make install-cilium CNI=$$CNI; \
	fi

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
	docker pull quay.io/cilium/cilium:v$(CILIUM_VERSION)
	kind load docker-image --name gke-metadata-server quay.io/cilium/cilium:v$(CILIUM_VERSION)
	# CNI=cilium-kpr enables Cilium's kubeProxyReplacement (kube-proxy is
	# disabled in the kind config via kubeProxyMode: "none"). This validates
	# coexistence with Cilium's own cgroup/connect4 program — Cilium attaches
	# one for service-VIP redirection, ours attaches one for the metadata-IP
	# rewrite, and the two must run together via cilium/ebpf's BPF_LINK_CREATE
	# multi-attach. bpf.masquerade is intentionally NOT enabled: it requires
	# additional Cilium config (ipv4NativeRoutingCIDR etc.) to keep egress
	# DNS working in kind, and it's orthogonal to what this matrix entry is
	# validating.
	@CNI=$${CNI:-cilium-default}; \
	if [ "$$CNI" = "cilium-kpr" ]; then \
		KPR_FLAGS="--set kubeProxyReplacement=true \
			--set k8sServiceHost=gke-metadata-server-control-plane \
			--set k8sServicePort=6443"; \
	else \
		KPR_FLAGS=""; \
	fi; \
	helm install cilium cilium/cilium --version $(CILIUM_VERSION) \
		--namespace kube-system \
		--set image.pullPolicy=IfNotPresent \
		--set ipam.mode=kubernetes \
		$$KPR_FLAGS

.PHONY: build
# Builds the daemon container image into the local Docker daemon and renders
# the Helm chart / Timoni module metadata so they are valid for filesystem
# applies. Nothing is pushed to a registry; CI loads the local image into
# the kind cluster via `make kind-load-images` and applies helm/timoni from
# their on-disk paths. Release-mode OCI publishing is handled inline by
# .github/workflows/release.yaml.
build:
	docker buildx build . --platform ${PLATFORMS} \
		-t ${LOCAL_IMAGE}:${LOCAL_TAG_DAEMON} --load
	sed "s|<HELM_VERSION>|${LOCAL_VERSION}|g" helm/gke-metadata-server/Chart.tpl.yaml | \
		sed "s|<CONTAINER_VERSION>|${LOCAL_VERSION}|g" > helm/gke-metadata-server/Chart.yaml
	helm lint helm/gke-metadata-server
	sed "s|<CONTAINER_VERSION>|${LOCAL_VERSION}|g" \
		timoni/gke-metadata-server/templates/config.tpl.cue > timoni/gke-metadata-server/templates/config.cue
	timoni mod vet timoni/gke-metadata-server/ \
		--name gke-metadata-server \
		--namespace kube-system \
		--values timoni/gke-metadata-server/debug_values.cue

.PHONY: build-go-test
build-go-test:
	mv .dockerignore .dockerignore.bkp
	mv .dockerignore.test .dockerignore
	docker buildx build . --platform ${PLATFORMS} \
		-t ${LOCAL_IMAGE}:${LOCAL_TAG_GOTEST} -f Dockerfile.test --load
	mv .dockerignore .dockerignore.test
	mv .dockerignore.bkp .dockerignore

.PHONY: kind-load-images
# Loads the locally-built daemon and test images into the kind cluster's
# containerd. Must run after both `build` and `build-go-test` and after the
# kind cluster is up. Pods then reference these tags directly with
# imagePullPolicy: Never (see testdata/pod.yaml).
kind-load-images:
	kind load docker-image --name gke-metadata-server ${LOCAL_IMAGE}:${LOCAL_TAG_DAEMON}
	kind load docker-image --name gke-metadata-server ${LOCAL_IMAGE}:${LOCAL_TAG_GOTEST}

.PHONY: test-unit
test-unit:
	go test -v ${TEST_ARGS} $$(go list ./... | grep -v github.com/matheuscscp/gke-metadata-server/internal/server | \
		grep -vE github.com/matheuscscp/gke-metadata-server$$)

.PHONY: test
test:
	@if [ "${TEST_ID}" == "" ]; then echo "TEST_ID variable is required."; exit -1; fi
	TEST_ID=${TEST_ID} \
	LOCAL_IMAGE=${LOCAL_IMAGE} \
	LOCAL_TAG_DAEMON=${LOCAL_TAG_DAEMON} \
	LOCAL_TAG_GOTEST=${LOCAL_TAG_GOTEST} \
	go test -v ${TEST_ARGS}

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
