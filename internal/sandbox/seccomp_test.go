package sandbox

import "testing"

// TestUnixSocketBlockFilterStructure verifies the BPF program's STRUCTURE — it
// cannot verify runtime behavior off-Linux (that requires Linux CI). The most
// valuable check here is that no jump offset lands outside the program, the
// classic way a hand-written BPF filter goes silently wrong.
func TestUnixSocketBlockFilterStructure(t *testing.T) {
	prog := unixSocketBlockFilter()
	assertValidSockFilterProgram(t, prog)

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

func TestNetworkDenySeccompFilterStructure(t *testing.T) {
	prog := networkDenySeccompFilter()
	assertValidSockFilterProgram(t, prog)

	for _, k := range []uint32{
		auditArchX86_64,
		auditArchAARCH64,
		nrSocketX86_64,
		nrSocketAARCH64,
		nrSocketpairX86_64,
		nrSocketpairAARCH64,
		afUnix,
	} {
		if !hasInstruction(prog, bpfJEQK, k) {
			t.Fatalf("network filter missing JEQ check for 0x%X", k)
		}
	}
	for _, denied := range []uint32{42, 203, 288, 242, 307, 269, 425, 426, 427} {
		if !hasInstruction(prog, bpfJEQK, denied) {
			t.Fatalf("network filter missing denied syscall %d", denied)
		}
	}
	if !hasInstruction(prog, bpfRETK, seccompRetAllow) {
		t.Fatal("network filter has no allow return")
	}
	if !hasInstruction(prog, bpfRETK, seccompRetErrno|errnoEPERM) {
		t.Fatal("network filter has no EPERM block return")
	}
}

func assertValidSockFilterProgram(t *testing.T, prog []sockFilter) {
	t.Helper()
	if len(prog) == 0 {
		t.Fatal("empty filter program")
	}
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
}

func hasInstruction(prog []sockFilter, code uint16, k uint32) bool {
	for _, ins := range prog {
		if ins.Code == code && ins.K == k {
			return true
		}
	}
	return false
}
