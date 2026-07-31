package talosproxy

import (
	"fmt"
	"net"
	"sync"
)

// CIDREntry holds the mapping between a cluster and its CIDR.
type CIDREntry struct {
	ClusterName string
	Namespace   string
	CIDR        *net.IPNet
}

// CIDRRegistry maintains a mapping of CIDRs to cluster names for routing
// intercepted connections to the correct workload cluster tunnel.
type CIDRRegistry struct {
	mu      sync.RWMutex
	entries map[string]*CIDREntry // keyed by cluster name
}

// NewCIDRRegistry creates a new empty CIDR registry.
func NewCIDRRegistry() *CIDRRegistry {
	return &CIDRRegistry{
		entries: make(map[string]*CIDREntry),
	}
}

// Register adds or updates a CIDR mapping for a cluster. It returns an error
// if the CIDR overlaps a CIDR already registered for a different cluster;
// re-registering the same cluster with a new CIDR is always allowed.
func (r *CIDRRegistry) Register(
	clusterName string,
	namespace string,
	cidr *net.IPNet,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for existingCluster, entry := range r.entries {
		if existingCluster == clusterName {
			continue
		}

		if cidrsOverlap(entry.CIDR, cidr) {
			return fmt.Errorf("%w: cluster %q (%s) overlaps existing cluster %q (%s)",
				ErrCIDROverlap, clusterName, cidr.String(), existingCluster, entry.CIDR.String())
		}
	}

	r.entries[clusterName] = &CIDREntry{
		ClusterName: clusterName,
		Namespace:   namespace,
		CIDR:        cidr,
	}

	return nil
}

// cidrsOverlap reports whether two CIDR blocks overlap. Two power-of-two aligned
// CIDR blocks overlap if and only if one contains the network address of the
// other, so checking containment in both directions is a complete overlap test.
func cidrsOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

// Deregister removes a cluster's CIDR mapping.
func (r *CIDRRegistry) Deregister(clusterName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.entries, clusterName)
}

// Lookup finds the cluster entry whose CIDR contains the given IP address.
func (r *CIDRRegistry) Lookup(ipAddr net.IP) (*CIDREntry, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, entry := range r.entries {
		if entry.CIDR.Contains(ipAddr) {
			return entry, nil
		}
	}

	return nil, fmt.Errorf("%w: %s", ErrCIDRNotFound, ipAddr.String())
}

// Len returns the number of registered entries.
func (r *CIDRRegistry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.entries)
}
