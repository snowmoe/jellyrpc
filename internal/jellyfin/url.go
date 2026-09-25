package jellyfin

import (
	"net"
	"net/url"
	"strings"
)

// determines if the jellyfin instance url provided is local
// checks if localhost or a .local domain
// checks if ip (if parseable) is rfc1918 or loopback
// still kept 127 and ::1 in the host check anyway but can possibly be removed
func IsLocalInstance(hostURL string) bool {
	u, err := url.Parse(hostURL)
	if err != nil {
		return true
	}

	host := u.Hostname()

	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".local") {
		return true
	}

	ip := net.ParseIP(host)
	if ip != nil {
		return ip.IsPrivate() || ip.IsLoopback()
	}

	return false
}

// cleans url's AND guesses protocol if it's missing (which isn't an issue if U READ DA README UGH)
func SanitiseURL(rawURL string) string {
	u := strings.TrimSpace(rawURL)
	if u == "" {
		return ""
	}

	// should catch if a user didn't READ THE README(!!!!) and missed the protocol
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		// then so the local instance func doesn't err from url.Parse with a missing protocol
		// just append http:// temporarily so that can parse n do it's thang
		tempURL := "http://" + u

		// then if it's local we just guess that it'll be http://
		if IsLocalInstance(tempURL) {
			u = "http://" + u
		} else {
			// if not local (ie almost 100% likely a domain being used) then we guess it'll be https://
			u = "https://" + u
		}
	}

	// if we fail to parse then just give the raw url back and pray
	parsed, err := url.Parse(u)
	if err != nil {
		return u
	}

	hostURL := parsed.Scheme + "://" + parsed.Host

	// prolly not needed but fuckit we schizo
	hostURL = strings.TrimSuffix(hostURL, "/")

	return hostURL
}
