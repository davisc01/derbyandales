package app

import (
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
)

// LANAddrs returns the machine's non-loopback IPv4 addresses, which is what
// tablets and TVs will use to reach the server.
//
// IPv6 is deliberately omitted: the startup card exists so someone can type or
// scan a URL at a brewery, and an IPv6 literal is neither.
func LANAddrs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipnet.IP.To4()
			if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, ip4)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// LocalHostname returns the machine's mDNS name (e.g. "race-mac.local"), which
// is friendlier to type than an IP and survives a DHCP lease change.
func LocalHostname() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return ""
	}
	if !strings.Contains(h, ".") {
		h += ".local"
	}
	return h
}

// URLs describes how to reach this server, for the startup card and QR code.
type URLs struct {
	Local    string   // http://localhost:PORT  — secure context, camera works
	Secure   []string // https://IP:PORT        — secure context after trusting the cert
	Insecure []string // http://IP:PORT         — fine for displays and voting, no camera
}

// BuildURLs assembles the reachable addresses for the given ports.
func BuildURLs(httpPort, httpsPort int) URLs {
	u := URLs{Local: fmt.Sprintf("http://localhost:%d", httpPort)}

	hosts := make([]string, 0, 4)
	if h := LocalHostname(); h != "" {
		hosts = append(hosts, h)
	}
	for _, ip := range LANAddrs() {
		hosts = append(hosts, ip.String())
	}
	for _, h := range hosts {
		u.Secure = append(u.Secure, fmt.Sprintf("https://%s:%d", h, httpsPort))
		u.Insecure = append(u.Insecure, fmt.Sprintf("http://%s:%d", h, httpPort))
	}
	return u
}

// LAN returns just the numeric addresses a screen or tablet can reach, in
// preference to the .local name.
//
// Both work, and the .local name survives a DHCP lease change where an address
// does not — but listing both made every URL appear twice on screens that are
// read at arm's length while carrying an HDMI cable. The address is the one
// that always resolves, including from a device that has no mDNS.
func (u URLs) LAN() []string {
	var out []string
	for _, addr := range u.Insecure {
		host := addr
		if i := strings.Index(host, "//"); i >= 0 {
			host = host[i+2:]
		}
		if i := strings.LastIndex(host, ":"); i >= 0 {
			host = host[:i]
		}
		// A numeric address starts with a digit; a hostname does not.
		if host != "" && host[0] >= '0' && host[0] <= '9' {
			out = append(out, addr)
		}
	}
	// Fall back to whatever there is rather than showing nothing at all.
	if len(out) == 0 {
		return u.Insecure
	}
	return out
}
