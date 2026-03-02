package talosproxy

import "net"

// Interceptor manages network traffic interception rules that redirect
// connections destined for workload cluster node IPs to the local proxy.
type Interceptor interface {
	// UpdateRules replaces the current interception rules with rules for the given CIDRs.
	UpdateRules(cidrs []*net.IPNet) error
	// Cleanup removes all interception rules.
	Cleanup() error
}
