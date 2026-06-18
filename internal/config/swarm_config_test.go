package config

import "testing"

func TestResolveSwarmMaxTeamSize(t *testing.T) {
	provider := `"activeProvider":"p","providers":[{"name":"p","provider":"openai","api_key":"sk","model_id":"m"}]`
	cases := []struct {
		name      string
		json      string
		wantSize  int
		wantError bool
	}{
		{"parsed from config", `{` + provider + `,"swarm":{"maxTeamSize":3}}`, 3, false},
		{"unset stays 0 so the swarm applies its own default", `{` + provider + `}`, 0, false},
		{"negative is rejected", `{` + provider + `,"swarm":{"maxTeamSize":-1}}`, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeConfig(t, tc.json)
			resolved, err := Resolve(ResolveOptions{UserConfigPath: path, Env: map[string]string{}})
			if tc.wantError {
				if err == nil {
					t.Fatal("expected an error for an invalid maxTeamSize")
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if resolved.Swarm.MaxTeamSize != tc.wantSize {
				t.Fatalf("MaxTeamSize = %d, want %d", resolved.Swarm.MaxTeamSize, tc.wantSize)
			}
		})
	}
}
