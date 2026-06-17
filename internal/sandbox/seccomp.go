package sandbox

// sockFilter mirrors the kernel's struct sock_filter (one classic-BPF
// instruction). It is defined here platform-neutrally so the Unix-socket-blocking
// program can be built and unit-tested on any OS; seccomp_linux.go converts it to
// unix.SockFilter to install it via prctl.
type sockFilter struct {
	Code uint16
	Jt   uint8
	Jf   uint8
	K    uint32
}

// Classic-BPF opcodes and the seccomp/audit constants used by the filter. These
// are stable kernel-ABI values, identical across architectures.
const (
	bpfLDWABS = 0x20 // BPF_LD | BPF_W | BPF_ABS
	bpfJEQK   = 0x15 // BPF_JMP | BPF_JEQ | BPF_K
	bpfRETK   = 0x06 // BPF_RET | BPF_K

	auditArchX86_64  = 0xC000003E
	auditArchAARCH64 = 0xC00000B7

	nrSocketX86_64      = 41
	nrSocketAARCH64     = 198
	nrSocketpairX86_64  = 53
	nrSocketpairAARCH64 = 199

	afUnix = 1 // AF_UNIX / AF_LOCAL

	seccompRetAllow = 0x7FFF0000 // SECCOMP_RET_ALLOW
	seccompRetErrno = 0x00050000 // SECCOMP_RET_ERRNO
	errnoEPERM      = 1          // OR'd into the low 16 bits of SECCOMP_RET_ERRNO

	// Byte offsets into struct seccomp_data.
	seccompOffsetNr   = 0
	seccompOffsetArch = 4
	seccompOffsetArg0 = 16
)

var networkDenySyscallsX86_64 = []uint32{
	42,  // connect
	43,  // accept
	44,  // sendto
	48,  // shutdown
	49,  // bind
	50,  // listen
	51,  // getsockname
	52,  // getpeername
	54,  // setsockopt
	55,  // getsockopt
	101, // ptrace
	288, // accept4
	299, // recvmmsg
	307, // sendmmsg
	310, // process_vm_readv
	311, // process_vm_writev
	425, // io_uring_setup
	426, // io_uring_enter
	427, // io_uring_register
}

var networkDenySyscallsAARCH64 = []uint32{
	117, // ptrace
	200, // bind
	201, // listen
	202, // accept
	203, // connect
	204, // getsockname
	205, // getpeername
	206, // sendto
	208, // setsockopt
	209, // getsockopt
	210, // shutdown
	242, // accept4
	243, // recvmmsg
	269, // sendmmsg
	270, // process_vm_readv
	271, // process_vm_writev
	425, // io_uring_setup
	426, // io_uring_enter
	427, // io_uring_register
}

// unixSocketBlockFilter builds a classic-BPF seccomp program that denies
// socket(2)/AF_UNIX with EPERM on x86-64 and arm64 and allows everything else.
// An unrecognized architecture is allowed (fail-open on arch is intentional: the
// filter blocks Unix sockets only where it knows the syscall ABI, rather than
// bricking an arch it does not understand). Jump targets are expressed as relative
// offsets from the instruction after the jump, per the BPF spec.
//
// WARNING: this program's runtime behavior cannot be verified off-Linux. The unit
// test asserts its structure; the actual blocking must be verified on Linux CI.
func unixSocketBlockFilter() []sockFilter {
	return []sockFilter{
		// 0: A = arch
		{Code: bpfLDWABS, K: seccompOffsetArch},
		// 1: if arch == x86_64 -> idx 4 (x86 nr load)
		{Code: bpfJEQK, K: auditArchX86_64, Jt: 2, Jf: 0},
		// 2: if arch == aarch64 -> idx 6 (arm nr load)
		{Code: bpfJEQK, K: auditArchAARCH64, Jt: 3, Jf: 0},
		// 3: unknown arch -> allow
		{Code: bpfRETK, K: seccompRetAllow},
		// 4: A = nr (x86 path)
		{Code: bpfLDWABS, K: seccompOffsetNr},
		// 5: if nr == socket -> idx 8 (domain check), else idx 10 (allow)
		{Code: bpfJEQK, K: nrSocketX86_64, Jt: 2, Jf: 4},
		// 6: A = nr (arm path)
		{Code: bpfLDWABS, K: seccompOffsetNr},
		// 7: if nr == socket -> idx 8 (domain check), else idx 10 (allow)
		{Code: bpfJEQK, K: nrSocketAARCH64, Jt: 0, Jf: 2},
		// 8: A = args[0] (domain)
		{Code: bpfLDWABS, K: seccompOffsetArg0},
		// 9: if domain == AF_UNIX -> idx 11 (block), else idx 10 (allow)
		{Code: bpfJEQK, K: afUnix, Jt: 1, Jf: 0},
		// 10: allow
		{Code: bpfRETK, K: seccompRetAllow},
		// 11: block with EPERM
		{Code: bpfRETK, K: seccompRetErrno | errnoEPERM},
	}
}

func networkDenySeccompFilter() []sockFilter {
	x86Section := networkDenySection(nrSocketX86_64, nrSocketpairX86_64, networkDenySyscallsX86_64)
	armSection := networkDenySection(nrSocketAARCH64, nrSocketpairAARCH64, networkDenySyscallsAARCH64)
	program := []sockFilter{
		{Code: bpfLDWABS, K: seccompOffsetArch},
		{Code: bpfJEQK, K: auditArchX86_64, Jt: 0, Jf: uint8(len(x86Section))},
	}
	program = append(program, x86Section...)
	program = append(program, sockFilter{Code: bpfJEQK, K: auditArchAARCH64, Jt: 0, Jf: uint8(len(armSection))})
	program = append(program, armSection...)
	program = append(program, sockFilter{Code: bpfRETK, K: seccompRetAllow})
	return program
}

func networkDenySection(socketNr uint32, socketpairNr uint32, deniedSyscalls []uint32) []sockFilter {
	section := []sockFilter{{Code: bpfLDWABS, K: seccompOffsetNr}}
	for _, nr := range deniedSyscalls {
		section = append(section,
			sockFilter{Code: bpfJEQK, K: nr, Jt: 0, Jf: 1},
			sockFilter{Code: bpfRETK, K: seccompRetErrno | errnoEPERM},
		)
	}
	section = appendNetworkSocketCheck(section, socketNr, true)
	section = appendNetworkSocketCheck(section, socketpairNr, false)
	section = append(section, sockFilter{Code: bpfRETK, K: seccompRetAllow})
	return section
}

func appendNetworkSocketCheck(section []sockFilter, nr uint32, reloadSyscallNumber bool) []sockFilter {
	section = append(section,
		sockFilter{Code: bpfJEQK, K: nr, Jt: 0, Jf: 4},
		sockFilter{Code: bpfLDWABS, K: seccompOffsetArg0},
		sockFilter{Code: bpfJEQK, K: afUnix, Jt: 1, Jf: 0},
		sockFilter{Code: bpfRETK, K: seccompRetErrno | errnoEPERM},
		sockFilter{Code: bpfRETK, K: seccompRetAllow},
	)
	if reloadSyscallNumber {
		section = append(section, sockFilter{Code: bpfLDWABS, K: seccompOffsetNr})
	}
	return section
}
