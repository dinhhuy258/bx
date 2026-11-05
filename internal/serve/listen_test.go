package serve

import (
	"net"
	"strings"
	"testing"
)

func fakeAddrs(cidrs ...string) func() ([]net.Addr, error) {
	return func() ([]net.Addr, error) {
		var out []net.Addr
		for _, c := range cidrs {
			_, n, _ := net.ParseCIDR(c)
			out = append(out, n)
		}
		return out, nil
	}
}

func TestResolveListen(t *testing.T) {
	addrs := fakeAddrs("127.0.0.1/8", "192.168.1.20/24", "100.101.102.103/32")
	cases := []struct{ in, want, err string }{
		{"auto", "100.101.102.103:7357", ""},
		{"100.64.0.9:8000", "100.64.0.9:8000", ""},
		{"127.0.0.1:0", "127.0.0.1:0", ""},
		{"localhost:7357", "localhost:7357", ""},
		{"0.0.0.0:7357", "", "not a Tailscale address"},
		{":7357", "", "not a Tailscale address"},
		{"192.168.1.20:7357", "", "not a Tailscale address"},
		{"100.128.0.1:7357", "", "not a Tailscale address"}, // just past 100.64.0.0/10
	}
	for _, c := range cases {
		got, err := resolveListen(c.in, addrs)
		if c.err == "" && (err != nil || got != c.want) {
			t.Errorf("%q: got %q, %v", c.in, got, err)
		}
		if c.err != "" && (err == nil || !strings.Contains(err.Error(), c.err)) {
			t.Errorf("%q: want error %q, got %v", c.in, c.err, err)
		}
	}
	if _, err := resolveListen("auto", fakeAddrs("127.0.0.1/8", "10.0.0.5/24")); err == nil || !strings.Contains(err.Error(), "no Tailscale address") {
		t.Errorf("auto without tailscale: %v", err)
	}
}

func TestAppHost(t *testing.T) {
	status := []byte(`{"Self":{"DNSName":"dev.tail1234.ts.net.","TailscaleIPs":["100.70.1.2"]}}`)
	for _, c := range []struct {
		override string
		json     []byte
		ip, want string
	}{
		{"", status, "100.70.1.2", "dev"},
		{"box.lan", status, "100.70.1.2", "box.lan"},
		{"", []byte(`{"Self":{"DNSName":""}}`), "100.70.1.2", "100.70.1.2"},
		{"", nil, "100.70.1.2", "100.70.1.2"},
	} {
		if got := AppHost(c.override, c.json, c.ip); got != c.want {
			t.Errorf("AppHost(%q, %s) = %q, want %q", c.override, c.json, got, c.want)
		}
	}
}
