# Image URL to use all building/pushing image targets
IMG ?= controller:latest

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --verify -q HEAD 2>/dev/null || echo unknown)
LDFLAGS ?= -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT)

CHART_DIR ?= charts/openbao-config-operator

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./api/...;./internal/...;./cmd/..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./api/..."

.PHONY: chart-sync
chart-sync: manifests yq ## Copy the generated CRDs and manager RBAC rules into the Helm chart and the CRD kustomizations.
	YQ="$(YQ)" hack/chart/sync-crds.sh
	YQ="$(YQ)" hack/chart/sync-rbac.sh
	hack/chart/sync-crd-kustomization.sh

.PHONY: samples
samples: ## Regenerate examples-generated/ from the CRDs in config/crd/bases.
	go run ./hack/samplegen -crds config/crd/bases -out examples-generated

.PHONY: test-samples
test-samples: setup-envtest ## Dry-run every generated sample against envtest with the CRDs installed.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test ./test/samplegen/... -count=1 -v

TEST_DOCS_FLAGS ?= -v

.PHONY: test-docs
test-docs: setup-envtest ## Dry-run every manifest in examples/, examples-generated/ and the docs pages against envtest; check snippet includes.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test -tags docs ./test/docs/... -count=1 $(TEST_DOCS_FLAGS)

.PHONY: docs-lint
docs-lint: ## Shellcheck the quickstart, tutorial and uninstall scripts that the docs include (shellcheck from PATH or SHELLCHECK).
	"$${SHELLCHECK:-shellcheck}" hack/docs/quickstart.sh hack/docs/tutorials.sh hack/docs/tutorial-*.sh hack/docs/uninstall.sh

.PHONY: api-coverage
api-coverage: ## Check api-coverage/classification.yaml against the OpenAPI snapshots and regenerate docs/api-coverage.md.
	go run ./hack/apicoverage

.PHONY: verify-generated
verify-generated: generate manifests chart-sync samples ## Fail when regenerating code, manifests, samples or the chart changes the tree.
	git diff --exit-code -- api config charts examples-generated
	@untracked="$$(git ls-files --others --exclude-standard -- api config charts examples-generated)"; \
	if [ -n "$$untracked" ]; then echo "untracked generated files:"; echo "$$untracked"; exit 1; fi

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: fmt-check
fmt-check: ## Fail when any Go file is not gofmt-formatted.
	@unformatted="$$(gofmt -l $$(find . -name '*.go' -not -path './.tmp/*' -not -path './bin/*'))"; \
	if [ -n "$$unformatted" ]; then echo "gofmt needed:"; echo "$$unformatted"; exit 1; fi

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: mod-check
mod-check: ## Fail when go.mod or go.sum is not tidy, or a downloaded module does not match go.sum.
	go mod tidy -diff
	go mod verify

.PHONY: check
check: fmt-check vet mod-check lint test test-docs verify-generated api-coverage helm-lint validate-manifests ## Run the fast subset of the PR gate (no integration, namespaces or e2e).

.PHONY: test-unit
test-unit: ## Run unit tests that need no envtest control plane.
	go test $$(go list -f '{{.ImportPath}} {{join .TestImports " "}} {{join .XTestImports " "}}' ./... | \
		awk '!/controller-runtime\/pkg\/envtest/ {print $$1}') -coverprofile cover-unit.out

.PHONY: test
test: setup-envtest ## Run unit and envtest tests; bao-backed ones use bao on PATH, BAO_BIN, or make bao.
	BAO_BIN="$${BAO_BIN:-$$(command -v bao || echo "$(BAO)")}" \
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v -e /test/e2e -e /test/integration) -race -coverprofile cover.out

# CI shards the suite with INTEGRATION_RUN / INTEGRATION_SKIP (go test -run / -skip regexes).
INTEGRATION_RUN ?= .
INTEGRATION_SKIP ?=
INTEGRATION_FLAGS ?= -timeout 25m

