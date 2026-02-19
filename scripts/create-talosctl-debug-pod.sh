#!/usr/bin/env bash
set -euo pipefail

# ─── Configuration ───────────────────────────────────────────────────────────
TALOSCTL_VERSION="${TALOSCTL_VERSION:-v1.12.2}"
POD_NAMESPACE="${POD_NAMESPACE:-default}"
SECRET_KEY="${SECRET_KEY:-talosconfig}"

# ─── Colors ──────────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

log()   { echo -e "${CYAN}[INFO]${NC}  $*"; }
ok()    { echo -e "${GREEN}[OK]${NC}    $*"; }
warn()  { echo -e "${YELLOW}[WARN]${NC}  $*"; }
fail()  { echo -e "${RED}[FAIL]${NC}  $*"; exit 1; }

# ─── Usage ───────────────────────────────────────────────────────────────────
usage() {
  cat <<EOF
Usage: $0 <upstream-context> <downstream-context> <cluster-name>

Creates a debug pod with talosctl in the downstream cluster, using the
talosconfig secret extracted from the upstream (Kommodity) cluster.

Arguments:
  upstream-context    kubectl context for the Kommodity management cluster
  downstream-context  kubectl context for the downstream (workload) cluster
  cluster-name        name of the cluster (used to find <cluster-name>-talosconfig secret)

Environment variables:
  TALOSCTL_VERSION    talosctl version to download (default: v1.12.2)
  POD_NAMESPACE       namespace for the debug pod (default: default)
  SECRET_KEY          key inside the upstream secret (default: talosconfig)
EOF
  exit 1
}

# ─── Parse arguments ─────────────────────────────────────────────────────────
if [ $# -lt 3 ]; then
  usage
fi

UPSTREAM_CONTEXT="$1"
DOWNSTREAM_CONTEXT="$2"
CLUSTER_NAME="$3"

UPSTREAM_SECRET="${CLUSTER_NAME}-talosconfig"
DOWNSTREAM_SECRET="${CLUSTER_NAME}-talosctl-debug"
POD_NAME="${CLUSTER_NAME}-talosctl-debug"

# ─── Cleanup on failure ─────────────────────────────────────────────────────
CREATED_SECRET=false
CREATED_POD=false
SETUP_COMPLETE=false

cleanup() {
  if [ "$SETUP_COMPLETE" = true ]; then
    return
  fi

  echo
  warn "Script interrupted or failed. Cleaning up..."

  if [ "$CREATED_POD" = true ]; then
    log "Deleting pod '$POD_NAME'..."
    kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" delete pod "$POD_NAME" --ignore-not-found --wait=false 2>/dev/null || true
  fi

  if [ "$CREATED_SECRET" = true ]; then
    log "Deleting secret '$DOWNSTREAM_SECRET'..."
    kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" delete secret "$DOWNSTREAM_SECRET" --ignore-not-found 2>/dev/null || true
  fi

  warn "Cleanup complete."
}

trap cleanup EXIT

# ─── Pre-flight checks ──────────────────────────────────────────────────────
log "Running pre-flight checks..."

command -v kubectl >/dev/null 2>&1 || fail "kubectl is not installed."
command -v jq      >/dev/null 2>&1 || fail "jq is not installed."
command -v yq      >/dev/null 2>&1 || fail "yq is not installed."

# Verify both contexts exist
kubectl config get-contexts "$UPSTREAM_CONTEXT" >/dev/null 2>&1 \
  || fail "Upstream context '$UPSTREAM_CONTEXT' not found in kubeconfig."
kubectl config get-contexts "$DOWNSTREAM_CONTEXT" >/dev/null 2>&1 \
  || fail "Downstream context '$DOWNSTREAM_CONTEXT' not found in kubeconfig."

ok "All prerequisites met."

# ─── Extract talosconfig from upstream ───────────────────────────────────────
log "Fetching secret '$UPSTREAM_SECRET' from upstream context '$UPSTREAM_CONTEXT'..."

TALOSCONFIG_B64=$(
  kubectl --context "$UPSTREAM_CONTEXT" get secret "$UPSTREAM_SECRET" -ojson \
    | jq -r ".data.\"$SECRET_KEY\" // empty"
) || fail "Could not fetch secret '$UPSTREAM_SECRET' from upstream."

if [ -z "$TALOSCONFIG_B64" ]; then
  fail "Secret '$UPSTREAM_SECRET' does not contain key '$SECRET_KEY'."
fi

TALOSCONFIG_DATA=$(echo "$TALOSCONFIG_B64" | base64 -d) \
  || fail "Failed to base64-decode talosconfig."

ok "Talosconfig extracted successfully."

# Extract node IPs from talosconfig for use in the summary.
NODE_IPS=$(echo "$TALOSCONFIG_DATA" \
  | yq -r ".contexts.\"$CLUSTER_NAME\".nodes // .contexts.\"$CLUSTER_NAME\".endpoints // [] | join(\",\")" \
  || true)

if [ -z "$NODE_IPS" ]; then
  warn "Could not extract node IPs from talosconfig."
fi

# ─── Create secret in downstream cluster ─────────────────────────────────────
log "Creating secret '$DOWNSTREAM_SECRET' in downstream context '$DOWNSTREAM_CONTEXT' (namespace: $POD_NAMESPACE)..."

# Delete existing secret if present
if kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" get secret "$DOWNSTREAM_SECRET" >/dev/null 2>&1; then
  warn "Secret '$DOWNSTREAM_SECRET' already exists, replacing..."
  kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" delete secret "$DOWNSTREAM_SECRET" --ignore-not-found
fi

kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" create secret generic "$DOWNSTREAM_SECRET" \
  --from-literal=talosconfig="$TALOSCONFIG_DATA"

CREATED_SECRET=true
ok "Secret created in downstream cluster."

# ─── Create debug pod ───────────────────────────────────────────────────────
# An Alpine init container downloads the talosctl binary from GitHub into a shared
# emptyDir volume. The main Alpine container then has both a shell and talosctl.
log "Creating debug pod '$POD_NAME' in downstream context '$DOWNSTREAM_CONTEXT' (namespace: $POD_NAMESPACE)..."

# Delete existing pod if present
if kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" get pod "$POD_NAME" >/dev/null 2>&1; then
  warn "Pod '$POD_NAME' already exists, replacing..."
  kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" delete pod "$POD_NAME" --wait=true
fi

kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" apply -f - <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: ${POD_NAME}
  namespace: ${POD_NAMESPACE}
  labels:
    app: talosctl-debug
    cluster: ${CLUSTER_NAME}
spec:
  initContainers:
    - name: fetch-talosctl
      image: alpine:3.21
      command: ["sh", "-c"]
      args:
        - |
          ARCH=\$(uname -m)
          case "\$ARCH" in
            x86_64)  ARCH=amd64 ;;
            aarch64) ARCH=arm64 ;;
          esac
          wget -qO /tools/talosctl "https://github.com/siderolabs/talos/releases/download/\${TALOSCTL_VERSION}/talosctl-linux-\${ARCH}" && \
          chmod +x /tools/talosctl
      env:
        - name: TALOSCTL_VERSION
          value: "${TALOSCTL_VERSION}"
      volumeMounts:
        - name: tools
          mountPath: /tools
  containers:
    - name: debug
      image: alpine:3.21
      command: ["sleep", "infinity"]
      env:
        - name: TALOSCONFIG
          value: /talos/talosconfig
        - name: PATH
          value: /tools:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
      volumeMounts:
        - name: talosconfig
          mountPath: /talos
          readOnly: true
        - name: tools
          mountPath: /tools
  volumes:
    - name: talosconfig
      secret:
        secretName: ${DOWNSTREAM_SECRET}
    - name: tools
      emptyDir: {}
  restartPolicy: Never
