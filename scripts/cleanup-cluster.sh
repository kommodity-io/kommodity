#!/bin/bash
set -e

CLUSTER_NAME=$1
if [ -z "$CLUSTER_NAME" ]; then
  echo "❗ Usage: $0 <cluster-name>"
  exit 1
fi

echo "🔍 Preparing to delete cluster: $CLUSTER_NAME"

read -p "⚠️  Are you sure you want to delete cluster '$CLUSTER_NAME'? (yes/no): " confirmation
if [ "$confirmation" != "yes" ]; then
  echo "🛑 Aborted."
  exit 1
fi

echo "🧹 Deleting cluster '$CLUSTER_NAME'..."

echo "📦 Uninstalling Helm release..."
helm uninstall "$CLUSTER_NAME" && echo "✅ Helm release removed."

# TODO: Ultimately, helm uninstall should remove all resources, but it currently does not.
echo "🗑️  Removing cluster-related secrets..."
kubectl delete secrets -l cluster.x-k8s.io/cluster-name="$CLUSTER_NAME"

echo "🔧 Removing finalizers from ScalewayMachines..."
kubectl get scalewaymachine -l cluster.x-k8s.io/cluster-name="$CLUSTER_NAME" -o name | while read -r m; do
  kubectl patch "$m" --type=json -p='[{"op": "remove", "path": "/metadata/finalizers"}]'
done

echo "🎉 Cluster deletion workflow completed!"
