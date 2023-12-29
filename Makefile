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
all: push-dev-image helm-upgrade

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

.PHONY: test
test:
	kubectl --context kind-kind -n default delete po test || true
	kubectl --context kind-kind apply -f k8s/test.yaml
	sleep 6
	kubectl --context kind-kind -n default logs test -c test-gcloud -f
	kubectl --context kind-kind -n default logs test -c test-go -f

.PHONY: push-dev-image
push-dev-image:
	docker build . -t matheuscscp/gke-metadata-server:dev
	docker push matheuscscp/gke-metadata-server:dev

.PHONY: ci-cluster dev-cluster
%-cluster: cli
	kind delete cluster
	kind create cluster --config k8s/$*-kind-config.yaml
	./cli publish --bucket gke-metadata-server-issuer-test --key-prefix $*
	helm -n kube-system install --wait gke-metadata-server helm/gke-metadata-server/ -f k8s/$*-helm-values.yaml

.PHONY: helm-upgrade
helm-upgrade:
	helm -n kube-system upgrade --wait gke-metadata-server helm/gke-metadata-server/ -f k8s/dev-helm-values.yaml

.PHONY: helm-diff
helm-diff:
	helm -n kube-system diff upgrade gke-metadata-server helm/gke-metadata-server/ -f k8s/dev-helm-values.yaml

.PHONY: bootstrap
bootstrap:
	cd .github/workflows/ && terraform init && terraform apply
