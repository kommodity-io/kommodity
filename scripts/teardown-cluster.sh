#!/bin/bash
set -e

CLUSTER_NAME=$1
if [ -z "$CLUSTER_NAME" ]; then
  echo "Usage: $0 <cluster-name>"
  exit 1
fi

echo "🗑️ Tearing down cluster: $CLUSTER_NAME"
helm uninstall "${CLUSTER_NAME}"

# Wait for machines to be deleted
echo "⏳ Waiting for machines to be deleted..."
while true; do
  machine_count=$(kubectl get machines -l cluster.x-k8s.io/cluster-name="$CLUSTER_NAME" --no-headers | wc -l)
  if [ "$machine_count" -eq 0 ]; then
    break
  fi
  echo "⏳ $machine_count machines remaining..."
  sleep 5
done
echo "✅ All machines deleted."

# Wait for Cluster object to be deleted
echo "⏳ Waiting for Cluster object to be deleted..."
while kubectl get cluster "$CLUSTER_NAME" &>/dev/null; do
  sleep 5
done
echo "✅ Cluster object deleted."

# Remove secrets related to this cluster
echo "🧹 Cleaning up secrets..."
kubectl delete secrets -l cluster.x-k8s.io/cluster-name="$CLUSTER_NAME"

echo "✅ Cluster teardown completed."
