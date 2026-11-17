#!/usr/bin/env bash
set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
#
# Override via environment:
#   KIND_CLUSTER_NAME  cluster name (default: gateway-controller-dev)
#   GWAPI_VERSION      Gateway API CRD version (default: v1.5.1)
#   METALLB_VERSION    MetalLB version (default: v0.14.9)
#
# Projects should copy this file and add project-specific
# deployment functions after the common infrastructure.

CLUSTER_NAME="${KIND_CLUSTER_NAME:-gateway-controller-dev}"
GWAPI_VERSION="${GWAPI_VERSION:-v1.5.1}"
METALLB_VERSION="${METALLB_VERSION:-v0.14.9}"
CONTAINER_ENGINE="${CONTAINER_ENGINE:-$(command -v podman || command -v docker || true)}"
KIND="${KIND:-kind}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KUBECTL="kubectl --context kind-${CLUSTER_NAME}"
# Honor explicit KIND_EXPERIMENTAL_PROVIDER, otherwise derive from CONTAINER_ENGINE
KIND_PROVIDER="${KIND_EXPERIMENTAL_PROVIDER:-$(basename "${CONTAINER_ENGINE}")}"

# ---------------------------------------------------------------------------
# KIND Cluster
# ---------------------------------------------------------------------------

create_cluster() {
    if KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" ${KIND} get clusters 2>/dev/null | grep -qx "${CLUSTER_NAME}"; then
        echo "==> Cluster '${CLUSTER_NAME}' already exists, reusing."
    else
        echo "==> Creating KIND cluster '${CLUSTER_NAME}'..."
        KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" ${KIND} create cluster \
            --name "${CLUSTER_NAME}" \
            --config "${SCRIPT_DIR}/kind-config.yaml" \
            --wait 60s
    fi
}

# ---------------------------------------------------------------------------
# Gateway API CRDs
# ---------------------------------------------------------------------------

install_gateway_api() {
    echo "==> Installing Gateway API CRDs ${GWAPI_VERSION}..."
    ${KUBECTL} apply -f \
        "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GWAPI_VERSION}/standard-install.yaml"
}

# ---------------------------------------------------------------------------
# MetalLB
# ---------------------------------------------------------------------------

install_metallb() {
    echo "==> Installing MetalLB ${METALLB_VERSION}..."
    ${KUBECTL} apply -f \
        "https://raw.githubusercontent.com/metallb/metallb/${METALLB_VERSION}/config/manifests/metallb-native.yaml"
    ${KUBECTL} wait --namespace metallb-system \
        --for=condition=ready pod \
        --selector=app=metallb \
        --timeout=300s
}

configure_metallb_pool() {
    echo "==> Configuring MetalLB IP pool..."
    SUBNET=""

    # Parse network subnet based on the container engine used by kind.
    # Filter for IPv4 only (exclude IPv6).
    if [ "${KIND_PROVIDER}" = "podman" ]; then
        # Podman uses .Subnets format; filter by IP length (4 = IPv4, 16 = IPv6)
        SUBNET=$("${CONTAINER_ENGINE}" network inspect \
            -f '{{range .Subnets}}{{if eq (len .Subnet.IP) 4}}{{.Subnet}}{{end}}{{end}}' \
            kind 2>/dev/null || true)
    else
        # Docker uses .IPAM.Config format
        SUBNET=$("${CONTAINER_ENGINE}" network inspect kind \
            -f '{{range .IPAM.Config}}{{.Subnet}} {{end}}' 2>/dev/null | \
            tr ' ' '\n' | grep '\.' | head -1 || true)
    fi

    if [ -z "${SUBNET}" ]; then
        echo "ERROR: Unable to determine network subnet for 'kind' network"
        exit 1
    fi

    # Calculate IP range based on subnet size. Adapt to both large (/16) and small (/24) subnets.
    IFS='.' read -r a b c _ <<< "${SUBNET%%/*}"
    PREFIX="${SUBNET#*/}"
    if [ "${PREFIX}" -le 16 ]; then
        POOL="${a}.${b}.255.200-${a}.${b}.255.210"
    else
        POOL="${a}.${b}.${c}.200-${a}.${b}.${c}.210"
    fi

    cat <<EOF | ${KUBECTL} apply -f -
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: kind-pool
  namespace: metallb-system
spec:
  addresses:
    - ${POOL}
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: l2
  namespace: metallb-system
EOF
}

