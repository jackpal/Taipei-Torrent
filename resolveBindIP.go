package main

import (
	"fmt"
	"log"
	"net"
	"regexp"
	"strconv"
)

// resolveBindIPAddr resolves either an ip address or an interface name or an interface name followed by [N].
// Empty string resolves as nil.
func resolveBindIPAddr(bindIP string) (resolved *net.IPAddr, err error) {
	// Parse addresses of form en0[2]
	matches := regexp.MustCompile(`(.+)\[(\d+)\]`).FindStringSubmatch(bindIP)
	if matches != nil {
		var i int
		i, err = strconv.Atoi(matches[2])
		if err != nil {
			return
		}
		return resolveInterfaceIndex(matches[1], i)
	}
	// Parse addresses of form en0
	resolved, err = resolveInterfaceIndex(bindIP, 0)
	if err == nil {
		return
	}
	// Parse addresses of form 192.168.0.10 or IPV6 equivalent.
	return net.ResolveIPAddr("ip", bindIP)
}

func resolveInterfaceIndex(ifName string, i int) (resolved *net.IPAddr, err error) {
	netInterface, err := net.InterfaceByName(ifName)
	if err != nil {
		return
	}
	addrs, err := netInterface.Addrs()
	if err != nil {
		return
	}
	if i < 0 || i >= len(addrs) {
		err = fmt.Errorf("Address index %d out of range 0..%d for interface %v", i, len(addrs), ifName)
		return
	}
	addr := addrs[i]
	if addrIPNet, ok := addr.(*net.IPNet); ok {
		// addr is a net.IPNet, convert it into a net.IPAddr
		resolved = &net.IPAddr{IP: addrIPNet.IP}
	} else {
		err = fmt.Errorf("%s[%d] is not an IP address", ifName, i)
		log.Println(err)
	}
	return
}
