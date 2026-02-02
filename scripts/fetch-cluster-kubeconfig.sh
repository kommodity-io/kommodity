#!/bin/bash
set -e

CLUSTER_NAME=$1
SOURCE_KUBECONFIG=$2

if [ -z "$CLUSTER_NAME" ]; then
  echo "Usage: $0 <cluster-name> [kubeconfig]"
  echo "  kubeconfig: Optional path to kubeconfig for fetching the secret"
  exit 1
fi

# Build kubectl args for source kubeconfig
KUBECTL_ARGS=""
if [ -n "$SOURCE_KUBECONFIG" ]; then
  if [ ! -f "$SOURCE_KUBECONFIG" ]; then
    echo "Error: Source kubeconfig '$SOURCE_KUBECONFIG' not found"
    exit 1
  fi
  KUBECTL_ARGS="--kubeconfig=$SOURCE_KUBECONFIG"
fi

TEMP_KUBECONFIG=$(mktemp)
KUBE_DIR="$HOME/.kube"
KUBE_CONFIG="$KUBE_DIR/config"

# Fetch the kubeconfig from the secret
if ! kubectl $KUBECTL_ARGS get secrets "${CLUSTER_NAME}-kubeconfig" &>/dev/null; then
  echo "Error: Could not find secret '${CLUSTER_NAME}-kubeconfig'"
  echo "Make sure you are on the Kommodity context."
  rm -f "$TEMP_KUBECONFIG"
  exit 1
fi

kubectl $KUBECTL_ARGS get secrets "${CLUSTER_NAME}-kubeconfig" -ojson | jq -r '.data.value' | base64 -d > "$TEMP_KUBECONFIG"

# Check if context already exists
if [ -f "$KUBE_CONFIG" ]; then
  CONTEXT_NAME=$(KUBECONFIG="$TEMP_KUBECONFIG" kubectl config current-context)

  if KUBECONFIG="$KUBE_CONFIG" kubectl config get-contexts "$CONTEXT_NAME" &>/dev/null; then
    echo "Context '$CONTEXT_NAME' already exists in $KUBE_CONFIG"
    read -p "Do you want to overwrite it? [y/N] " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
      echo "Aborted."
      rm -f "$TEMP_KUBECONFIG"
      exit 1
    fi
  fi
fi

# Merge into existing kubeconfig
if [ -f "$KUBE_CONFIG" ]; then
  # Backup existing config
  cp "$KUBE_CONFIG" "$KUBE_CONFIG.backup"
  KUBECONFIG="$TEMP_KUBECONFIG:$KUBE_CONFIG" kubectl config view --flatten > "$KUBE_CONFIG.merged"
  mv "$KUBE_CONFIG.merged" "$KUBE_CONFIG"
else
  # No existing config, just use the new one
  mv "$TEMP_KUBECONFIG" "$KUBE_CONFIG"
fi

# Cleanup
rm -f "$TEMP_KUBECONFIG"

echo "Kubeconfig for '$CLUSTER_NAME' merged into $KUBE_CONFIG"
