# Makefile for DFW (Distributed Firewall)
# Based on implementation plan

# Image URL to use all building/pushing image targets
CONTROLLER_IMG ?= dfw-controller:latest
AGENT_IMG ?= dfw-agent:latest

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.30.0

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: generate fmt vet ## Build manager binary.
	go build -o bin/dfw-controller ./cmd/dfw-controller
	go build -o bin/dfw-agent ./cmd/dfw-agent

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/dfw-controller/main.go

##@ Docker

# Docker Hub support: set DOCKER_HUB_REPO (e.g. yourusername) in env or GitHub secrets
# Then images become ${DOCKER_HUB_REPO}/dfw-controller and /dfw-agent
DOCKER_HUB_REPO ?= dfw

CONTROLLER_IMG := $(if $(filter dfw-controller:latest,$(CONTROLLER_IMG)),$(DOCKER_HUB_REPO)/dfw-controller:latest,$(CONTROLLER_IMG))
AGENT_IMG := $(if $(filter dfw-agent:latest,$(AGENT_IMG)),$(DOCKER_HUB_REPO)/dfw-agent:latest,$(AGENT_IMG))

.PHONY: docker-build-controller
docker-build-controller: ## Build the docker image for controller
	docker build -t ${CONTROLLER_IMG} -f Dockerfile.controller .

.PHONY: docker-build-agent
docker-build-agent: ## Build the docker image for agent (includes LLVM for runtime eBPF compile)
	docker build -t ${AGENT_IMG} -f Dockerfile.agent .

.PHONY: docker-push-controller
docker-push-controller: docker-build-controller ## Push controller image to Docker Hub (assumes docker login done with DOCKER_HUB_TOKEN)
	docker push ${CONTROLLER_IMG}

.PHONY: docker-push-agent
docker-push-agent: docker-build-agent ## Push agent image to Docker Hub
	docker push ${AGENT_IMG}

.PHONY: docker-push
docker-push: docker-push-controller docker-push-agent ## Push both images

.PHONY: docker-login
docker-login: ## Login to Docker Hub using DOCKER_HUB_TOKEN (for local use)
	@echo "Logging into Docker Hub as ${DOCKER_HUB_REPO}..."
	@echo "${DOCKER_HUB_TOKEN}" | docker login -u "${DOCKER_HUB_REPO}" --password-stdin


##@ Deployment

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build config/crd | kubectl delete --ignore-not-found -f -

##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest

## Tool Versions
KUSTOMIZE_VERSION ?= v5.4.1
CONTROLLER_TOOLS_VERSION ?= v0.15.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
	$(KUSTOMIZE): $(LOCALBIN)
	@if test -x $(LOCALBIN)/kustomize && ! $(LOCALBIN)/kustomize version | grep -q $(KUSTOMIZE_VERSION); then \
		echo "$(LOCALBIN)/kustomize version is not expected $(KUSTOMIZE_VERSION). Removing it before installing."; \
		rm -rf $(LOCALBIN)/kustomize; \
	fi
	test -s $(LOCALBIN)/kustomize || { curl -Ss "https://raw.githubusercontent.com/kubernetes-sigs/kustomize/master/hack/install_kustomize.sh" | bash -s -- $(subst v,,$(KUSTOMIZE_VERSION)) $(LOCALBIN); }

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
	$(CONTROLLER_GEN): $(LOCALBIN)
	test -s $(LOCALBIN)/controller-gen && $(LOCALBIN)/controller-gen --version | grep -q $(CONTROLLER_TOOLS_VERSION) || \
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_TOOLS_VERSION)

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
	$(ENVTEST): $(LOCALBIN)
	test -s $(LOCALBIN)/setup-envtest || GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

# NOTE: Requires Go 1.22+. For eBPF agent image, use multi-stage with clang/LLVM.

# eBPF (data-driven DFW program). Requires clang/llc in PATH or use docker agent build.
# Produces bpf/dfw.bpf.o (loadable ELF). In real: agent container compiles the .c at boot.
CLANG ?= clang
CLANG_FLAGS ?= -O2 -g -target bpf -c -D__TARGET_ARCH_x86 -I/usr/include -I/usr/include/bpf

.PHONY: bpf
bpf: ## Compile the DFW eBPF C to ELF object (for inspection / agent).
	$(CLANG) $(CLANG_FLAGS) bpf/dfw.bpf.c -o bpf/dfw.bpf.o
	@echo "bpf/dfw.bpf.o ready (use bpftool prog load, or let agent compile source)"

.PHONY: bpf-clean
bpf-clean:
	rm -f bpf/*.o
