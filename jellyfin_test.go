package main

import "testing"

// okay okay maybe tests are useful after all

func TestSanitiseURL(t *testing.T) {
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
