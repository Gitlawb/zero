package cli

import "testing"

func TestParseExecArgsCollectsAddDirs(t *testing.T) {
	options, _, err := parseExecArgs([]string{
		"--prompt", "hi",
		"--add-dir", "/one",
		"--add-dir=/two",
	})
	if err != nil {
		t.Fatalf("parseExecArgs: %v", err)
	}
	if len(options.addDirs) != 2 || options.addDirs[0] != "/one" || options.addDirs[1] != "/two" {
		t.Fatalf("addDirs=%v want [/one /two]", options.addDirs)
	}
}

func TestParseExecArgsAddDirRequiresValue(t *testing.T) {
	if _, _, err := parseExecArgs([]string{"--add-dir"}); err == nil {
		t.Fatal("bare --add-dir must error")
	}
}

func TestSplitLeadingAddDirFlags(t *testing.T) {
	addDirs, rest, err := splitLeadingAddDirFlags([]string{"--add-dir", "/one", "--add-dir=/two", "exec", "--prompt", "x"})
	if err != nil {
		t.Fatalf("splitLeadingAddDirFlags: %v", err)
	}
	if len(addDirs) != 2 || addDirs[0] != "/one" || addDirs[1] != "/two" {
		t.Fatalf("addDirs=%v want [/one /two]", addDirs)
	}
	if len(rest) != 3 || rest[0] != "exec" {
		t.Fatalf("rest=%v want [exec --prompt x]", rest)
	}
	if _, _, err := splitLeadingAddDirFlags([]string{"--add-dir"}); err == nil {
		t.Fatal("trailing bare --add-dir must error")
	}
	// Values that look like flags must be rejected in BOTH spellings.
	if _, _, err := splitLeadingAddDirFlags([]string{"--add-dir", "--foo"}); err == nil {
		t.Fatal("--add-dir with flag-like value must error")
	}
	if _, _, err := splitLeadingAddDirFlags([]string{"--add-dir=--foo"}); err == nil {
		t.Fatal("--add-dir=--foo must error")
	}
	if _, _, err := splitLeadingAddDirFlags([]string{"--add-dir=-foo"}); err == nil {
		t.Fatal("--add-dir=-foo must error")
	}
}
