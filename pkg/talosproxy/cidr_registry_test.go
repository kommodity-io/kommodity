package talosproxy_test

import (
	"net"
	"testing"

	"github.com/kommodity-io/kommodity/pkg/talosproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustParseCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()

	_, ipNet, err := net.ParseCIDR(cidr)
	require.NoError(t, err)

	return ipNet
}

func TestCIDRRegistry_RegisterAndLookup(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	cidr := mustParseCIDR(t, "10.200.0.0/20")

	require.NoError(t, registry.Register("cluster-a", "default", cidr))

	entry, err := registry.Lookup(net.ParseIP("10.200.0.5"))
	require.NoError(t, err)
	assert.Equal(t, "cluster-a", entry.ClusterName)
	assert.Equal(t, "default", entry.Namespace)
}

func TestCIDRRegistry_LookupNotFound(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	cidr := mustParseCIDR(t, "10.200.0.0/20")

	require.NoError(t, registry.Register("cluster-a", "default", cidr))

	_, err := registry.Lookup(net.ParseIP("192.168.1.1"))
	require.Error(t, err)
	assert.ErrorIs(t, err, talosproxy.ErrCIDRNotFound)
}

func TestCIDRRegistry_MultipleClusters(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	cidrA := mustParseCIDR(t, "10.200.0.0/20")
	cidrB := mustParseCIDR(t, "10.201.0.0/20")

	require.NoError(t, registry.Register("cluster-a", "ns-a", cidrA))
	require.NoError(t, registry.Register("cluster-b", "ns-b", cidrB))

	entryA, err := registry.Lookup(net.ParseIP("10.200.0.5"))
	require.NoError(t, err)
	assert.Equal(t, "cluster-a", entryA.ClusterName)

	entryB, err := registry.Lookup(net.ParseIP("10.201.0.5"))
	require.NoError(t, err)
	assert.Equal(t, "cluster-b", entryB.ClusterName)
}

func TestCIDRRegistry_Deregister(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	cidr := mustParseCIDR(t, "10.200.0.0/20")

	require.NoError(t, registry.Register("cluster-a", "default", cidr))
	registry.Deregister("cluster-a")

	_, err := registry.Lookup(net.ParseIP("10.200.0.5"))
	require.Error(t, err)
	assert.ErrorIs(t, err, talosproxy.ErrCIDRNotFound)
}

func TestCIDRRegistry_Len(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	assert.Equal(t, 0, registry.Len())

	cidr := mustParseCIDR(t, "10.200.0.0/20")
	require.NoError(t, registry.Register("cluster-a", "default", cidr))
	assert.Equal(t, 1, registry.Len())

	require.NoError(t, registry.Register("cluster-b", "default", mustParseCIDR(t, "10.201.0.0/20")))
	assert.Equal(t, 2, registry.Len())

	registry.Deregister("cluster-a")
	assert.Equal(t, 1, registry.Len())
}

func TestCIDRRegistry_OverwriteExisting(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	cidrOld := mustParseCIDR(t, "10.200.0.0/20")
	cidrNew := mustParseCIDR(t, "10.202.0.0/20")

	require.NoError(t, registry.Register("cluster-a", "default", cidrOld))
	require.NoError(t, registry.Register("cluster-a", "default", cidrNew))

	// Old CIDR should no longer match
	_, err := registry.Lookup(net.ParseIP("10.200.0.5"))
	require.Error(t, err)

	// New CIDR should match
	entry, err := registry.Lookup(net.ParseIP("10.202.0.5"))
	require.NoError(t, err)
	assert.Equal(t, "cluster-a", entry.ClusterName)

	assert.Equal(t, 1, registry.Len())
}

func TestCIDRRegistry_RejectsOverlappingCIDR(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	supernet := mustParseCIDR(t, "10.0.0.0/8")
	subnet := mustParseCIDR(t, "10.200.16.0/20")

	require.NoError(t, registry.Register("cluster-a", "default", supernet))

	err := registry.Register("cluster-b", "default", subnet)
	require.Error(t, err)
	require.ErrorIs(t, err, talosproxy.ErrCIDROverlap)

	// Overlapping cluster must not be registered.
	assert.Equal(t, 1, registry.Len())

	_, lookupErr := registry.Lookup(net.ParseIP("10.200.16.5"))
	require.NoError(t, lookupErr, "supernet should still own the address space")
}

func TestCIDRRegistry_RejectsOverlappingReRegistration(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	cidrA := mustParseCIDR(t, "10.200.0.0/20")
	cidrB := mustParseCIDR(t, "10.201.0.0/20")
	overlapping := mustParseCIDR(t, "10.0.0.0/8")

	require.NoError(t, registry.Register("cluster-a", "default", cidrA))
	require.NoError(t, registry.Register("cluster-b", "default", cidrB))

	// Re-registering cluster-a with a CIDR overlapping cluster-b must fail and
	// leave cluster-a's original entry intact.
	err := registry.Register("cluster-a", "default", overlapping)
	require.Error(t, err)
	require.ErrorIs(t, err, talosproxy.ErrCIDROverlap)

	assert.Equal(t, 2, registry.Len())

	entry, lookupErr := registry.Lookup(net.ParseIP("10.200.0.5"))
	require.NoError(t, lookupErr)
	assert.Equal(t, "cluster-a", entry.ClusterName)
}

func TestCIDRRegistry_RejectsSameNameDifferentNamespace(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	cidrA := mustParseCIDR(t, "10.200.0.0/20")
	cidrB := mustParseCIDR(t, "10.201.0.0/20")

	require.NoError(t, registry.Register("cluster-a", "ns-a", cidrA))

	// Same name, different namespace must be rejected instead of silently
	// overwriting the existing entry or bypassing the overlap check.
	err := registry.Register("cluster-a", "ns-b", cidrB)
	require.Error(t, err)
	require.ErrorIs(t, err, talosproxy.ErrClusterNameConflict)

	// The original entry must remain intact.
	assert.Equal(t, 1, registry.Len())

	entry, lookupErr := registry.Lookup(net.ParseIP("10.200.0.5"))
	require.NoError(t, lookupErr)
	assert.Equal(t, "cluster-a", entry.ClusterName)
	assert.Equal(t, "ns-a", entry.Namespace)
}

func TestCIDRRegistry_SameNameSameNamespaceAllowsReRegistration(t *testing.T) {
	t.Parallel()

	registry := talosproxy.NewCIDRRegistry()
	cidrOld := mustParseCIDR(t, "10.200.0.0/20")
	cidrNew := mustParseCIDR(t, "10.201.0.0/20")

	require.NoError(t, registry.Register("cluster-a", "ns-a", cidrOld))
	require.NoError(t, registry.Register("cluster-a", "ns-a", cidrNew))

	// New CIDR should match, old should not.
	entry, err := registry.Lookup(net.ParseIP("10.201.0.5"))
	require.NoError(t, err)
	assert.Equal(t, "cluster-a", entry.ClusterName)

	_, err = registry.Lookup(net.ParseIP("10.200.0.5"))
	require.Error(t, err)
}
