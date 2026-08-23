package config

import "testing"

// The ownership matrix. Every row is a shape the resolver can validly produce,
// and the answer decides whether a user-config or credential-store mutator may
// run at all — so a wrong answer here is a write to a row the user never chose.
func TestResolveProviderRowOwnership(t *testing.T) {
	for _, testCase := range []struct {
		name string
		// persisted are the user config's rows.
		persisted []string
		// resolved is every row spelling the session is displaying.
		resolved []string
		// row is the one being acted on.
		row           string
		wantBacked    bool
		wantPersisted string
	}{
		{
			name:          "exact user row",
			persisted:     []string{"work"},
			resolved:      []string{"work"},
			row:           "work",
			wantBacked:    true,
			wantPersisted: "work",
		},
		{
			// The defect. Cross-layer merging is exact, so both rows exist; the
			// project row must not write through the user row it merely shares a
			// credential identity with.
			name:       "project row beside its user case sibling",
			persisted:  []string{"work"},
			resolved:   []string{"work", "WORK"},
			row:        "WORK",
			wantBacked: false,
		},
		{
			// The same pair from the other side: the user row is still its own.
			name:          "user row beside its project case sibling",
			persisted:     []string{"work"},
			resolved:      []string{"work", "WORK"},
			row:           "work",
			wantBacked:    true,
			wantPersisted: "work",
		},
		{
			// A sole case variant is the case the normalized fallback exists for:
			// a session launched with ZERO_PROVIDER=openai against a saved
			// "OpenAI" row, with no sibling to confuse it.
			name:          "sole case variant",
			persisted:     []string{"OpenAI"},
			resolved:      []string{"openai"},
			row:           "openai",
			wantBacked:    true,
			wantPersisted: "OpenAI",
		},
		{
			name:       "env-only row with no persisted counterpart",
			persisted:  []string{"work"},
			resolved:   []string{"work", "groq"},
			row:        "groq",
			wantBacked: false,
		},
		{
			// Two persisted rows carry the identity, so no single row can be the
			// target. First-match would have picked one.
			name:       "ambiguous persisted rows",
			persisted:  []string{"work", "WORK"},
			resolved:   []string{"work", "WORK", "Work"},
			row:        "Work",
			wantBacked: false,
		},
		{
			// "s" and long-s "ſ" are DIFFERENT credential identities — the store
			// keeps separate entries — so neither may claim the other's row.
			name:          "long-s is a distinct identity",
			persisted:     []string{"s"},
			resolved:      []string{"s", "ſ"},
			row:           "ſ",
			wantBacked:    false,
			wantPersisted: "",
		},
		{
			name:          "long-s row of its own",
			persisted:     []string{"s", "ſ"},
			resolved:      []string{"s", "ſ"},
			row:           "ſ",
			wantBacked:    true,
			wantPersisted: "ſ",
		},
		{
			name:       "unnamed row",
			persisted:  []string{"work"},
			resolved:   []string{"work"},
			row:        "",
			wantBacked: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			persisted := make([]ProviderProfile, 0, len(testCase.persisted))
			for _, name := range testCase.persisted {
				persisted = append(persisted, ProviderProfile{Name: name})
			}
			owner := ResolveProviderRowOwnership(persisted, testCase.resolved, testCase.row)
			if owner.UserBacked != testCase.wantBacked {
				t.Fatalf("UserBacked = %t, want %t (reason %q)", owner.UserBacked, testCase.wantBacked, owner.Reason)
			}
			if owner.PersistedName != testCase.wantPersisted {
				t.Fatalf("PersistedName = %q, want %q", owner.PersistedName, testCase.wantPersisted)
			}
			if !owner.UserBacked && owner.Reason == "" {
				t.Fatal("a refusal must carry a reason the UI can show")
			}
			if owner.UserBacked && owner.Reason != "" {
				t.Fatalf("a backed row must carry no refusal reason, got %q", owner.Reason)
			}
		})
	}
}

// The shared lookup rule. Ambiguous is a distinct outcome from NotFound because
// the callers this replaces returned the first match instead.
func TestLookupProviderName(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		candidates []string
		want       string
		wantName   string
		wantResult ProviderNameLookup
	}{
		{name: "exact wins over a sibling", candidates: []string{"WORK", "work"}, want: "work", wantName: "work", wantResult: ProviderNameExact},
		{name: "exact wins in either order", candidates: []string{"work", "WORK"}, want: "WORK", wantName: "WORK", wantResult: ProviderNameExact},
		{name: "sole identity match", candidates: []string{"OpenAI"}, want: "openai", wantName: "OpenAI", wantResult: ProviderNameNormalized},
		{name: "several identity matches", candidates: []string{"work", "WORK"}, want: "Work", wantResult: ProviderNameAmbiguous},
		{name: "no match", candidates: []string{"work"}, want: "groq", wantResult: ProviderNameNotFound},
		{name: "long-s is not s", candidates: []string{"s"}, want: "ſ", wantResult: ProviderNameNotFound},
		{name: "blank", candidates: []string{"work"}, want: "  ", wantResult: ProviderNameNotFound},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			name, result := LookupProviderName(testCase.candidates, testCase.want)
			if result != testCase.wantResult || name != testCase.wantName {
				t.Fatalf("LookupProviderName(%q, %q) = %q/%v, want %q/%v",
					testCase.candidates, testCase.want, name, result, testCase.wantName, testCase.wantResult)
			}
			if result.Resolved() != (name != "") {
				t.Fatalf("Resolved() = %t for name %q", result.Resolved(), name)
			}
		})
	}
}
