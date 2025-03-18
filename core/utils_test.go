package core

import "testing"

func TestMaskSecrets(t *testing.T) {
	tests := []struct {
		name     string
		text     string
		secrets  []string
		expected string
	}{
		{
			name:     "single secret",
			text:     "password=1234",
			secrets:  []string{"1234"},
			expected: "password=**** REDACTED ***",
		},
		{
			name:     "multiple secrets",
			text:     "password=1234, token=abcd",
			secrets:  []string{"1234", "abcd"},
			expected: "password=**** REDACTED ***, token=**** REDACTED ***",
		},
		{
			name:     "secret not found",
			text:     "nothing here",
			secrets:  []string{"secret"},
			expected: "nothing here",
		},
		{
			name:     "empty secrets slice",
			text:     "password=1234",
			secrets:  []string{},
			expected: "password=1234",
		},
		{
			name:     "multiple occurrences",
			text:     "key=abcd, another key=abcd",
			secrets:  []string{"abcd"},
			expected: "key=**** REDACTED ***, another key=**** REDACTED ***",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskSecrets(tt.text, tt.secrets)
			if result != tt.expected {
				t.Errorf("MaskSecrets() = %q; want %q", result, tt.expected)
			}
		})
	}
}

func TestMaskSecretsInSlice(t *testing.T) {
	tests := []struct {
		name     string
		texts    []string
		secrets  []string
		expected []string
	}{
		{
			name:     "single secret",
			texts:    []string{"password=1234"},
			secrets:  []string{"1234"},
			expected: []string{"password=**** REDACTED ***"},
		},
		{
			name:     "multiple secrets",
			texts:    []string{"password=1234, token=abcd"},
			secrets:  []string{"1234", "abcd"},
			expected: []string{"password=**** REDACTED ***, token=**** REDACTED ***"},
		},
		{
			name:     "secret not found",
			texts:    []string{"nothing here"},
			secrets:  []string{"secret"},
			expected: []string{"nothing here"},
		},
		{
			name:     "empty secrets slice",
			texts:    []string{"password=1234"},
			secrets:  []string{},
			expected: []string{"password=1234"},
		},
		{
			name:     "multiple occurrences",
			texts:    []string{"key=abcd, another key=abcd"},
			secrets:  []string{"abcd"},
			expected: []string{"key=**** REDACTED ***, another key=**** REDACTED ***"},
		},
		{
			name:     "multiple lines",
			texts:    []string{"password=1234", "token=abcd"},
			secrets:  []string{"1234", "abcd"},
			expected: []string{"password=**** REDACTED ***", "token=**** REDACTED ***"},
		},
		{
			name:     "empty input slice",
			texts:    []string{},
			secrets:  []string{"1234"},
			expected: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := MaskSecretsInSlice(tt.texts, tt.secrets)
			if len(result) != len(tt.expected) {
				t.Errorf("MaskSecretsInSlice() length = %d; want %d", len(result), len(tt.expected))
				return
			}
			for i := range result {
				if result[i] != tt.expected[i] {
					t.Errorf("MaskSecretsInSlice()[%d] = %q; want %q", i, result[i], tt.expected[i])
				}
			}
		})
	}
}