# ---------------------------------------------------------------------------
# Manager (project-specific)
# ---------------------------------------------------------------------------

IMG="${IMG:-gateway-controller:dev}"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
KUSTOMIZATION="${REPO_ROOT}/config/manager/kustomization.yaml"

# make deploy runs 'kustomize edit set image', which rewrites the tracked
# kustomization in place. Put it back so kind-up leaves the working tree
# clean however it exits, and a dev tag cannot be committed by accident.
restore_kustomization() {
    if [ -n "${SAVED_KUSTOMIZATION:-}" ] && [ -f "${SAVED_KUSTOMIZATION}" ]; then
        cp "${SAVED_KUSTOMIZATION}" "${KUSTOMIZATION}"
        rm -f "${SAVED_KUSTOMIZATION}"
        SAVED_KUSTOMIZATION=""
    fi
}

load_images() {
    echo "==> Loading ${IMG} into the cluster..."
    local image_name="${IMG}"

    # kind reads from the docker daemon directly; podman has to hand over an
    # archive instead. Podman prefixes local images with localhost/.
    if [ "${KIND_PROVIDER}" = "podman" ]; then
        local archive
        local save_ref="${IMG}"
        archive="$(mktemp -t gateway-controller-XXXXXX.tar)"

        # Normalize unqualified local images. If IMG contains no /, it's a local
        # image name and should be tagged under docker.io/library for loading.
        if [[ "${IMG}" != */* ]]; then
            save_ref="docker.io/library/${IMG}"
            "${CONTAINER_ENGINE}" tag "localhost/${IMG}" "${save_ref}" 2>/dev/null || true
        fi

        "${CONTAINER_ENGINE}" save -o "${archive}" "${save_ref}"
        KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" ${KIND} load image-archive "${archive}" --name "${CLUSTER_NAME}"
        rm -f "${archive}"
    else
        KIND_EXPERIMENTAL_PROVIDER="${KIND_PROVIDER}" ${KIND} load docker-image "${image_name}" --name "${CLUSTER_NAME}"
    fi
}

deploy_controller() {
    echo "==> Deploying the manager..."

    SAVED_KUSTOMIZATION="$(mktemp)"
    cp "${KUSTOMIZATION}" "${SAVED_KUSTOMIZATION}"
    trap restore_kustomization EXIT
    make -C "${REPO_ROOT}" deploy IMG="${IMG}" KUBECTL="${KUBECTL}"
    restore_kustomization
    trap - EXIT

    # IMG is a fixed tag, so on a repeat run the apply above is a no-op and
    # the pod keeps its old binary. Roll it, and wait on the new ReplicaSet:
    # --for=condition=available returned at once, satisfied by the pod being
    # replaced.
    ${KUBECTL} rollout restart --namespace gateway-controller-system \
        deployment/gateway-controller-controller-manager
    ${KUBECTL} rollout status --namespace gateway-controller-system \
        deployment/gateway-controller-controller-manager \
        --timeout=180s
}

# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

if ! command -v "${KIND}" &>/dev/null; then
    echo "ERROR: kind is required but not found at ${KIND}"
    exit 1
fi

if ! command -v kubectl &>/dev/null; then
    echo "ERROR: kubectl is required but not found"
    exit 1
fi

if [ -z "${CONTAINER_ENGINE}" ]; then
    echo "ERROR: no container engine found (install podman or docker)"
    exit 1
fi

create_cluster
# Add back if/when gateway API is needed
#install_gateway_api
install_metallb
configure_metallb_pool

load_images
deploy_controller

echo "==> KIND cluster ready."
echo ""
echo "    Cluster:  ${CLUSTER_NAME}"
echo "    Context:  kind-${CLUSTER_NAME}"
