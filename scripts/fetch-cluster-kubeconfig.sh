#!/bin/bash
set -e

CLUSTER_NAME=$1
if [ -z "$CLUSTER_NAME" ]; then
  echo "Usage: $0 <cluster-name> <kommodity-context>"
  exit 1
fi

KOMMODITY_CONTEXT=$2
if [ -z "$KOMMODITY_CONTEXT" ]; then
  echo "Usage: $0 <cluster-name> <kommodity-context>"
  exit 1
fi

TALOS_CONFIG_PATH=/tmp/talosconfig

kubectl --context "$KOMMODITY_CONTEXT" get secrets "${CLUSTER_NAME}-talosconfig" -ojson | jq -r '.data.talosconfig' | base64 -d > $TALOS_CONFIG_PATH

node_IP=$(yq -r ".contexts.\"${CLUSTER_NAME}\".endpoints[0]" "$TALOS_CONFIG_PATH")

echo "🔗 Updating kubeconfig with node IP: $node_IP"

talosctl --talosconfig "$TALOS_CONFIG_PATH" kubeconfig -n "$node_IP"

echo "✅ Kubeconfig updated successfully."

rm -f $TALOS_CONFIG_PATH
