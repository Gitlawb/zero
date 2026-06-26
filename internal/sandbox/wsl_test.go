package sandbox

import "testing"

func TestParseWSL(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		wantWSL  bool
		wantWSL2 bool
	}{
		{
			name:     "wsl2 microsoft kernel",
			input:    "Linux version 5.15.90.1-microsoft-standard-WSL2 (...)",
			wantWSL:  true,
			wantWSL2: true,
		},
		{
			name:     "wsl1 legacy marker",
			input:    "Linux version 4.4.0-19041-Microsoft (wsl1 build) ...",
			wantWSL:  true,
			wantWSL2: false,
		},
		{
			name:     "plain linux",
			input:    "Linux version 6.8.0-generic (gcc ...) #1 SMP",
			wantWSL:  false,
			wantWSL2: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseWSL(tc.input)
			if got.IsWSL != tc.wantWSL || got.IsWSL2 != tc.wantWSL2 {
				t.Fatalf("parseWSL(%q) = {IsWSL:%v IsWSL2:%v}, want {%v %v}", tc.input, got.IsWSL, got.IsWSL2, tc.wantWSL, tc.wantWSL2)
			}
			if got.IsWSL && got.Kernel == "" {
				t.Fatalf("parseWSL should record the kernel string")
			}
		})
	}
}

func wslBackendForTest() Backend {
	return Backend{Name: BackendWSL, Platform: "linux", Fallback: true}
}

func TestWSLPlanDegradesWithoutNativeSandbox(t *testing.T) {
	root := t.TempDir()
	policy := DefaultPolicy()
	engine := NewEngine(EngineOptions{WorkspaceRoot: root, Policy: policy, Backend: wslBackendForTest()})

	plan, err := engine.BuildCommandPlan(CommandSpec{Name: "/bin/sh", Args: []string{"-c", "pwd"}, Dir: root})
	if err != nil {
		t.Fatalf("WSL command plan: %v", err)
	}
	if plan.Wrapped || plan.EnforcementLevel != EnforcementDegraded || !plan.RequiresPlatformSandbox {
		t.Fatalf("WSL command plan = %#v, want degraded direct plan", plan)
	}
}
