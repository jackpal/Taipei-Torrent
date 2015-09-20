package main

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

type bindIPTestData []struct {
	in  string
	out string
}

var bindIPTests = bindIPTestData{
	{"", ""},
	{"junk", "error"},
	{"junk[999]", "error"},
	{"192.168.1.10", "192.168.1.10"},
	{"2001:db8:85a3::8a2e:370:7334", "2001:db8:85a3::8a2e:370:7334"},
}

func TestResolveBindIP(t *testing.T) {
	for _, tt := range bindIPTests {
		resolveBindIPAddrCase(t, tt.in, tt.out)
	}
}

func resolveBindIPAddrCase(t *testing.T, in string, out string) {
	bindIPAddr, err := resolveBindIPAddr(in)
	if err != nil {
		if out != "error" {
			t.Errorf("resolveBindIPAddr(%q) => error %v, want %q", in, err, out)
		}
	} else {
		bindIP := bindIPAddr.String()
		if bindIP != out {
			t.Errorf("resolveBindIPAddr(%q) => %v, want %q", in, bindIP, out)
		}
	}
}

func TestDynamicParseBindIP(t *testing.T) {
	netIFs, err := net.Interfaces()
	if err != nil {
		t.Errorf("net.Interfaces() => %v", err)
	}
	if len(netIFs) == 0 {
		t.Errorf("net.Interfaces() => no interfaces")
	}
	netIF := netIFs[0]
	addrs, err := netIFs[0].Addrs()
	if err != nil {
		t.Errorf("%v.Addrs() => %v", netIF.Name, err)
	}
	if len(addrs) == 0 {
		t.Errorf("%v.Addrs() => no addresses", netIF.Name)
	}
	resolveBindIPAddrCase(t, netIF.Name, stripMask(addrs[0].String()))
	resolveBindIPAddrCase(t, fmt.Sprintf("%v[%d]", netIF.Name, -1), "error")
	resolveBindIPAddrCase(t, fmt.Sprintf("%v[%d]", netIF.Name, len(addrs)), "error")
	for i, addr := range addrs {
		resolveBindIPAddrCase(t, fmt.Sprintf("%v[%d]", netIF.Name, i), stripMask(addr.String()))
	}
}

func stripMask(ipnet string) string {
	return strings.SplitN(ipnet, "/", 2)[0]
}
