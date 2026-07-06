package provideroauth

import "testing"

func TestCopilotEndpointsAPIFormat(t *testing.T) {
	cases := []struct {
		name      string
		endpoints []string
		want      string
	}{
		{"no field defaults to chat", nil, copilotAPIFormatChat},
		{"chat only", []string{"/chat/completions"}, copilotAPIFormatChat},
		{"chat and responses prefers chat", []string{"/responses", "/chat/completions"}, copilotAPIFormatChat},
		{"responses only", []string{"/responses", "ws:/responses"}, copilotAPIFormatResponses},
		{"embeddings only falls back to chat", []string{"/embeddings"}, copilotAPIFormatChat},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := copilotEndpointsAPIFormat(tc.endpoints); got != tc.want {
				t.Fatalf("copilotEndpointsAPIFormat(%v) = %q, want %q", tc.endpoints, got, tc.want)
			}
		})
	}
}

func TestCopilotModelAPIFormatBlankInputsDefaultToChat(t *testing.T) {
	if got := CopilotModelAPIFormat(nil, nil, "", "", "gpt-5.4-mini"); got != copilotAPIFormatChat {
		t.Fatalf("blank bearer = %q, want %q", got, copilotAPIFormatChat)
	}
	if got := CopilotModelAPIFormat(nil, nil, "token", "", ""); got != copilotAPIFormatChat {
		t.Fatalf("blank model = %q, want %q", got, copilotAPIFormatChat)
	}
}
