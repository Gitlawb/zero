package sandbox

import "testing"

// TestUnixSocketBlockFilterStructure verifies the BPF program's STRUCTURE — it
// cannot verify runtime behavior off-Linux (that requires Linux CI). The most
// valuable check here is that no jump offset lands outside the program, the
// classic way a hand-written BPF filter goes silently wrong.
func TestUnixSocketBlockFilterStructure(t *testing.T) {
	prog := unixSocketBlockFilter()
	if len(prog) == 0 {
		t.Fatal("empty filter program")
	}

	// Every conditional jump must target an in-range instruction.
	for i, ins := range prog {
		if ins.Code != bpfJEQK {
			continue
		}
		if jt := i + 1 + int(ins.Jt); jt >= len(prog) {
			t.Fatalf("instruction %d Jt jumps to %d, out of range (len=%d)", i, jt, len(prog))
		}
		if jf := i + 1 + int(ins.Jf); jf >= len(prog) {
			t.Fatalf("instruction %d Jf jumps to %d, out of range (len=%d)", i, jf, len(prog))
		}
	}

	// The filter must check both supported arches and their socket() syscall
	// numbers, and the AF_UNIX domain.
	for _, k := range []uint32{auditArchX86_64, auditArchAARCH64, nrSocketX86_64, nrSocketAARCH64, afUnix} {
		if !hasInstruction(prog, bpfJEQK, k) {
			t.Fatalf("filter missing JEQ check for 0x%X", k)
		}
	}
	// It must load the domain argument (args[0]) and the syscall number/arch.
	for _, k := range []uint32{seccompOffsetArch, seccompOffsetNr, seccompOffsetArg0} {
		if !hasInstruction(prog, bpfLDWABS, k) {
			t.Fatalf("filter never loads seccomp_data offset %d", k)
		}
	}
	// It must both allow (default) and block with EPERM.
	if !hasInstruction(prog, bpfRETK, seccompRetAllow) {
		t.Fatal("filter has no allow return")
	}
	if !hasInstruction(prog, bpfRETK, seccompRetErrno|errnoEPERM) {
		t.Fatal("filter has no EPERM block return")
	}
}

func hasInstruction(prog []sockFilter, code uint16, k uint32) bool {
	for _, ins := range prog {
		if ins.Code == code && ins.K == k {
			return true
		}
	}
	return false
}
