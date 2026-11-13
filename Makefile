IMG ?= gateway-controller:latest

# kind-up builds and loads this tag rather than IMG, so a local kind cluster
# is never accidentally running an image meant for a registry.
KIND_CLUSTER_NAME ?= gateway-controller-dev
KIND_IMG ?= gateway-controller:dev

KUBECTL ?= kubectl

CONTAINER_ENGINE ?= $(shell command -v podman 2>/dev/null || command -v docker 2>/dev/null)

.PHONY: all
all: build

.PHONY: require-container-engine
require-container-engine:
	@if [ -z "$(CONTAINER_ENGINE)" ]; then \
		echo "ERROR: No container engine found. Install podman or docker"; \
		exit 1; \
	fi

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} \
		/^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2 } \
		/^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) }' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate CRD and RBAC manifests.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate deepcopy implementations.
	$(CONTROLLER_GEN) object paths="./..."

# The verify-* targets are intended for CI against a clean checkout, and are
# run individually so a failure names the step that produced it. They check
# untracked files too: new generated output would otherwise pass unnoticed.
.PHONY: verify-manifests
verify-manifests: manifests ## Fail if generated manifests are not committed.
	$(call verify-clean,config,manifests)

.PHONY: verify-generate
verify-generate: generate ## Fail if generated deepcopy code is not committed.
	$(call verify-clean,'*zz_generated*',generate)

.PHONY: verify-tidy
verify-tidy: tidy ## Fail if go.mod or go.sum are not tidy.
	$(call verify-clean,go.mod go.sum,tidy)

.PHONY: verify
verify: verify-manifests verify-generate verify-tidy ## Run all verify-* targets.

# verify-clean fails when pathspec $1 has uncommitted or untracked changes,
# naming the make target $2 that regenerates it.
define verify-clean
@out="$$(git status --porcelain --untracked-files=all -- $(1))"; \
if [ -n "$$out" ]; then \
	echo "$(1) is out of date; run 'make $(2)' and commit the result:"; \
	echo "$$out"; \
	exit 1; \
fi
endef

.PHONY: fmt
fmt: ## Format Go sources.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: lint
lint: golangci-lint ## Run golangci-lint.
	$(GOLANGCI_LINT) run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint and apply fixes.
	$(GOLANGCI_LINT) run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify the golangci-lint configuration.
	$(GOLANGCI_LINT) config verify

