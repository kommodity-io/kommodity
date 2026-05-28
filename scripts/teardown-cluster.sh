#!/bin/bash
set -e

KUBE_CONTEXT=$1
CLUSTER_NAME=$2
if [ -z "$CLUSTER_NAME" ] || [ -z "$KUBE_CONTEXT" ]; then
  echo "❌ Usage: $0 <kommodity-kube-context> <cluster-name>"
  exit 1
fi

KUBECTL="kubectl --context=${KUBE_CONTEXT}"
HELM="helm --kube-context=${KUBE_CONTEXT}"

echo "🗑️ Tearing down cluster: $CLUSTER_NAME"
echo "   Context: ${KUBE_CONTEXT}"
echo "   This will run: ${HELM} uninstall --ignore-not-found ${CLUSTER_NAME}"
read -r -p "Proceed with helm uninstall? [y/N] " confirm
case "${confirm}" in
  y) ;;
  *)
    echo "❌ Aborted by user."
    exit 1
    ;;
esac

${HELM} uninstall --ignore-not-found "${CLUSTER_NAME}"

# Wait for machines to be deleted
echo "⏳ Waiting for machines to be deleted..."
while true; do
  machine_count=$(${KUBECTL} get machines -l cluster.x-k8s.io/cluster-name="$CLUSTER_NAME" --no-headers | wc -l)
  if [ "$machine_count" -eq 0 ]; then
    break
  fi
  echo "⏳ $machine_count machines remaining..."
  sleep 5
done
echo "✅ All machines deleted."

# Remove finalizers on KubevirtCluster if present (ignore if already gone)
echo "🧹 Removing finalizers from KubevirtCluster/${CLUSTER_NAME} (if present)..."
${KUBECTL} patch kubevirtcluster "${CLUSTER_NAME}" \
  --type=merge -p '{"metadata":{"finalizers":[]}}' --ignore-not-found

# Wait for Cluster object to be deleted
CLUSTER_DELETE_TIMEOUT="${CLUSTER_DELETE_TIMEOUT:-600s}"
echo "⏳ Waiting up to ${CLUSTER_DELETE_TIMEOUT} for Cluster object to be deleted..."
if ! ${KUBECTL} wait --for=delete "cluster/${CLUSTER_NAME}" --timeout="${CLUSTER_DELETE_TIMEOUT}"; then
  echo "❌ Timed out waiting for Cluster object '${CLUSTER_NAME}' to be deleted."
  exit 1
fi
echo "✅ Cluster object deleted."

# Remove secrets related to this cluster
echo "🧹 Cleaning up secrets..."
${KUBECTL} delete secrets -l cluster.x-k8s.io/cluster-name="$CLUSTER_NAME"

echo "✅ Cluster teardown completed."
