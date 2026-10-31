// Package serve is the JSON + WebSocket API of bx: projects, sessions and their states,
// the actions of the list pane, and a stream of the selected Claude pane per client.
// It listens on the Tailscale address only; Tailscale is the access control.
package serve

import (
	"fmt"
	"net"
)

// DefaultPort is the port `listen = "auto"` uses.
const DefaultPort = "7357"

var tailscaleNet = &net.IPNet{IP: net.IPv4(100, 64, 0, 0), Mask: net.CIDRMask(10, 32)}

// ResolveListen turns the config value into a host:port on the Tailscale interface.
func ResolveListen(cfg string) (string, error) { return resolveListen(cfg, net.InterfaceAddrs) }

func resolveListen(cfg string, addrs func() ([]net.Addr, error)) (string, error) {
	if cfg == "" || cfg == "auto" {
		list, err := addrs()
		if err != nil {
			return "", err
		}
		for _, a := range list {
			if n, ok := a.(*net.IPNet); ok && tailscaleNet.Contains(n.IP) {
				return net.JoinHostPort(n.IP.String(), DefaultPort), nil
			}
		}
		return "", fmt.Errorf("no Tailscale address (100.64.0.0/10) on this machine; is tailscale up?")
	}
	host, _, err := net.SplitHostPort(cfg)
	if err != nil {
		return "", fmt.Errorf("serve.listen %q: %w", cfg, err)
	}
	if host == "localhost" {
		return cfg, nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !(ip.IsLoopback() || tailscaleNet.Contains(ip)) {
		return "", fmt.Errorf("serve.listen %q: %q is not a Tailscale address (100.64.0.0/10) or localhost; bx serve has no login and refuses other interfaces", cfg, host)
	}
	return cfg, nil
}
