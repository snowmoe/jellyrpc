package jellyfin

import (
	"net"
	"net/url"
	"strings"
)

// CGNAT range, used by tailscale, and its impossible for a jellyfin
// instance to have a CGNAT ip AND be public, so it'll flag as local either way
const cgnatCIDR = "100.64.0.0/10"

// so tests can replace this
var lookupIP = net.LookupIP

// IsLocalInstance tries to determine if url provided is local
func IsLocalInstance(hostURL string) bool {
	u, err := url.Parse(hostURL)
	if err != nil {
		return true
	}

	host := strings.ToLower(u.Hostname())

	if host == "localhost" || hasSuffixes(host, ".local", ".lan", ".ts.net", ".home.arpa") {
		return true
	}

	// ignore the error because if that didn't parse somethings fucked
	// and it's probably not my fault (i think)
	_, tsSubnet, _ := net.ParseCIDR(cgnatCIDR)

	ip := net.ParseIP(host)
	if ip != nil {
		return isLocalOrSubnetIP(ip, tsSubnet)
	}

	addrs, err := lookupIP(host)
	if err != nil {
		return false
	}

	for _, ip := range addrs {
		// catches if any of the ip's are private, if it has a private AND public
		// ip it will still get labeled as local
		if isLocalOrSubnetIP(ip, tsSubnet) {
			return true
		}
	}

	return false
}

// isLocalOrSubnetIP checks if an ip is a loopback, local (rfc1918), or a 169 (rip dhcp), or in the subnet provided
func isLocalOrSubnetIP(ip net.IP, sub *net.IPNet) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || sub.Contains(ip)
}

func hasSuffixes(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}

	return false
}

// SanitiseURL cleans a url AND guesses the protocol if it's missing
func SanitiseURL(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return ""
	}

	// should catch if a url was supplied without the protocol
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		// then so the local instance func doesn't err from url.Parse with a missing protocol
		// just append http:// temporarily so that can parse n do it's thang
		tempURL := "http://" + u

		// then if it's local we just guess that it'll be http://
		if IsLocalInstance(tempURL) {
			u = "http://" + u
		} else {
			// if not local we guess it'll be https://
			u = "https://" + u
		}
	}

	// if we fail to parse then just give the raw url back and pray
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}

	hostURL := parsed.Scheme + "://" + parsed.Host

	// append a / if missing so we can match properly on jellyfin web ui path
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}

	// cut anything after /web/ and leave us with whatever was before
	// will technically cut wrong if someone serves jellyfin UNDER a /web subpath
	// (their webui url would look like: /web/web/#/home), stupid, and very rare
	path, _, _ := strings.Cut(parsed.Path, "/web/")

	// append that new path back on, will be empty if jellyfin isn't under a subpath
	// otherwise it will have the subpath jellyfin is under (guessed)
	hostURL += path

	return strings.TrimRight(hostURL, "/")
}
