package jellyfin

import (
	"errors"
	"net"
	"testing"
)

// okay okay maybe tests are useful after all

// fakeLookup swaps lookupIP for the test, hosts in the map resolve to their
// ips, anything else fails like a nxdomain would
func fakeLookup(t *testing.T, hosts map[string][]string) {
	t.Helper()

	orig := lookupIP
	t.Cleanup(func() { lookupIP = orig })

	lookupIP = func(host string) ([]net.IP, error) {
		addrs, ok := hosts[host]
		if !ok {
			return nil, errors.New("no such host")
		}

		var ips []net.IP
		for _, a := range addrs {
			ips = append(ips, net.ParseIP(a))
		}
		return ips, nil
	}
}

// noLookup fails the test if lookupIP gets called at all
func noLookup(t *testing.T) {
	t.Helper()

	orig := lookupIP
	t.Cleanup(func() { lookupIP = orig })

	lookupIP = func(host string) ([]net.IP, error) {
		t.Errorf("unexpected dns lookup for %q", host)
		return nil, errors.New("nope")
	}
}

func TestIsLocalInstanceNoLookup(t *testing.T) {
	// suffixes and ip literals should be decided without ever touching dns
	noLookup(t)

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{"localhost", "http://localhost:8096", true},
		{".local", "http://nas.local:8096", true},
		{".lan", "http://jellyfin.lan", true},
		{".ts.net", "https://salmon.tail1234.ts.net", true},
		{".home.arpa", "http://jellyfin.home.arpa", true},
		{"suffix uppercase", "http://JellyFin.LAN:8096", true},
		{"rfc1918 192", "http://192.168.1.10:8096", true},
		{"rfc1918 10", "http://10.0.0.5", true},
		{"rfc1918 172", "http://172.16.4.2", true},
		{"loopback", "http://127.0.0.1:8096", true},
		{"loopback v6", "http://[::1]:8096", true},
		{"link local", "http://169.254.10.1", true},
		{"link local v6", "http://[fe80::1]:8096", true},
		{"ula v6", "http://[fd12:3456::1]:8096", true},
		{"cgnat start", "http://100.64.0.1", true},
		{"cgnat end", "http://100.127.255.254", true},
		{"just past cgnat", "http://100.128.0.1", false},
		{"public v4", "https://203.0.113.5", false},
		{"public v6", "https://[2001:db8::1]", false},
		// url.Parse failing counts as local, which ends up as bridge art
		{"unparseable", "http://[::1", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsLocalInstance(tc.url)
			if got != tc.expected {
				t.Errorf("IsLocalInstance(%q): expected %v, got %v", tc.url, tc.expected, got)
			}
		})
	}
}

func TestIsLocalInstanceLookup(t *testing.T) {
	fakeLookup(t, map[string][]string{
		"nas":                {"192.168.1.20"},
		"jelly.example.com":  {"203.0.113.5"},
		"tailnet.salmon.dev": {"100.100.1.1"},
		// public domain rewritten to the local reverse proxy (adguard etc)
		"jelly.trout.cloud": {"192.168.1.2"},
		// any local ip wins even if theres a public one too
		"mixed.example.com": {"2001:db8::1", "10.0.0.2"},
		"empty.example.com": {},
	})

	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{"bare lan hostname", "http://nas:8096", true},
		{"public domain", "https://jelly.example.com", false},
		{"domain on tailscale ip", "https://tailnet.salmon.dev", true},
		{"split horizon guesses local", "https://jelly.trout.cloud", true},
		{"mixed public and local", "https://mixed.example.com", true},
		{"resolves to nothing", "https://empty.example.com", false},
		{"lookup fails", "https://nope.example.com", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsLocalInstance(tc.url)
			if got != tc.expected {
				t.Errorf("IsLocalInstance(%q): expected %v, got %v", tc.url, tc.expected, got)
			}
		})
	}
}

func TestSanitiseURL(t *testing.T) {
	// keep this off the network, domains without a protocol would do a real lookup
	fakeLookup(t, map[string][]string{"nas": {"192.168.1.20"}})

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"clean url", "https://jelly.example.com", "https://jelly.example.com"},
		{"trailing slash", "https://jelly.example.com/", "https://jelly.example.com"},
		{"web ui reminents", "https://jelly.instance/web/#/home", "https://jelly.instance"},
		{"localhost + no protocl", "localhost:8096", "http://localhost:8096"},
		{"local ip + no protocol", "192.168.1.69:8096/web/", "http://192.168.1.69:8096"},
		{"domain + no protocl", "jellyfin.example.com", "https://jellyfin.example.com"},
		{"spaces + trailing slash", "   https://jelly.example.com/web/   ", "https://jelly.example.com"},
		{"ip with port", "192.168.1.69:8096", "http://192.168.1.69:8096"},
		{"lan hostname + no protocol", "nas:8096", "http://nas:8096"},
		{"tailscale ip + no protocol", "100.101.102.103:8096", "http://100.101.102.103:8096"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitiseURL(tc.input)
			if got != tc.expected {
				t.Errorf("\ninput:    %s\nexpected: %s\ngot:      %s", tc.input, tc.expected, got)
			}
		})
	}
}
