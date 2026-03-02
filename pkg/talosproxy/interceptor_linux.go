//go:build linux

package talosproxy

import (
	"fmt"
	"net"

	"github.com/google/nftables"
	"github.com/google/nftables/binaryutil"
	"github.com/google/nftables/expr"
)

const (
	nftTableName = "kommodity-proxy"
	nftChainName = "output"
	nftChainPrio = -100
	tcpProtocol  = 6
	// talosApidPort is the default Talos apid gRPC port that we intercept.
	talosApidPort = 50000
)

// NftablesInterceptor manages nftables rules for redirecting traffic to the local proxy.
type NftablesInterceptor struct {
	localPort int
	conn      *nftables.Conn
	table     *nftables.Table
	chain     *nftables.Chain
}

// NewNftablesInterceptor creates a new nftables-based interceptor.
func NewNftablesInterceptor(localPort int) (*NftablesInterceptor, error) {
	conn, err := nftables.New()
	if err != nil {
		return nil, fmt.Errorf("failed to create nftables connection: %w", err)
	}

	return &NftablesInterceptor{
		localPort: localPort,
		conn:      conn,
	}, nil
}

// UpdateRules replaces all nftables redirect rules with rules matching the provided CIDRs.
func (i *NftablesInterceptor) UpdateRules(cidrs []*net.IPNet) error {
	// Delete existing table to start fresh
	if i.table != nil {
		i.conn.DelTable(i.table)
	}

	// Create table
	i.table = i.conn.AddTable(&nftables.Table{
		Family: nftables.TableFamilyIPv4,
		Name:   nftTableName,
	})

	// Create output chain with NAT hook
	chainPolicy := nftables.ChainPolicyAccept
	i.chain = i.conn.AddChain(&nftables.Chain{
		Name:     nftChainName,
		Table:    i.table,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookOutput,
		Priority: nftablesChainPriority(nftChainPrio),
		Policy:   &chainPolicy,
	})

	// Add a redirect rule for each CIDR
	for _, cidr := range cidrs {
		err := i.addRedirectRule(cidr)
		if err != nil {
			return fmt.Errorf("failed to add redirect rule for CIDR %s: %w", cidr.String(), err)
		}
	}

	err := i.conn.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush nftables rules: %w", err)
	}

	return nil
}

// Cleanup removes the nftables table and all associated rules.
func (i *NftablesInterceptor) Cleanup() error {
	if i.table != nil {
		i.conn.DelTable(i.table)

		err := i.conn.Flush()
		if err != nil {
			return fmt.Errorf("failed to cleanup nftables rules: %w", err)
		}

		i.table = nil
		i.chain = nil
	}

	return nil
}

func (i *NftablesInterceptor) addRedirectRule(cidr *net.IPNet) error {
	ip := cidr.IP.To4()
	if ip == nil {
		return fmt.Errorf("only IPv4 CIDRs are supported: %s", cidr.String())
	}

	// Match: ip daddr <cidr> tcp dport 50000 redirect to :<localPort>
	i.conn.AddRule(&nftables.Rule{
		Table: i.table,
		Chain: i.chain,
		Exprs: []expr.Any{
			// Load IPv4 destination address into register 1
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseNetworkHeader,
				Offset:       16, // destination address offset in IPv4 header
				Len:          4,
			},
			// Bitwise AND with mask to get network address
			&expr.Bitwise{
				SourceRegister: 1,
				DestRegister:   1,
				Len:            4,
				Mask:           cidr.Mask,
				Xor:            net.IPv4Mask(0, 0, 0, 0),
			},
			// Compare against network address
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     ip,
			},
			// Load L4 protocol type
			&expr.Meta{
				Key:      expr.MetaKeyL4PROTO,
				Register: 1,
			},
			// Compare against TCP
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     []byte{tcpProtocol},
			},
			// Load TCP destination port into register 1
			&expr.Payload{
				DestRegister: 1,
				Base:         expr.PayloadBaseTransportHeader,
				Offset:       2, // destination port offset in TCP header
				Len:          2,
			},
			// Compare against port 50000
			&expr.Cmp{
				Op:       expr.CmpOpEq,
				Register: 1,
				Data:     binaryutil.BigEndian.PutUint16(uint16(talosApidPort)),
			},
			// Redirect to local port
			&expr.Immediate{
				Register: 1,
				Data:     binaryutil.BigEndian.PutUint16(uint16(i.localPort)),
			},
			&expr.Redir{
				RegisterProtoMin: 1,
			},
		},
	})

	return nil
}

func nftablesChainPriority(priority int) *nftables.ChainPriority {
	p := nftables.ChainPriority(priority)
	return &p
}
