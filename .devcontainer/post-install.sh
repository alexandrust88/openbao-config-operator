#!/bin/bash
set -euo pipefail

echo "===================================="
echo "Kubebuilder DevContainer Setup"
echo "===================================="

# Verify running as root (required for installing to /usr/local/bin and /etc)
if [ "$(id -u)" -ne 0 ]; then
  echo "ERROR: This script must be run as root"
  exit 1
fi

echo ""
echo "Detecting system architecture..."
# Detect architecture using uname
MACHINE=$(uname -m)
case "${MACHINE}" in
  x86_64)
    ARCH="amd64"
    ;;
  aarch64|arm64)
    ARCH="arm64"
    ;;
  *)
    echo "WARNING: Unsupported architecture ${MACHINE}, defaulting to amd64"
    ARCH="amd64"
    ;;
esac
echo "Architecture: ${ARCH}"

echo ""
echo "------------------------------------"
echo "Setting up bash completion..."
echo "------------------------------------"

BASH_COMPLETIONS_DIR="/usr/share/bash-completion/completions"

# Enable bash-completion in root's .bashrc (devcontainer runs as root)
if ! grep -q "source /usr/share/bash-completion/bash_completion" ~/.bashrc 2>/dev/null; then
  echo 'source /usr/share/bash-completion/bash_completion' >> ~/.bashrc
  echo "Added bash-completion to .bashrc"
fi

echo ""
echo "------------------------------------"
echo "Installing development tools..."
echo "------------------------------------"

# Sums from each release's published checksum file; bump version and both sums together
# renovate: datasource=github-releases depName=kubernetes-sigs/kind
KIND_VERSION=v0.33.0
declare -A KIND_SHA256=([amd64]=aee6151561422756b764a4ae28e7f44cda5af5a9eead3cc9985112b1de8d8e0d [arm64]=20022bee6cfcd5086cb7234d218e3454e6090022f2a8f55d1fa7fcf42c3867a2)
# renovate: datasource=github-releases depName=kubernetes-sigs/kubebuilder
KUBEBUILDER_VERSION=v4.13.1
declare -A KUBEBUILDER_SHA256=([amd64]=c91a214f3cada120cd7468ee4633621da963e997cf19a1cf320ebd36e6324fd6 [arm64]=7d39d8c2ed3f0bde122b5ba4ee5106d07da3a920b6865a8be4a56e689f8a0a56)
# renovate: datasource=github-releases depName=kubernetes/kubernetes
KUBECTL_VERSION=v1.35.8
declare -A KUBECTL_SHA256=([amd64]=874d5e72dbb819f43cff16bcd1e4f8bac5b7f2361fe1e55049b0a6c676fb0cbf [arm64]=cc749967b62f4422260bc9c0aa7a7c55f45175ae38cb8d95767b5d2b7e04c1fd)

# install_verified <name> <url> <sha256>
install_verified() {
  local tmp
  tmp=$(mktemp)
  curl -fsSLo "${tmp}" "$2"
  echo "$3  ${tmp}" | sha256sum -c --quiet -
  install -m 0755 "${tmp}" "/usr/local/bin/$1"
  rm -f "${tmp}"
}

# Install kind
if ! command -v kind &> /dev/null; then
  echo "Installing kind ${KIND_VERSION}..."
  install_verified kind "https://github.com/kubernetes-sigs/kind/releases/download/${KIND_VERSION}/kind-linux-${ARCH}" "${KIND_SHA256[${ARCH}]}"
  echo "kind installed successfully"
fi

# Generate kind bash completion
if command -v kind &> /dev/null; then
  if kind completion bash > "${BASH_COMPLETIONS_DIR}/kind" 2>/dev/null; then
    echo "kind completion installed"
  else
    echo "WARNING: Failed to generate kind completion"
  fi
fi

# Install kubebuilder
if ! command -v kubebuilder &> /dev/null; then
  echo "Installing kubebuilder ${KUBEBUILDER_VERSION}..."
  install_verified kubebuilder "https://github.com/kubernetes-sigs/kubebuilder/releases/download/${KUBEBUILDER_VERSION}/kubebuilder_linux_${ARCH}" "${KUBEBUILDER_SHA256[${ARCH}]}"
  echo "kubebuilder installed successfully"
fi

# Generate kubebuilder bash completion
if command -v kubebuilder &> /dev/null; then
  if kubebuilder completion bash > "${BASH_COMPLETIONS_DIR}/kubebuilder" 2>/dev/null; then
    echo "kubebuilder completion installed"
  else
    echo "WARNING: Failed to generate kubebuilder completion"
  fi
fi

# Install kubectl
if ! command -v kubectl &> /dev/null; then
  echo "Installing kubectl ${KUBECTL_VERSION}..."
  install_verified kubectl "https://dl.k8s.io/release/${KUBECTL_VERSION}/bin/linux/${ARCH}/kubectl" "${KUBECTL_SHA256[${ARCH}]}"
  echo "kubectl installed successfully"
fi

# Generate kubectl bash completion
if command -v kubectl &> /dev/null; then
  if kubectl completion bash > "${BASH_COMPLETIONS_DIR}/kubectl" 2>/dev/null; then
    echo "kubectl completion installed"
  else
    echo "WARNING: Failed to generate kubectl completion"
  fi
fi

# Generate Docker bash completion
if command -v docker &> /dev/null; then
  if docker completion bash > "${BASH_COMPLETIONS_DIR}/docker" 2>/dev/null; then
    echo "docker completion installed"
  else
    echo "WARNING: Failed to generate docker completion"
  fi
fi

echo ""
echo "------------------------------------"
echo "Configuring Docker environment..."
echo "------------------------------------"

# Wait for Docker to be ready
echo "Waiting for Docker to be ready..."
for i in {1..30}; do
  if docker info >/dev/null 2>&1; then
    echo "Docker is ready"
    break
  fi
  if [ "$i" -eq 30 ]; then
    echo "WARNING: Docker not ready after 30s"
  fi
  sleep 1
done

# Create kind network (ignore if already exists)
if ! docker network inspect kind >/dev/null 2>&1; then
  if docker network create kind >/dev/null 2>&1; then
    echo "Created kind network"
  else
    echo "WARNING: Failed to create kind network (may already exist)"
  fi
fi

echo ""
echo "------------------------------------"
echo "Verifying installations..."
echo "------------------------------------"
kind version
kubebuilder version
kubectl version --client
docker --version
go version

echo ""
echo "===================================="
echo "DevContainer ready!"
echo "===================================="
echo "All development tools installed successfully."
echo "You can now start building Kubernetes operators."
