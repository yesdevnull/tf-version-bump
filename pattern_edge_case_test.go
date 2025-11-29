package main

import "testing"

func TestPatternMatchingEdgeCasesOverlap(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		// Middle part potentially overlapping with suffix
		{
			name:     "two wildcards with repeated char - too short",
			input:    "ac",
			pattern:  "a*c*c",
			expected: false,
		},
		{
			name:     "two wildcards with repeated char - minimum",
			input:    "acc",
			pattern:  "a*c*c",
			expected: true,
		},
		{
			name:     "two wildcards with repeated char - with content",
			input:    "acbc",
			pattern:  "a*c*c",
			expected: true,
		},
		{
			name:     "three parts minimum - just enough",
			input:    "abc",
			pattern:  "a*b*c",
			expected: true,
		},
		{
			name:     "three parts - too short",
			input:    "ab",
			pattern:  "a*b*c",
			expected: false,
		},
		{
			name:     "three parts - with middle content",
			input:    "axyzbxyc",
			pattern:  "a*b*c",
			expected: true,
		},
		{
			name:     "suffix appears multiple times",
			input:    "testtest",
			pattern:  "test*test",
			expected: true,
		},
		{
			name:     "middle part is same as suffix",
			input:    "axbxc",
			pattern:  "a*x*c",
			expected: true,
		},
		{
			name:     "all same character with wildcards",
			input:    "aaa",
			pattern:  "a*a*a",
			expected: true,
		},
		{
			name:     "all same character - too short",
			input:    "aa",
			pattern:  "a*a*a",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchPattern(tt.input, tt.pattern)
			if result != tt.expected {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.input, tt.pattern, result, tt.expected)
			}
		})
	}
}