EOF

CREATED_POD=true
ok "Debug pod created."

# ─── Wait for pod to be ready ────────────────────────────────────────────────
log "Waiting for pod '$POD_NAME' to be running..."

kubectl --context "$DOWNSTREAM_CONTEXT" -n "$POD_NAMESPACE" wait pod "$POD_NAME" \
  --for=condition=Ready --timeout=120s \
  || fail "Pod did not become ready within 120s."

ok "Pod is running."

# ─── Mark setup complete (skip cleanup on normal exit) ───────────────────────
SETUP_COMPLETE=true

# ─── Summary ─────────────────────────────────────────────────────────────────
echo
echo "═══════════════════════════════════════════════════════════════"
ok "Debug pod is ready!"
echo
echo -e "  ${CYAN}Attach to pod:${NC}"
echo "    kubectl --context $DOWNSTREAM_CONTEXT -n $POD_NAMESPACE exec -it $POD_NAME -- sh"
echo
echo -e "  ${CYAN}Run talosctl from your shell:${NC}"
if [ -n "$NODE_IPS" ]; then
  echo "    kubectl --context $DOWNSTREAM_CONTEXT -n $POD_NAMESPACE exec -it $POD_NAME -- talosctl version --nodes $NODE_IPS"
  echo "    kubectl --context $DOWNSTREAM_CONTEXT -n $POD_NAMESPACE exec -it $POD_NAME -- talosctl get members --nodes $NODE_IPS"
else
  echo "    kubectl --context $DOWNSTREAM_CONTEXT -n $POD_NAMESPACE exec -it $POD_NAME -- talosctl version --nodes <node-ip>"
  echo "    kubectl --context $DOWNSTREAM_CONTEXT -n $POD_NAMESPACE exec -it $POD_NAME -- talosctl get members --nodes <node-ip>"
fi
echo
echo -e "  ${CYAN}Cleanup:${NC}"
echo "    kubectl --context $DOWNSTREAM_CONTEXT -n $POD_NAMESPACE delete pod $POD_NAME"
echo "    kubectl --context $DOWNSTREAM_CONTEXT -n $POD_NAMESPACE delete secret $DOWNSTREAM_SECRET"
echo "═══════════════════════════════════════════════════════════════"
