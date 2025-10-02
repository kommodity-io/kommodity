# This script sets up a kind management cluster
# It is meant to be used for debugging and testing purposes, so we can directly compare the behavior of kommodity
# against the behavior of CAPI 

# The following providers will be installed: 
#   - cluster-api core provider
#   - talos control plane provider
#   - talos bootstrap provider
#   - scaleway infrastructure provider

# Prerequisites:
#   - kind installed (https://kind.sigs.k8s.io/docs/user/quick-start/)
#   - clusterctl installed (https://cluster-api.sigs.k8s.io/user/quick-start#install-clusterctl)

set -euo pipefail

echo "🚀 Setting up kind management cluster..."
kind create cluster --name kind-management

# Initialize the management cluster
echo "🔧 Initializing cluster with CAPI, Talos, and Scaleway providers..."
clusterctl init --infrastructure scaleway --control-plane talos --bootstrap talos

echo "✅ Management cluster setup complete."