.PHONY: test-integration
test-integration: setup-envtest ## Run integration tests against bao server -dev (bao on PATH, BAO_BIN, or make bao) and envtest.
	BAO_BIN="$${BAO_BIN:-$$(command -v bao || echo "$(BAO)")}" \
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" \
	go test -tags integration ./test/integration/... ./internal/importer/... -count=1 $(INTEGRATION_FLAGS) \
		-run '$(INTEGRATION_RUN)' -skip '$(INTEGRATION_SKIP)'

.PHONY: test-conformance
test-conformance: setup-envtest ## Run the conformance matrix (no-write and default tests per kind) against bao server -dev and envtest.
	BAO_BIN="$${BAO_BIN:-$$(command -v bao || echo "$(BAO)")}" \
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" \
	go test -tags conformance ./test/conformance/... -count=1 -timeout 100m -v

.PHONY: test-namespaces
test-namespaces: setup-envtest ## Run the namespace matrix (every built-in kind per connection mode) against bao server -dev and envtest.
	BAO_BIN="$${BAO_BIN:-$$(command -v bao || echo "$(BAO)")}" \
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" \
	go test -tags integration ./test/nsmatrix/... -count=1 -race -timeout 40m -v

.PHONY: live-render
live-render: kustomize ## Render every live kit overlay (examples/live/overlays/<mode>/<variant>) to .tmp/live-render.
	@mkdir -p .tmp/live-render
	@for overlay in examples/live/overlays/*/*; do \
		out=".tmp/live-render/$$(echo "$${overlay#examples/live/overlays/}" | tr / -).yaml"; \
		"$(KUSTOMIZE)" build "$$overlay" > "$$out" || exit 1; \
		echo "$$out"; \
	done

.PHONY: test-livekit
test-livekit: setup-envtest kustomize ## Check the live kit: render, coverage, full fields, enum values, one-of branches, CRD validation, and root and child overlays against bao server -dev.
	BAO_BIN="$${BAO_BIN:-$$(command -v bao || echo "$(BAO)")}" \
	KUSTOMIZE="$(KUSTOMIZE)" \
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" \
	go test -tags integration ./test/livekit/... -count=1 -timeout 45m -v

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
KIND_CLUSTER ?= openbao-config-operator-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: kind ## Set up a Kind cluster for e2e tests if it does not exist
	@case "$$("$(KIND)" get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			"$(KIND)" create cluster --name $(KIND_CLUSTER) --image $(KIND_NODE_IMAGE) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e ## Run the e2e tests, then delete the Kind cluster.
	$(MAKE) test-e2e-run
	$(MAKE) cleanup-test-e2e

.PHONY: test-e2e-run
test-e2e-run: setup-test-e2e ## Run the e2e tests and keep the Kind cluster.
	KIND="$(KIND)" KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -timeout 25m -v -ginkgo.v

.PHONY: cleanup-test-e2e
cleanup-test-e2e: kind ## Tear down the Kind cluster used for e2e tests
	@"$(KIND)" delete cluster --name $(KIND_CLUSTER)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

.PHONY: govulncheck
govulncheck: govulncheck-tool ## Report known vulnerabilities reachable from both Go modules.
	"$(GOVULNCHECK)" ./...
	cd test/livekit/pluginfixture && "$(GOVULNCHECK)" ./...

.PHONY: helm-lint
helm-lint: helm ## Lint the Helm chart and render it with default values.
	"$(HELM)" lint --strict $(CHART_DIR)
	"$(HELM)" template openbao-config-operator $(CHART_DIR) --namespace openbao-config-operator > /dev/null

.PHONY: validate-manifests
validate-manifests: kustomize helm kubeconform ## Validate the kustomize and Helm output against the Kubernetes schemas.
	"$(KUSTOMIZE)" build config/default | "$(KUBECONFORM)" -strict -ignore-missing-schemas -summary
	"$(HELM)" template openbao-config-operator $(CHART_DIR) --namespace openbao-config-operator | \
		"$(KUBECONFORM)" -strict -ignore-missing-schemas -summary

# Env: IMAGE (required, repository:tag already loaded into the kind cluster), KIND_CLUSTER (default obco-helm),
# PREVIOUS_CHART, PREVIOUS_VERSION, NAMESPACE, ARTIFACTS_DIR; see hack/chart/lifecycle.sh.
.PHONY: helm-lifecycle
helm-lifecycle: helm yq ## Package, install, upgrade from the last release, roll back and uninstall the chart on a kind cluster (deletes its CRDs).
	PATH="$(LOCALBIN):$$PATH" HELM="$(HELM)" YQ="$(YQ)" KUBECTL="$(KUBECTL)" CHART_DIR="$(CHART_DIR)" hack/chart/lifecycle.sh

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -trimpath -ldflags "$(LDFLAGS)" -o bin/manager ./cmd

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd --leader-elect=false

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t ${IMG} .

# --build-context builder=DIR replaces the Dockerfile's builder stage, so the image keeps the release runtime stage.
.PHONY: docker-build-host
docker-build-host: ## Build docker image from a manager compiled on the host with its Go build cache (kind e2e).
	dir="$$(mktemp -d)"; trap 'rm -rf "$$dir"' EXIT; \
	CGO_ENABLED=0 GOOS=linux GOARCH="$$(go env GOARCH)" go build -trimpath -buildvcs=false \
		-ldflags="-s -w -buildid= -X main.version=$(VERSION) -X main.commit=$(COMMIT)" -o "$$dir/workspace/manager" ./cmd; \
	$(CONTAINER_TOOL) build --build-context builder="$$dir" --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image. The Dockerfile cross-compiles on the build
# platform, so no emulation is needed. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1)
PLATFORMS ?= linux/amd64,linux/arm64
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	$(CONTAINER_TOOL) buildx create --name openbao-config-operator-builder
	trap '$(CONTAINER_TOOL) buildx rm openbao-config-operator-builder' EXIT; \
	$(CONTAINER_TOOL) buildx build --builder openbao-config-operator-builder --push --platform=$(PLATFORMS) --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --tag ${IMG} .

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate dist/install.yaml (CRDs and deployment) and dist/crds.yaml.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml
	"$(KUSTOMIZE)" build config/crd > dist/crds.yaml

##@ Release

.PHONY: release-dry-run
release-dry-run: helm yq kustomize syft grype oras ocm crane ## Build, scan, sign and verify every release artifact into .tmp/release-dryrun; pushes nothing.
	HELM="$(HELM)" YQ="$(YQ)" KUSTOMIZE="$(KUSTOMIZE)" SYFT="$(SYFT)" GRYPE="$(GRYPE)" ORAS="$(ORAS)" \
	OCM="$(OCM)" CRANE="$(CRANE)" BUILDKIT_IMAGE="$(BUILDKIT_IMAGE)" PLATFORMS="$(PLATFORMS)" hack/release/dry-run.sh

.PHONY: ocm-dry-run
ocm-dry-run: yq oras ocm crane ## Build, sign, verify and transfer the OCM component of the last release-dry-run against local registries.
	YQ="$(YQ)" ORAS="$(ORAS)" OCM="$(OCM)" CRANE="$(CRANE)" hack/ocm/dry-run.sh

.PHONY: lint-workflows
lint-workflows: actionlint ## Lint the GitHub workflows with actionlint and zizmor (zizmor from PATH or ZIZMOR).
	"$(ACTIONLINT)"
	"$${ZIZMOR:-zizmor}" --offline .github

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

HOST_OS = $(shell go env GOHOSTOS)
HOST_ARCH = $(shell go env GOHOSTARCH)

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= $(LOCALBIN)/kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOVULNCHECK ?= $(LOCALBIN)/govulncheck
HELM ?= $(LOCALBIN)/helm
KUBECONFORM ?= $(LOCALBIN)/kubeconform
YQ ?= $(LOCALBIN)/yq
BAO ?= $(LOCALBIN)/bao
SYFT ?= $(LOCALBIN)/syft
GRYPE ?= $(LOCALBIN)/grype
ORAS ?= $(LOCALBIN)/oras
ACTIONLINT ?= $(LOCALBIN)/actionlint
OCM ?= $(LOCALBIN)/ocm
CRANE ?= $(LOCALBIN)/crane

## Tool Versions (the golangci-lint version lives in .custom-gcl.yml, which builds it)
# renovate: datasource=go depName=sigs.k8s.io/kustomize/kustomize/v5
KUSTOMIZE_VERSION ?= v5.8.1
# renovate: datasource=go depName=sigs.k8s.io/controller-tools
CONTROLLER_TOOLS_VERSION ?= v0.20.1
# renovate: datasource=go depName=sigs.k8s.io/kind
KIND_VERSION ?= v0.33.0
# renovate: datasource=go depName=golang.org/x/vuln
GOVULNCHECK_VERSION ?= v1.9.0
# renovate: datasource=github-releases depName=helm/helm
HELM_VERSION ?= v4.3.1
# helm publishes its archives on get.helm.sh; refresh these from helm-<version>-<os>-<arch>.tar.gz.sha256sum with the bump.
HELM_SHA256_linux_amd64 ?= 0403a88ae20913d11b10a2467b50087721fea7581736da6b9d3992f5357fe54b
HELM_SHA256_linux_arm64 ?= 8cf8f66ecbc252ac8f7f06cca68aba88508ddfde3a6a34feb1678ad1694b4f88
HELM_SHA256_darwin_arm64 ?= 6b4e61c8cd27d99e5a801e63ff25dd5c70b36875c2cd659d3e9ce6b76fc23198
# renovate: datasource=go depName=github.com/yannh/kubeconform
KUBECONFORM_VERSION ?= v0.8.0
# renovate: datasource=go depName=github.com/mikefarah/yq/v4
YQ_VERSION ?= v4.54.3
# renovate: datasource=github-releases depName=openbao/openbao
BAO_VERSION ?= v2.7.2
# The *_SHA256_<os>_<arch> pins come from each release's published checksums; renovate moves them with the version.
# renovate: datasource=github-release-attachments depName=openbao/openbao version=v2.7.2
BAO_SHA256_linux_amd64 ?= 582b1f3c2fdc5afd443b2d57a225ef654de75adea609fdb66731fd582a9cbc96
# renovate: datasource=github-release-attachments depName=openbao/openbao version=v2.7.2
BAO_SHA256_linux_arm64 ?= 5b97bf816fa820e5e521abec1db19c6161fcca2f666cf4a84e335089c0e83edd
# renovate: datasource=github-release-attachments depName=openbao/openbao version=v2.7.2
BAO_SHA256_darwin_arm64 ?= 2b4fc131910f4b075b2e1c9a9de67c461489491a8f03fbdc73387294d67d9547
# renovate: datasource=github-releases depName=anchore/syft
SYFT_VERSION ?= v1.57.1
# renovate: datasource=github-release-attachments depName=anchore/syft version=v1.57.1
SYFT_SHA256_linux_amd64 ?= bf52519eb57f675d47c07d43213cb6dff6449f3d6d1147ff6701ed91f44eef0c
# renovate: datasource=github-release-attachments depName=anchore/syft version=v1.57.1
SYFT_SHA256_linux_arm64 ?= aa3d3e07344f590bcce8d31043b752daa76c8a56c2aa34ceeee63c6168152582
# renovate: datasource=github-release-attachments depName=anchore/syft version=v1.57.1
SYFT_SHA256_darwin_arm64 ?= e0a74a5d2688d1254b4e17b71f068a83215e3625f846265bf6614c7ba06f3a96
# renovate: datasource=github-releases depName=anchore/grype
GRYPE_VERSION ?= v0.121.3
# renovate: datasource=github-release-attachments depName=anchore/grype version=v0.121.3
GRYPE_SHA256_linux_amd64 ?= e9da18cc1429e786722e32161288103600efffd8e483c90761f90485bd1bec85
# renovate: datasource=github-release-attachments depName=anchore/grype version=v0.121.3
GRYPE_SHA256_linux_arm64 ?= b7815598d48da78bfd16f5b98ef28e1bb09c03d34fc8c3e350f99b6ccb3c7cba
# renovate: datasource=github-release-attachments depName=anchore/grype version=v0.121.3
GRYPE_SHA256_darwin_arm64 ?= 54336f2cd8cdee8b3d8ea83462561ff7fa9293498054c7c53a193db44bfea4c8
# renovate: datasource=github-releases depName=oras-project/oras
ORAS_VERSION ?= v1.3.6
# renovate: datasource=github-release-attachments depName=oras-project/oras version=v1.3.6
ORAS_SHA256_linux_amd64 ?= 66fb0ad7206f3f6fa77ed5b552049d364ccf4b50b1684554f731f76302b52ca0
# renovate: datasource=github-release-attachments depName=oras-project/oras version=v1.3.6
ORAS_SHA256_linux_arm64 ?= b84269e78dfc2d5329a4aab373da3200de95e6c138338cf6c66c10c0a3f248c1
# renovate: datasource=github-release-attachments depName=oras-project/oras version=v1.3.6
ORAS_SHA256_darwin_arm64 ?= e2b25a53234e2471c5fc81936f957fa612065ab559c21834c02d8bfbc2f62b30
# renovate: datasource=github-releases depName=rhysd/actionlint
ACTIONLINT_VERSION ?= v1.7.12
# renovate: datasource=github-release-attachments depName=rhysd/actionlint version=v1.7.12
ACTIONLINT_SHA256_linux_amd64 ?= 8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8
# renovate: datasource=github-release-attachments depName=rhysd/actionlint version=v1.7.12
ACTIONLINT_SHA256_linux_arm64 ?= 325e971b6ba9bfa504672e29be93c24981eeb1c07576d730e9f7c8805afff0c6
# renovate: datasource=github-release-attachments depName=rhysd/actionlint version=v1.7.12
ACTIONLINT_SHA256_darwin_arm64 ?= aba9ced2dee8d27fecca3dc7feb1a7f9a52caefa1eb46f3271ea66b6e0e6953f
# renovate: datasource=github-releases depName=open-component-model/ocm
OCM_VERSION ?= v0.54.0
# renovate: datasource=github-release-attachments depName=open-component-model/ocm version=v0.54.0
OCM_SHA256_linux_amd64 ?= ec48f9fc42eb4ee6c8415435ca62bf72dcf8c790f4399ce5111f6488f42379b2
# renovate: datasource=github-release-attachments depName=open-component-model/ocm version=v0.54.0
OCM_SHA256_linux_arm64 ?= e16c66ec3fe6932ed578e898f8fbe97c95d43405878f550b30b1a78d0222b4aa
# renovate: datasource=github-release-attachments depName=open-component-model/ocm version=v0.54.0
OCM_SHA256_darwin_arm64 ?= c564302e999035ac98b54b61e9467be82c9de5e03087807cab025edb4643c35d
# renovate: datasource=go depName=github.com/google/go-containerregistry
CRANE_VERSION ?= v0.22.1
# renovate: datasource=docker depName=kindest/node
KIND_NODE_IMAGE ?= kindest/node:v1.35.8@sha256:07b2536e30b803ed61d1677a79df6115f798ce64c80f9e22f6ed45afd09323c0
# renovate: datasource=docker depName=moby/buildkit
BUILDKIT_IMAGE ?= moby/buildkit:v0.34.1@sha256:99368f3e46177c6405b9740e4461d91cee004128daeecca78d4ceac50ec1da5c
GOLANGCI_LINT_VERSION ?= $(shell awk '/^version:/ {print $$2}' .custom-gcl.yml)
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint-custom-$(GOLANGCI_LINT_VERSION)

#ENVTEST_VERSION is the version of controller-runtime release branch to fetch the envtest setup script (i.e. release-0.20)
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
		echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Build golangci-lint with the module plugins from .custom-gcl.yml if necessary.
$(GOLANGCI_LINT): .custom-gcl.yml | $(LOCALBIN)
	@set -e; tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
	echo "Building custom golangci-lint $(GOLANGCI_LINT_VERSION) with plugins..."; \
	GOBIN="$$tmp" go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	"$$tmp/golangci-lint" custom --destination "$(LOCALBIN)" --name "$(notdir $(GOLANGCI_LINT))"

.PHONY: kind
kind: $(KIND) ## Download kind locally if necessary.
$(KIND): $(LOCALBIN)
	$(call go-install-tool,$(KIND),sigs.k8s.io/kind,$(KIND_VERSION))

.PHONY: govulncheck-tool
govulncheck-tool: $(GOVULNCHECK) ## Download govulncheck locally if necessary.
$(GOVULNCHECK): $(LOCALBIN)
	$(call go-install-tool,$(GOVULNCHECK),golang.org/x/vuln/cmd/govulncheck,$(GOVULNCHECK_VERSION))

.PHONY: kubeconform
kubeconform: $(KUBECONFORM) ## Download kubeconform locally if necessary.
$(KUBECONFORM): $(LOCALBIN)
	$(call go-install-tool,$(KUBECONFORM),github.com/yannh/kubeconform/cmd/kubeconform,$(KUBECONFORM_VERSION))

.PHONY: yq
yq: $(YQ) ## Download yq locally if necessary.
$(YQ): $(LOCALBIN)
	$(call go-install-tool,$(YQ),github.com/mikefarah/yq/v4,$(YQ_VERSION))

.PHONY: helm
helm: $(HELM) ## Download helm locally if necessary.
$(HELM): $(LOCALBIN)
	$(call download-tool,$(HELM),$(HELM_VERSION),https://get.helm.sh,helm-$(HELM_VERSION)-$(HOST_OS)-$(HOST_ARCH).tar.gz,$(HELM_SHA256_$(HOST_OS)_$(HOST_ARCH)),$(HOST_OS)-$(HOST_ARCH)/helm)

.PHONY: bao
bao: $(BAO) ## Download the pinned OpenBao release binary locally if necessary.
$(BAO): $(LOCALBIN)
	$(call download-tool,$(BAO),$(BAO_VERSION),https://github.com/openbao/openbao/releases/download/$(BAO_VERSION),openbao_$(BAO_VERSION:v%=%)_$(HOST_OS)_$(HOST_ARCH).tar.gz,$(BAO_SHA256_$(HOST_OS)_$(HOST_ARCH)),bao)

.PHONY: syft
syft: $(SYFT) ## Download syft locally if necessary.
$(SYFT): $(LOCALBIN)
	$(call download-tool,$(SYFT),$(SYFT_VERSION),https://github.com/anchore/syft/releases/download/$(SYFT_VERSION),syft_$(SYFT_VERSION:v%=%)_$(HOST_OS)_$(HOST_ARCH).tar.gz,$(SYFT_SHA256_$(HOST_OS)_$(HOST_ARCH)),syft)

.PHONY: grype
grype: $(GRYPE) ## Download grype locally if necessary.
$(GRYPE): $(LOCALBIN)
	$(call download-tool,$(GRYPE),$(GRYPE_VERSION),https://github.com/anchore/grype/releases/download/$(GRYPE_VERSION),grype_$(GRYPE_VERSION:v%=%)_$(HOST_OS)_$(HOST_ARCH).tar.gz,$(GRYPE_SHA256_$(HOST_OS)_$(HOST_ARCH)),grype)

.PHONY: oras
oras: $(ORAS) ## Download oras locally if necessary.
$(ORAS): $(LOCALBIN)
	$(call download-tool,$(ORAS),$(ORAS_VERSION),https://github.com/oras-project/oras/releases/download/$(ORAS_VERSION),oras_$(ORAS_VERSION:v%=%)_$(HOST_OS)_$(HOST_ARCH).tar.gz,$(ORAS_SHA256_$(HOST_OS)_$(HOST_ARCH)),oras)

.PHONY: actionlint
actionlint: $(ACTIONLINT) ## Download actionlint locally if necessary.
$(ACTIONLINT): $(LOCALBIN)
	$(call download-tool,$(ACTIONLINT),$(ACTIONLINT_VERSION),https://github.com/rhysd/actionlint/releases/download/$(ACTIONLINT_VERSION),actionlint_$(ACTIONLINT_VERSION:v%=%)_$(HOST_OS)_$(HOST_ARCH).tar.gz,$(ACTIONLINT_SHA256_$(HOST_OS)_$(HOST_ARCH)),actionlint)

# The ocm module has replace directives, so go install cannot build it.
.PHONY: ocm
ocm: $(OCM) ## Download the pinned ocm CLI release binary locally if necessary.
$(OCM): $(LOCALBIN)
	$(call download-tool,$(OCM),$(OCM_VERSION),https://github.com/open-component-model/ocm/releases/download/$(OCM_VERSION),ocm-$(OCM_VERSION:v%=%)-$(HOST_OS)-$(HOST_ARCH).tar.gz,$(OCM_SHA256_$(HOST_OS)_$(HOST_ARCH)),ocm)

.PHONY: crane
crane: $(CRANE) ## Download crane locally if necessary.
$(CRANE): $(LOCALBIN)
	$(call go-install-tool,$(CRANE),github.com/google/go-containerregistry/cmd/crane,$(CRANE_VERSION))

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
# $4 - optional -ldflags, e.g. the version string a release build stamps in
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $(if $(4),-ldflags "$(4)") $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

# download-tool fetches a release tarball, verifies it against the sha256 pinned in this Makefile (a checksum file
# of the same release can be replaced together with the tarball) and extracts one binary
# $1 - target path with name of binary
# $2 - version
# $3 - base url of the release
# $4 - tarball name
# $5 - pinned sha256 of the tarball
# $6 - path of the binary inside the tarball
define download-tool
@[ -f "$(1)-$(2)" ] || { \
set -e; \
[ -n "$(5)" ] || { echo "no sha256 pinned in the Makefile for $(4)" >&2; exit 1; }; \
tmp="$$(mktemp -d)"; trap 'rm -rf "$$tmp"' EXIT; \
echo "Downloading $(3)/$(4)"; \
curl -fsSLo "$$tmp/$(4)" "$(3)/$(4)"; \
echo "$(5)  $$tmp/$(4)" | shasum -a 256 -c -; \
tar -xzf "$$tmp/$(4)" -C "$$tmp" "$(6)"; \
mv "$$tmp/$(6)" "$(1)-$(2)"; \
} ;\
ln -sf "$$(realpath "$(1)-$(2)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

.PHONY: lab-up lab-identity lab-eso lab-pki
lab-up: ## Create the disposable kind lab with TLS raft OpenBao and the operator.
	hack/lab/up.sh $(or $(LAB_PROFILE),raft)

lab-identity: ## Exercise Keycloak OIDC, JWT and group access in the local lab.
	npm ci --ignore-scripts --prefix hack/lab/browser
	hack/lab/identity-up.sh
	hack/lab/identity-test.sh
	hack/lab/identity-browser.sh

lab-eso: ## Exercise tenant secret delivery and rotation through ESO.
	hack/lab/eso-up.sh
	hack/lab/eso-test.sh

lab-pki: ## Issue and verify localhost certificates through cert-manager and operator PKI CRDs.
	hack/lab/pki-up.sh
	hack/lab/pki-test.sh
