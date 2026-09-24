package main

import (
	"slices"
	"testing"
)

func TestMissingRequiredValues(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		expected []string
	}{
		{
			name: "missing jellyfin url",
			config: Config{
				JellyfinKey:  "bar",
				JellyfinURL:  "",
				JellyfinUser: "foo",
			},
			expected: []string{"JELLYFIN_URL"},
		},
		{
			name: "missing jellyfin url and key",
			config: Config{
				JellyfinKey:  "",
				JellyfinURL:  "",
				JellyfinUser: "foo",
			},
			expected: []string{"JELLYFIN_KEY", "JELLYFIN_URL"},
		},
		{
			name: "missing jellyfin key and user",
			config: Config{
				JellyfinKey:  "",
				JellyfinURL:  "foo",
				JellyfinUser: "",
			},
			expected: []string{"JELLYFIN_KEY", "JELLYFIN_USER"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			missing, _ := tc.config.Validate()

			if !slices.Equal(missing, tc.expected) {
				t.Errorf("expected: %v, got: %v", tc.expected, missing)
			}
		})
	}
}
