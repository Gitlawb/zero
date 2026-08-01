package tools

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

type commandReducerCorpusCase struct {
	name    string
	command string
	output  string
	want    string
}

func commandReducerCorpus() []commandReducerCorpusCase {
	goLines := []string{"output:"}
	cargoTestLines := []string{"output:", "running 24 tests"}
	cargoCheckLines := []string{"output:"}
	pytestLines := []string{"output:"}
	vitestLines := []string{"output:", " RUN  v3.2.4 /workspace"}
	jestLines := []string{"output:"}
	for index := 0; index < 24; index++ {
		goLines = append(goLines, fmt.Sprintf("ok  \texample.test/pkg%d\t0.01s", index))
		cargoTestLines = append(cargoTestLines, fmt.Sprintf("test module::case_%02d ... ok", index))
		cargoCheckLines = append(cargoCheckLines, fmt.Sprintf("    Checking dependency_%02d v1.0.%d", index, index))
		pytestLines = append(pytestLines, fmt.Sprintf("tests/test_module_%02d.py ........                         [%3d%%]", index, (index+1)*100/24))
		vitestLines = append(vitestLines, fmt.Sprintf(" ✓ src/module_%02d.test.ts (8 tests) 12ms", index))
		jestLines = append(jestLines, fmt.Sprintf("PASS src/module_%02d.test.ts", index))
	}
	goLines = append(goLines, "exit_code: 0")
	cargoTestLines = append(cargoTestLines,
		"test result: ok. 24 passed; 0 failed; 0 ignored; 0 measured; 0 filtered out; finished in 0.08s",
		"exit_code: 0",
	)
	cargoCheckLines = append(cargoCheckLines,
		"    Finished `dev` profile [unoptimized + debuginfo] target(s) in 2.41s",
		"exit_code: 0",
	)
	pytestLines = append(pytestLines,
		"============================= 192 passed in 3.21s =============================",
		"exit_code: 0",
	)
	vitestLines = append(vitestLines,
		" Test Files  24 passed (24)",
		"      Tests  192 passed (192)",
		"   Duration  3.21s",
		"exit_code: 0",
	)
	jestLines = append(jestLines,
		"Test Suites: 24 passed, 24 total",
		"Tests:       192 passed, 192 total",
		"Time:        3.21 s",
		"exit_code: 0",
	)
	return []commandReducerCorpusCase{
		{name: "go_test", command: "go test ./...", output: strings.Join(goLines, "\n"), want: "exit_code: 0"},
		{name: "cargo_test", command: "cargo test --workspace", output: strings.Join(cargoTestLines, "\n"), want: "24 passed"},
		{name: "cargo_check", command: "cargo check --workspace", output: strings.Join(cargoCheckLines, "\n"), want: "Finished `dev` profile"},
		{name: "pytest", command: "pytest -q", output: strings.Join(pytestLines, "\n"), want: "192 passed"},
		{name: "npm_vitest", command: "npm test", output: strings.Join(vitestLines, "\n"), want: "192 passed"},
		{name: "pnpm_vitest", command: "pnpm test", output: strings.Join(vitestLines, "\n"), want: "192 passed"},
		{name: "yarn_jest", command: "yarn test", output: strings.Join(jestLines, "\n"), want: "192 passed"},
	}
}

func TestCommandOutputReductionCorpusMetrics(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	totalRawTokens := 0
	totalModelTokens := 0
	for _, testCase := range commandReducerCorpus() {
		t.Run(testCase.name, func(t *testing.T) {
			result := reduceCommandOutput(ExecCommandToolName, map[string]any{"cmd": testCase.command}, Result{
				Status: StatusOK,
				Output: testCase.output,
			})
			if !strings.Contains(result.Output, testCase.want) {
				t.Fatalf("decisive summary %q was lost: %q", testCase.want, result.Output)
			}
			if spillPath := result.Meta["spill_path"]; spillPath != "" {
				spilled, err := os.ReadFile(spillPath)
				if err != nil {
					t.Fatalf("read raw artifact: %v", err)
				}
				if string(spilled) != testCase.output {
					t.Fatal("raw artifact differs from original output")
				}
			}
			rawTokens := estimateOutputTokens(testCase.output)
			modelTokens := estimateOutputTokens(result.Output)
			totalRawTokens += rawTokens
			totalModelTokens += modelTokens
			reduction := 100 * (rawTokens - modelTokens) / rawTokens
			t.Logf("raw_bytes=%d model_bytes=%d raw_tokens=%d model_tokens=%d reduction_pct=%d reduced=%t",
				len(testCase.output), len(result.Output), rawTokens, modelTokens, reduction, result.Meta["command_output_reduced"] == "true")
		})
	}
	if totalRawTokens == 0 {
		t.Fatal("empty reducer corpus")
	}
	if totalModelTokens >= totalRawTokens {
		t.Fatalf("aggregate reducer output did not save tokens: raw=%d model=%d", totalRawTokens, totalModelTokens)
	}
	t.Logf("aggregate raw_tokens=%d model_tokens=%d reduction_pct=%d",
		totalRawTokens, totalModelTokens, 100*(totalRawTokens-totalModelTokens)/totalRawTokens)
}
