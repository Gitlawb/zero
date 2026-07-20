package aimlapi

import "testing"

func TestValidEmailRejectsIncompleteDomains(t *testing.T) {
	tests := []struct {
		value string
		valid bool
	}{
		{value: "user@example.com", valid: true},
		{value: "123@123.com", valid: true},
		{value: "123@123.", valid: false},
		{value: "123@123", valid: false},
		{value: "user@example.c", valid: false},
		{value: "user@example.123", valid: false},
		{value: "user@.com", valid: false},
		{value: "user@example..com", valid: false},
	}
	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			if got := ValidEmail(test.value); got != test.valid {
				t.Fatalf("ValidEmail(%q) = %v, want %v", test.value, got, test.valid)
			}
		})
	}
}