.PHONY: lint-extra
lint-extra: actionlint shellcheck ## Lint things Go tooling does not cover.
	$(ACTIONLINT)
	$(SHELLCHECK) hack/*.sh

.PHONY: tidy
tidy: ## Tidy go.mod and go.sum.
	go mod tidy

##@ Test

GINKGO_PARALLEL ?= false
GINKGO_PROCS ?= 2
GINKGO_FLAGS := -v --race --randomize-all --randomize-suites
ifeq ($(GINKGO_PARALLEL),true)
	GINKGO_FLAGS += -p --procs=$(GINKGO_PROCS)
endif

# Unit tests are hermetic: no cluster, no envtest binaries, no network.
.PHONY: test-unit
test-unit: ## Run unit tests.
	go test -race -v $(GO_TEST_FLAGS) ./api/... ./cmd/... ./internal/...

.PHONY: test
test: test-unit ## Convenience alias for test-unit.

.PHONY: test-integration
test-integration: manifests generate setup-envtest ginkgo ## Run integration tests against envtest.
	@K8S_VERSION="$(ENVTEST_K8S_VERSION)"; \
	if [ -z "$$K8S_VERSION" ]; then echo "ERROR: Failed to detect Kubernetes version"; exit 1; fi; \
	KUBEBUILDER_ASSETS="$$("$(ENVTEST)" use "$$K8S_VERSION" --bin-dir "$(LOCALBIN)" -p path)" || { \
		echo "ERROR: Failed to locate envtest assets for Kubernetes $$K8S_VERSION"; exit 1; \
	}; \
	if [ -z "$$KUBEBUILDER_ASSETS" ]; then \
		echo "ERROR: envtest reported no asset path for Kubernetes $$K8S_VERSION"; exit 1; \
	fi; \
	export KUBEBUILDER_ASSETS; \
	echo "$(GINKGO) $(GINKGO_FLAGS) ./test/integration/..."; \
	$(GINKGO) $(GINKGO_FLAGS) ./test/integration/...

.PHONY: test-e2e
test-e2e: ginkgo ## Run e2e tests against a kind cluster (see kind-up).
	@echo "$(GINKGO) $(GINKGO_FLAGS) --timeout=30m ./test/e2e/..."
	$(GINKGO) $(GINKGO_FLAGS) --timeout=30m ./test/e2e/...

.PHONY: coverage
coverage: setup-envtest ## Run unit and integration tests with coverage.
	@K8S_VERSION="$(ENVTEST_K8S_VERSION)"; \
	if [ -z "$$K8S_VERSION" ]; then echo "ERROR: Failed to detect Kubernetes version"; exit 1; fi; \
	KUBEBUILDER_ASSETS="$$("$(ENVTEST)" use "$$K8S_VERSION" --bin-dir "$(LOCALBIN)" -p path)" || { \
		echo "ERROR: Failed to locate envtest assets for Kubernetes $$K8S_VERSION"; exit 1; \
	}; \
	if [ -z "$$KUBEBUILDER_ASSETS" ]; then \
		echo "ERROR: envtest reported no asset path for Kubernetes $$K8S_VERSION"; exit 1; \
	fi; \
	export KUBEBUILDER_ASSETS; \
	go test -coverprofile=cover.out -covermode=atomic -coverpkg=./api/...,./internal/... \
		./api/... ./cmd/... ./internal/... ./test/integration/... && \
	go tool cover -func=cover.out | tail -1

.PHONY: kind-up
kind-up: kind ## Create a kind cluster with the manager built and deployed.
	$(MAKE) docker-build IMG=$(KIND_IMG)
	IMG=$(KIND_IMG) CONTAINER_ENGINE=$(CONTAINER_ENGINE) KIND=$(KIND) \
		KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) bash hack/setup-kind.sh

.PHONY: kind-down
kind-down: kind ## Delete the kind cluster.
	IMG=$(KIND_IMG) CONTAINER_ENGINE=$(CONTAINER_ENGINE) KIND=$(KIND) \
		KIND_CLUSTER_NAME=$(KIND_CLUSTER_NAME) bash hack/teardown-kind.sh

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build the manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run the manager against the configured cluster.
	go run cmd/main.go

.PHONY: docker-build
docker-build: require-container-engine ## Build the manager image.
	$(CONTAINER_ENGINE) build -t ${IMG} .

.PHONY: docker-push
docker-push: require-container-engine ## Push the manager image.
	$(CONTAINER_ENGINE) push ${IMG}

##@ Deployment

.PHONY: install
install: manifests kustomize ## Install the CRDs into the target cluster.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Remove the CRDs from the target cluster.
	$(KUSTOMIZE) build config/crd | $(KUBECTL) delete --ignore-not-found -f -

.PHONY: deploy
deploy: manifests kustomize ## Deploy the manager to the target cluster.
	cd config/manager && $(KUSTOMIZE) edit set image controller=${IMG}
	$(KUSTOMIZE) build config/default | $(KUBECTL) apply -f -

.PHONY: undeploy
undeploy: kustomize ## Remove the manager from the target cluster.
	$(KUSTOMIZE) build config/default | $(KUBECTL) delete --ignore-not-found -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
# go install rejects a relative GOBIN, and a relative symlink target would
# resolve against the link's own directory, so normalise an override once here
# rather than at each use. Prefixing CURDIR rather than calling abspath leaves
# the value as a single word; abspath splits on whitespace.
override LOCALBIN := $(if $(filter /%,$(LOCALBIN)),$(LOCALBIN),$(CURDIR)/$(LOCALBIN))
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
GOLANGCI_LINT ?= $(LOCALBIN)/golangci-lint
ENVTEST ?= $(LOCALBIN)/setup-envtest
KUSTOMIZE ?= $(LOCALBIN)/kustomize
ACTIONLINT ?= $(LOCALBIN)/actionlint
SHELLCHECK ?= $(LOCALBIN)/shellcheck
KIND ?= $(LOCALBIN)/kind
GINKGO ?= $(LOCALBIN)/ginkgo

## Tool Versions
CONTROLLER_TOOLS_VERSION ?= v0.23.0
GOLANGCI_LINT_VERSION ?= v2.15.0
KUSTOMIZE_VERSION ?= v5.8.1
ACTIONLINT_VERSION ?= v1.7.12
SHELLCHECK_VERSION ?= v0.11.0
KIND_VERSION ?= v0.33.0
# Resolved from go.mod so ginkgo version matches the test suite
GINKGO_VERSION ?= $(call gomodver,github.com/onsi/ginkgo/v2)
# Resolved from go.mod so the envtest tooling cannot drift from the libraries
# it must match.
ENVTEST_VERSION ?= $(call gomodver,sigs.k8s.io/controller-runtime)
ENVTEST_K8S_VERSION ?= $(shell printf '%s' '$(call gomodver,k8s.io/api)' | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Install kustomize.
$(KUSTOMIZE): | $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

# shellcheck ships per-OS/arch tarballs rather than as a Go module, so it
# cannot use go-install-tool.
.PHONY: shellcheck
shellcheck: $(SHELLCHECK) ## Install shellcheck.
$(SHELLCHECK): | $(LOCALBIN)
	rm -f $@*; \
	set -e ;\
	os="$$(uname -s | tr '[:upper:]' '[:lower:]')" ;\
	arch="$$(uname -m)"; [ "$$arch" = "arm64" ] && arch=aarch64 ;\
	echo "Downloading shellcheck $(SHELLCHECK_VERSION) ($$os.$$arch)" ;\
	curl -sSL "https://github.com/koalaman/shellcheck/releases/download/$(SHELLCHECK_VERSION)/shellcheck-$(SHELLCHECK_VERSION).$$os.$$arch.tar.xz" \
		| tar xJ -C "$(LOCALBIN)" --strip-components=1 "shellcheck-$(SHELLCHECK_VERSION)/shellcheck" ;\
	mv "$(LOCALBIN)/shellcheck" "$@-$(SHELLCHECK_VERSION)" ;\
	ln -sf "$@-$(SHELLCHECK_VERSION)" "$@"

.PHONY: actionlint
actionlint: $(ACTIONLINT) ## Install actionlint.
$(ACTIONLINT): | $(LOCALBIN)
	$(call go-install-tool,$(ACTIONLINT),github.com/rhysd/actionlint/cmd/actionlint,$(ACTIONLINT_VERSION))


.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Install controller-gen.
$(CONTROLLER_GEN): | $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Install golangci-lint.
$(GOLANGCI_LINT): | $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST.
	@K8S_VERSION="$(ENVTEST_K8S_VERSION)"; \
	if [ -z "$$K8S_VERSION" ]; then echo "ERROR: Failed to detect Kubernetes version"; exit 1; fi; \
	echo "Setting up envtest binaries for Kubernetes version $$K8S_VERSION..."; \
	"$(ENVTEST)" use $$K8S_VERSION --bin-dir "$(LOCALBIN)" -p path > /dev/null || { \
		echo "Error: Failed to set up envtest binaries for version $$K8S_VERSION."; \
		exit 1; \
	}

.PHONY: envtest
envtest: $(ENVTEST) ## Install setup-envtest locally if necessary.
$(ENVTEST): | $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: kind
kind: $(KIND) ## Install kind locally if necessary.
$(KIND): | $(LOCALBIN)
	$(call go-install-tool,$(KIND),sigs.k8s.io/kind,$(KIND_VERSION))

.PHONY: ginkgo
ginkgo: $(GINKGO) ## Install ginkgo locally if necessary.
$(GINKGO): | $(LOCALBIN)
	$(call go-install-tool,$(GINKGO),github.com/onsi/ginkgo/v2/ginkgo,$(GINKGO_VERSION))

# go-install-tool installs $2@$3 as $1-$3 and links $1 to it, so that changing
# a pinned version reinstalls rather than silently reusing the old binary.
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$(1)-$(3)" "$(1)"
endef
