package main

import "testing"

func TestPatternMatchingBoundaryConditions(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		// Consecutive wildcards
		{"consecutive wildcards **", "abc", "a**c", true},
		{"triple wildcards", "abc", "a***c", true},
		{"many wildcards", "abc", "a*****c", true},

		// Very long inputs
		{"long string match", string(make([]byte, 1000)), "*", true},

		// All wildcards
		{"just wildcard", "anything", "*", true},
		{"multiple wildcards only", "anything", "***", true},

		// Edge cases with empty parts
		{"wildcard at start", "test", "*test", true},
		{"wildcard at end", "test", "test*", true},
		{"wildcard both ends", "test", "*test*", true},

		// Patterns that don't match
		{"prefix too long", "ab", "abc*", false},
		{"suffix too long", "ab", "*abc", false},
		{"middle part missing", "ac", "a*b*c", false},

		// Special characters (not regex)
		{"dots are literal", "test.tf", "test.tf", true},
		{"dots with wildcard", "test.tf", "*.tf", true},
		{"brackets are literal", "test[0]", "test[0]", true},
		{"plus is literal", "test+prod", "test+prod", true},
		{"question mark is literal", "test?", "test?", true},

		// Empty strings
		{"empty input empty pattern", "", "", true},
		{"empty input wildcard", "", "*", true},
		{"empty input non-wildcard", "", "test", false},
		{"wildcard empty part", "test", "test*", true},

		// Case sensitivity
		{"case sensitive", "Test", "test", false},
		{"case match", "test", "test", true},

		// Multiple middle parts
		{"two middle parts", "a-b-c-d", "a-*-*-d", true},
		{"three middle parts", "a-1-2-3-z", "a-*-*-*-z", true},
		{"middle parts order matters", "a-x-y-z", "a-*-y-*", true},
	}

	failed := 0
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchPattern(tt.input, tt.pattern)
			if result != tt.expected {
				t.Errorf("matchPattern(%q, %q) = %v, want %v", tt.input, tt.pattern, result, tt.expected)
				failed++
			}
		})
	}

	if failed == 0 {
		t.Logf("All %d boundary condition tests passed!", len(tests))
	}
}
