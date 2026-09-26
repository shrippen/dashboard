package sources

import (
	"net"
	"strings"

	"andon/internal/drivers/httpclient"
)

// NetMode is the instance's outbound network mode.
type NetMode string

const (
	NetOpen      NetMode = "open"
	NetAllowlist NetMode = "allowlist"
)

// NetworkPolicy restricts which hosts sources may reach:
//
//	open        everything
//	allowlist   listed hosts, listed networks, public addresses (if Public)
type NetworkPolicy struct {
	Mode     NetMode
	Networks []string // CIDR, e.g. 192.168.10.0/24
	Hosts    []string
	Public   bool
}

// ParseNetworks validates CIDR entries ("10.0.0.0/8").
func ParseNetworks(cidrs []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(strings.TrimSpace(c))
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// ApplyNetwork installs policy as the process-wide egress guard.
func ApplyNetwork(policy NetworkPolicy) error {
	if policy.Mode != NetAllowlist {
		httpclient.SetGuard(nil)
		return nil
	}
	nets, err := ParseNetworks(policy.Networks)
	if err != nil {
		return err
	}
	hosts := make(map[string]bool, len(policy.Hosts))
	for _, h := range policy.Hosts {
		hosts[strings.ToLower(strings.TrimSpace(h))] = true
	}

	httpclient.SetGuard(func(host string, addrs []net.IP) bool {
		if hosts[strings.ToLower(host)] {
			return true
		}
		for _, addr := range addrs {
			if !allowedAddr(addr, nets, policy) {
				return false
			}
		}
		return true
	})
	return nil
}

func allowedAddr(addr net.IP, nets []*net.IPNet, policy NetworkPolicy) bool {
	if policy.Public && isGlobal(addr) {
		return true
	}
	for _, n := range nets {
		if n.Contains(addr) {
			return true
		}
	}
	return false
}

// isGlobal: routable on the internet, not private, loopback or link-local.
func isGlobal(ip net.IP) bool {
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast()
}
