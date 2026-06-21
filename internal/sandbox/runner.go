package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
)

var errNativeSandboxUnavailable = errors.New("native sandbox backend is unavailable")

func nativeSandboxUnavailableError(backend Backend) error {
	message := strings.TrimSpace(backend.Message)
	if message == "" {
		return errNativeSandboxUnavailable
	}
	return fmt.Errorf("%w: %s", errNativeSandboxUnavailable, message)
}

type CommandSpec struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

type CommandPlan struct {
	Backend                 Backend           `json:"backend"`
	TargetBackend           BackendName       `json:"targetBackend"`
	WorkspaceRoot           string            `json:"workspaceRoot"`
	Policy                  Policy            `json:"policy"`
	PermissionProfile       PermissionProfile `json:"permissionProfile"`
	Wrapped                 bool              `json:"wrapped"`
	SandboxEnvMarkers       []string          `json:"sandboxEnvMarkers,omitempty"`
	EnforcementLevel        EnforcementLevel  `json:"enforcementLevel"`
	DowngradeReason         string            `json:"downgradeReason,omitempty"`
	RequiresPlatformSandbox bool              `json:"requiresPlatformSandbox"`
	Name                    string            `json:"name"`
	Args                    []string          `json:"args"`
	Dir                     string            `json:"dir,omitempty"`
	Env                     []string          `json:"env,omitempty"`
	SandboxDir              string            `json:"sandboxDir,omitempty"`
	// MonitorTag, when non-empty, is the unique marker embedded in the
	// sandbox-exec profile's denial messages; a caller passes it to
	// StartDenialMonitor to capture what the sandbox blocked. Empty unless
	// Policy.MonitorDenials is set on a macOS sandbox-exec plan.
	MonitorTag string `json:"monitorTag,omitempty"`
	// Notes records auditable least-privilege downgrade notes, such as native
	// isolation being unavailable in the selected environment. Surfaced to the
	// operator; never affects enforcement.
	Notes []string `json:"notes,omitempty"`
	// cleanup releases resources tied to the plan's lifetime. It is never
	// serialized; callers invoke it via Cleanup() once the command has finished.
	cleanup func()
}

// Cleanup releases any resources the plan holds. It is safe to call on a zero
// plan and to call more than once.
func (plan CommandPlan) Cleanup() {
	if plan.cleanup != nil {
		plan.cleanup()
	}
}

func (engine *Engine) CommandContext(ctx context.Context, spec CommandSpec) (*exec.Cmd, CommandPlan, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	plan, err := engine.BuildCommandPlan(spec)
	if err != nil {
		return nil, CommandPlan{}, err
	}
	command := exec.CommandContext(ctx, plan.Name, plan.Args...)
	command.Dir = plan.Dir
	command.Env = plan.Env
	return command, plan, nil
}

// writeRoots returns the full ordered write-root list for command plans:
// the workspace root plus any granted extra roots. The single-root fallback
// only applies to engines built without a workspace root (NewEngine always
// builds a scope otherwise); it is kept as defense in depth.
func (engine *Engine) writeRoots(workspaceRoot string) []string {
	var roots []string
	if engine.scope != nil {
		roots = engine.scope.Roots()
	} else {
		roots = []string{workspaceRoot}
	}
	// Reflect the policy's AllowWrite roots in the OS backend write binds so a
	// sandboxed shell command may write where the policy grants writes. DenyWrite
	// is enforced at the policy gate, and on sandbox-exec additionally as an
	// explicit deny rule (see sandboxExecProfile).
	policy := engine.effectivePolicy(engine.policy)
	if extra := resolveWriteRootPaths(policy.AllowWrite); len(extra) > 0 {
		roots = dedupeStrings(append(roots, extra...))
	}
	return roots
}

func (engine *Engine) BuildCommandPlan(spec CommandSpec) (CommandPlan, error) {
	if engine == nil {
		return directCommandPlan(spec, Backend{Name: BackendUnavailable, Message: "sandbox disabled"}, Policy{}, ""), nil
	}
	policy := engine.effectivePolicy(engine.policy)
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	workspaceRoot, commandDir, err := engine.resolveCommandDir(spec.Dir, policy)
	if err != nil {
		return CommandPlan{}, err
	}
	spec.Name = strings.TrimSpace(spec.Name)
	if spec.Name == "" {
		return CommandPlan{}, errors.New("sandbox command name is required")
	}
	spec.Dir = commandDir

	backend := engine.backend
	if backend.Name == "" {
		backend = Backend{Name: BackendUnavailable, Message: "native sandbox backend was not selected"}
	}
	preference := SandboxPreferenceAuto
	// Re-entrancy guard: a command spawned by a process we already wrapped (both
	// ZERO_SANDBOXED=1 and ZERO_SANDBOX_BACKEND set in its env — see
	// IsAlreadySandboxed) must not be wrapped again — nested platform wrappers
	// fail and a second sandbox wrapper would be redundant. Return a pass-through
	// plan.
	if IsAlreadySandboxed() {
		preference = SandboxPreferenceForbid
	}
	if policy.Mode == ModeDisabled {
		preference = SandboxPreferenceForbid
	}
	profile := PermissionProfileFromPolicy(workspaceRoot, policy, engine.scope)
	manager := NewSandboxManager(SandboxManagerOptions{
		GOOS:    backend.Platform,
		Backend: backend,
	})
	return manager.BuildCommandPlan(SandboxManagerRequest{
		WorkspaceRoot:     workspaceRoot,
		Command:           spec,
		Policy:            policy,
		Scope:             engine.scope,
		Profile:           profile,
		Preference:        preference,
		ValidateExecution: true,
	})
}

func buildPlatformCommandPlan(execRequest SandboxExecutionRequest, policy Policy) (CommandPlan, error) {
	if policy.Mode == "" {
		policy = DefaultPolicy()
	}
	spec := execRequest.Command
	backend := execRequest.Backend
	workspaceRoot := execRequest.WorkspaceRoot
	if execRequest.EnforcementLevel == EnforcementDisabled || execRequest.TargetBackend == BackendNone || !execRequest.RequiresPlatformSandbox {
		return withSandboxExecutionMetadata(directCommandPlan(spec, backend, policy, workspaceRoot), execRequest), nil
	}
	switch backend.Name {
	case BackendLinuxBwrap:
		if backend.Available && backend.Executable != "" {
			return linuxSandboxHelperCommandPlan(execRequest, policy)
		}
	case BackendMacOSSeatbelt:
		if backend.Available && backend.Executable != "" {
			return withSandboxExecutionMetadata(seatbeltCommandPlan(execRequest, policy, backend), execRequest), nil
		}
	case BackendWindowsRestrictedToken:
		if backend.Available && backend.Executable != "" {
			return windowsRestrictedTokenCommandPlan(execRequest, policy)
		}
	case BackendWSL:
		return CommandPlan{}, nativeSandboxUnavailableError(backend)
	}
	return CommandPlan{}, nativeSandboxUnavailableError(backend)
}

func linuxSandboxHelperCommandPlan(execRequest SandboxExecutionRequest, policy Policy) (CommandPlan, error) {
	spec := execRequest.Command
	helper := LinuxSandboxHelperCommand{}
	if execRequest.Backend.Name == BackendLinuxBwrap && execRequest.Backend.Executable != "" {
		helper.Name = execRequest.Backend.Executable
	} else {
		resolved, err := linuxSandboxHelperCommand()
		if err != nil {
			return CommandPlan{}, err
		}
		helper = resolved
	}
	command := append([]string{spec.Name}, spec.Args...)
	args, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:  execRequest.WorkspaceRoot,
		CommandCWD:        spec.Dir,
		PermissionProfile: execRequest.PermissionProfile,
		BlockUnixSockets:  policy.BlockUnixSockets,
		Command:           command,
	})
	if err != nil {
		return CommandPlan{}, err
	}
	planDir := spec.Dir
	if helper.Dir != "" {
		planDir = helper.Dir
	}
	plan := CommandPlan{
		Backend:           execRequest.Backend,
		TargetBackend:     execRequest.TargetBackend,
		WorkspaceRoot:     execRequest.WorkspaceRoot,
		Policy:            policy,
		Wrapped:           true,
		SandboxEnvMarkers: execRequest.SandboxEnvMarkers,
		EnforcementLevel:  execRequest.EnforcementLevel,
		Name:              helper.Name,
		Args:              append(append([]string{}, helper.ArgsPrefix...), args...),
		Dir:               planDir,
		Env:               cloneStrings(spec.Env),
		SandboxDir:        spec.Dir,
	}
	return withSandboxExecutionMetadata(plan, execRequest), nil
}

func withSandboxExecutionMetadata(plan CommandPlan, request SandboxExecutionRequest) CommandPlan {
	plan.Backend = request.Backend
	plan.TargetBackend = request.TargetBackend
	plan.PermissionProfile = request.PermissionProfile
	plan.SandboxEnvMarkers = request.SandboxEnvMarkers
	plan.EnforcementLevel = request.EnforcementLevel
	plan.DowngradeReason = request.DowngradeReason
	plan.RequiresPlatformSandbox = request.RequiresPlatformSandbox
	return plan
}

func directCommandPlan(spec CommandSpec, backend Backend, policy Policy, workspaceRoot string) CommandPlan {
	return CommandPlan{
		Backend:           backend,
		TargetBackend:     backend.TargetBackend(),
		WorkspaceRoot:     workspaceRoot,
		Policy:            policy,
		Wrapped:           false,
		SandboxEnvMarkers: backend.SandboxEnvMarkers(policy),
		EnforcementLevel:  backend.EnforcementLevel(policy),
		DowngradeReason:   backend.DowngradeReason(policy),
		Name:              spec.Name,
		Args:              cloneStrings(spec.Args),
		Dir:               spec.Dir,
		Env:               cloneStrings(spec.Env),
	}
}

func (engine *Engine) resolveCommandDir(dir string, policy Policy) (string, string, error) {
	workspaceRoot := strings.TrimSpace(engine.workspaceRoot)
	if workspaceRoot == "" {
		return "", "", errors.New("sandbox workspace root is required")
	}
	workspaceRoot = filepath.Clean(workspaceRoot)
	if !filepath.IsAbs(workspaceRoot) {
		absolute, err := filepath.Abs(workspaceRoot)
		if err != nil {
			return "", "", fmt.Errorf("resolve sandbox workspace: %w", err)
		}
		workspaceRoot = absolute
	}
	if resolved, err := filepath.EvalSymlinks(workspaceRoot); err == nil {
		workspaceRoot = resolved
	}

	commandDir := strings.TrimSpace(dir)
	if commandDir == "" {
		commandDir = workspaceRoot
	} else if !filepath.IsAbs(commandDir) {
		commandDir = filepath.Join(workspaceRoot, commandDir)
	}
	commandDir = filepath.Clean(commandDir)
	if resolved, err := filepath.EvalSymlinks(commandDir); err == nil {
		commandDir = resolved
	}
	if policy.EnforceWorkspace {
		if block := engine.scopeFor(engine.workspaceRoot).validate(commandDir); block != nil {
			return "", "", Block{
				Code:     block.Code,
				ToolName: "sandbox_command",
				Action:   ActionDeny,
				Risk: Risk{
					Level:      RiskCritical,
					Categories: []string{"path_escape"},
					Reason:     "critical risk: path_escape",
				},
				Path:   block.Path,
				Reason: block.Reason,
			}
		}
	}
	return workspaceRoot, commandDir, nil
}

func seatbeltCommandPlan(execRequest SandboxExecutionRequest, policy Policy, backend Backend) CommandPlan {
	return seatbeltCommandPlanWithProfile(execRequest.Command, execRequest.WorkspaceRoot, execRequest.PermissionProfile, policy, backend)
}

func seatbeltCommandPlanWithProfile(spec CommandSpec, workspaceRoot string, profile PermissionProfile, policy Policy, backend Backend) CommandPlan {
	denialTag := ""
	if policy.MonitorDenials {
		denialTag = nextSandboxDenialTag()
	}
	args := []string{"-p", seatbeltProfileFromPermissionProfile(profile, policy, denialTag), spec.Name}
	args = append(args, spec.Args...)
	envBackend := backend.Name
	if envBackend == "" {
		envBackend = BackendMacOSSeatbelt
	}
	env := sandboxEnvironment(policy, envBackend, workspaceRoot)
	plan := CommandPlan{
		Backend:           backend,
		TargetBackend:     backend.TargetBackend(),
		WorkspaceRoot:     workspaceRoot,
		Policy:            policy,
		Wrapped:           true,
		SandboxEnvMarkers: backend.SandboxEnvMarkers(policy),
		EnforcementLevel:  backend.EnforcementLevel(policy),
		Name:              backend.Executable,
		Args:              args,
		Dir:               spec.Dir,
		Env:               env,
		SandboxDir:        spec.Dir,
	}
	// The plan's monitor tag MUST equal the one embedded in the profile above so the
	// monitor matches exactly this run's denials.
	plan.MonitorTag = denialTag
	return plan
}

func seatbeltCompatibilityPermissionProfile(writeRoots []string, policy Policy) PermissionProfile {
	fs := FileSystemPolicy{
		Kind:                 FileSystemUnrestricted,
		ReadRoots:            []string{string(filepath.Separator)},
		IncludePlatformRoots: true,
		AllowTemp:            true,
	}
	if policy.EnforceWorkspace {
		fs.Kind = FileSystemRestricted
		fs.WriteRoots = make([]WritableRoot, 0, len(writeRoots))
		for _, root := range writeRoots {
			fs.WriteRoots = append(fs.WriteRoots, WritableRoot{Root: root})
		}
	}
	fs.DenyRead = normalizeProfilePaths(policy.DenyRead)
	fs.DenyWrite = normalizeProfilePaths(policy.DenyWrite)
	return PermissionProfile{
		FileSystem: fs,
		Network:    NetworkPolicy{Mode: policy.Network},
	}
}

func existingBubblewrapMounts() []string {
	candidates := []string{"/bin", "/usr", "/lib", "/lib64", "/sbin", "/etc"}
	mounts := []string{}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			mounts = append(mounts, candidate)
		}
	}
	return mounts
}

func sandboxEnvironment(policy Policy, backend BackendName, home string) []string {
	pathValue := firstEnv("PATH", defaultPath())
	if runtime.GOOS == "darwin" {
		// The sandbox runs commands with a scrubbed, minimal PATH that omits the
		// Homebrew / usr-local bin dirs, so a bare `python3`/`node` resolves to a
		// system stub (the macOS /usr/bin/python3 placeholder) or is not found at
		// all. Make the user-tool dirs reachable to match the read/map-executable
		// allowances added for those trees.
		pathValue = ensureMacToolPaths(pathValue)
	}
	env := []string{
		"HOME=" + home,
		"PATH=" + pathValue,
		"TERM=" + firstEnv("TERM", "dumb"),
		EnvSandboxBackend + "=" + string(backend),
		"ZERO_SANDBOX_NETWORK=" + string(policy.Network),
		EnvSandboxed + "=1",
	}
	if runtime.GOOS == "windows" {
		env = append(env, "COMSPEC="+firstEnv("COMSPEC", "cmd.exe"))
	}
	return env
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}

func firstEnv(key string, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func defaultPath() string {
	if runtime.GOOS == "windows" {
		return os.Getenv("PATH")
	}
	return "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
}

// macToolPaths are the user-package bin dirs that hold node/python/etc. on macOS
// (Homebrew on Apple Silicon under /opt/homebrew, and /usr/local for Intel/older
// installs). They are NOT in the scrubbed default PATH, so the sandbox would
// otherwise miss user-installed interpreters.
var macToolPaths = []string{
	"/opt/homebrew/bin",
	"/opt/homebrew/sbin",
	"/usr/local/bin",
	"/usr/local/sbin",
}

// ensureMacToolPaths prepends any missing macToolPaths to a PATH value so a
// sandboxed command can find Homebrew/usr-local tools. Existing entries are kept
// in place (no duplicates, original order preserved after the prepended dirs).
func ensureMacToolPaths(path string) string {
	present := make(map[string]bool)
	for _, entry := range strings.Split(path, ":") {
		if entry != "" {
			present[entry] = true
		}
	}
	missing := make([]string, 0, len(macToolPaths))
	for _, dir := range macToolPaths {
		if !present[dir] {
			missing = append(missing, dir)
		}
	}
	if len(missing) == 0 {
		return path
	}
	if path == "" {
		return strings.Join(missing, ":")
	}
	return strings.Join(missing, ":") + ":" + path
}

// sandboxWritableDevices are the standard character devices that virtually every
// command needs to write to (e.g. `> /dev/null`). The bubblewrap backend exposes
// these via `--dev /dev`; the sandbox-exec profile must allow them explicitly or
// the equivalent operations fail with "Operation not permitted".
var sandboxWritableDevices = []string{
	"/dev/null",
	"/dev/zero",
	"/dev/random",
	"/dev/urandom",
	"/dev/stdin",
	"/dev/stdout",
	"/dev/stderr",
	"/dev/tty",
	"/dev/dtracehelper",
}

// sandboxWritableSubpaths are non-workspace trees the sandbox-exec profile must
// keep writable for parity with the bubblewrap backend's writable /tmp tmpfs.
// macOS resolves /tmp and /var to their /private counterparts before the sandbox
// check, so both forms are listed. /dev/fd covers process-substitution writes.
var sandboxWritableSubpaths = []string{
	"/tmp",
	"/private/tmp",
	"/var/tmp",
	"/private/var/tmp",
	"/var/folders",
	"/private/var/folders",
	"/dev/fd",
}

// sandboxMachServices is the curated allowlist of Mach services a sandboxed
// command may look up. Under the seatbelt default-deny, XPC to common system
// daemons is otherwise blocked, so tools that touch the keychain
// (securityd/trustd), user/group lookup (opendirectoryd), preferences
// (cfprefsd), network config (SystemConfiguration), launch services, or the
// pasteboard fail. None of these grant filesystem or network access — those stay
// governed by the file-write and network rules below — so the workspace boundary
// is unaffected.
var sandboxMachServices = []string{
	"com.apple.system.opendirectoryd.libinfo",
	"com.apple.system.opendirectoryd.membership",
	"com.apple.system.opendirectoryd.api",
	"com.apple.system.logger",
	"com.apple.logd",
	"com.apple.cfprefsd.daemon",
	"com.apple.cfprefsd.agent",
	"com.apple.securityd",
	"com.apple.securityd.xpc",
	"com.apple.SecurityServer",
	"com.apple.trustd",
	"com.apple.trustd.agent",
	"com.apple.SystemConfiguration.configd",
	"com.apple.SystemConfiguration.DNSConfiguration",
	"com.apple.lsd.mapdb",
	"com.apple.coreservices.launchservicesd",
	"com.apple.pasteboard.1",
}

// sandboxDenialLogTag is the base marker for a sandbox-exec denial in the unified
// log when Policy.MonitorDenials is set; nextSandboxDenialTag derives a unique
// per-plan tag from it so the runtime monitor can find this run's denials via
// `log stream`.
const sandboxDenialLogTag = "zero-sandbox-denied-v1"

// sandboxDenialTagSeq makes each monitored plan's denial tag unique.
var sandboxDenialTagSeq atomic.Uint64

// nextSandboxDenialTag returns a process-unique denial tag. Without uniqueness,
// two concurrent monitored commands share one marker and StartDenialMonitor —
// which filters `log stream` only by tag — would ingest each other's denials,
// leaking unrelated paths/hosts into the wrong <sandbox_blocks> block. The pid
// disambiguates across processes; the counter across plans within a process.
func nextSandboxDenialTag() string {
	return fmt.Sprintf("%s-%d-%d", sandboxDenialLogTag, os.Getpid(), sandboxDenialTagSeq.Add(1))
}

func sandboxMachLookupRule() string {
	filters := make([]string, 0, len(sandboxMachServices))
	for _, service := range sandboxMachServices {
		filters = append(filters, `(global-name "`+sandboxProfileString(service)+`")`)
	}
	return "(allow mach-lookup\n  " + strings.Join(filters, "\n  ") + ")"
}

func sandboxExecProfile(writeRoots []string, policy Policy, denialTag string) string {
	return seatbeltProfileFromPermissionProfile(seatbeltCompatibilityPermissionProfile(writeRoots, policy), policy, denialTag)
}

func seatbeltProfileFromPermissionProfile(profile PermissionProfile, policy Policy, denialTag string) string {
	networkRule := networkRuleForProfile(profile.Network)
	readRule := seatbeltReadRule(profile.FileSystem)
	writeRule := seatbeltWriteRule(profile.FileSystem)
	denyDefault := "(deny default)"
	if denialTag != "" {
		// Tag denials so the runtime log monitor can attribute them to THIS run; the
		// message is emitted to the unified log on every deny and StartDenialMonitor
		// filters `log stream` for this exact (per-plan) tag.
		denyDefault = `(deny default (with message "` + sandboxProfileString(denialTag) + `"))`
	}
	rules := []string{
		"(version 1)",
		denyDefault,
		"(allow process*)",
		"(allow process-info* (target same-sandbox))",
		"(allow sysctl-read)",
		"(allow sysctl-write (sysctl-name \"kern.grade_cputype\"))",
		"(allow iokit-open (iokit-registry-entry-class \"RootDomainUserClient\"))",
		"(allow ipc-posix-sem)",
		`(allow ipc-posix-shm-read-data ipc-posix-shm-write-create ipc-posix-shm-write-unlink (ipc-posix-name-regex #"^/__KMP_REGISTERED_LIB_[0-9]+$"))`,
		"(allow pseudo-tty)",
		`(allow file-read* file-write* file-ioctl (literal "/dev/ptmx"))`,
		`(allow file-read* file-write* (require-all (regex #"^/dev/ttys[0-9]+") (extension "com.apple.sandbox.pty")))`,
		`(allow file-ioctl (regex #"^/dev/ttys[0-9]+"))`,
		"(allow ipc-posix-shm-read* (ipc-posix-name-prefix \"apple.cfprefs.\"))",
		"(allow user-preference-read)",
		// Let a sandboxed command signal itself and its own process group so scripts
		// that spawn and kill children (e.g. `sleep 30 & kill %1`, test runners,
		// timeouts) work. The target restriction keeps it from signalling any
		// process outside its own group.
		"(allow signal (target self) (target pgrp))",
		sandboxMachLookupRule(),
		seatbeltPlatformRuntimeRules(),
		readRule,
		writeRule,
	}
	rules = append(rules, denyReadRules(profile.FileSystem)...)
	rules = append(rules, writeRootCarveoutDenyRules(profile.FileSystem)...)
	rules = append(rules, denyWriteRulesFromPaths(profile.FileSystem.DenyWrite)...)
	rules = append(rules, networkRule)
	return strings.Join(nonEmptyStrings(rules), "\n")
}

func seatbeltReadRule(fs FileSystemPolicy) string {
	if fs.Kind == FileSystemUnrestricted {
		return "(allow file-read*)"
	}
	filters := make([]string, 0, len(fs.ReadRoots)+len(macosPlatformReadRoots()))
	for _, root := range fs.ReadRoots {
		filters = appendSeatbeltSubpathFilter(filters, root)
	}
	if fs.IncludePlatformRoots {
		for _, root := range macosPlatformReadRoots() {
			filters = appendSeatbeltSubpathFilter(filters, root)
		}
	}
	if len(filters) == 0 {
		return ""
	}
	return "(allow file-read* file-test-existence\n  " + strings.Join(filters, "\n  ") + ")"
}

func seatbeltWriteRule(fs FileSystemPolicy) string {
	if fs.Kind == FileSystemUnrestricted {
		return "(allow file-write*)"
	}
	filters := make([]string, 0, len(fs.WriteRoots)+len(sandboxWritableSubpaths)+len(sandboxWritableDevices))
	for _, root := range fs.WriteRoots {
		if filter := seatbeltWritableRootFilter(root); filter != "" {
			filters = append(filters, filter)
		}
	}
	if fs.AllowTemp {
		for _, subpath := range sandboxWritableSubpaths {
			filters = append(filters, `(subpath "`+sandboxProfileString(subpath)+`")`)
		}
	}
	for _, device := range sandboxWritableDevices {
		filters = append(filters, `(literal "`+sandboxProfileString(device)+`")`)
	}
	if len(filters) == 0 {
		return ""
	}
	return "(allow file-write*\n  " + strings.Join(filters, "\n  ") + ")"
}

func seatbeltWritableRootFilter(root WritableRoot) string {
	rootPath := strings.TrimSpace(root.Root)
	if rootPath == "" {
		return ""
	}
	return `(subpath "` + sandboxProfileString(rootPath) + `")`
}

func seatbeltProtectedMetadataRegex(root string, name string) string {
	root = strings.TrimSpace(filepath.ToSlash(filepath.Clean(root)))
	name = strings.Trim(strings.TrimSpace(name), `/\`)
	if root == "" || name == "" || name == "." {
		return ""
	}
	root = strings.TrimRight(root, "/")
	if root == "" {
		root = "/"
	}
	escapedRoot := regexpQuoteMeta(root)
	escapedName := regexpQuoteMeta(name)
	if root == "/" {
		return "^/" + escapedName + "(/.*)?$"
	}
	return "^" + escapedRoot + "/" + escapedName + "(/.*)?$"
}

func denyReadRules(fs FileSystemPolicy) []string {
	return denySeatbeltPathRules("file-read*", fs.DenyRead)
}

func writeRootCarveoutDenyRules(fs FileSystemPolicy) []string {
	if fs.Kind != FileSystemRestricted {
		return nil
	}
	var out []string
	for _, root := range fs.WriteRoots {
		for _, subpath := range root.ReadOnlySubpaths {
			subpath = strings.TrimSpace(subpath)
			if subpath == "" {
				continue
			}
			escaped := sandboxProfileString(subpath)
			out = append(out,
				`(deny file-write* (literal "`+escaped+`"))`,
				`(deny file-write* (subpath "`+escaped+`"))`,
			)
		}
		for _, name := range root.ProtectedMetadataNames {
			regex := seatbeltProtectedMetadataRegex(root.Root, name)
			if regex == "" {
				continue
			}
			out = append(out, `(deny file-write* (regex #"`+sandboxProfileRegex(regex)+`"))`)
		}
	}
	return out
}

// denyWriteRules returns seatbelt deny clauses for the policy's resolved
// DenyWrite paths: a (subpath ...) clause for a directory, a (literal ...) clause
// for a single file. Empty when DenyWrite is unset.
func denyWriteRules(policy Policy) []string {
	return denyWriteRulesFromPaths(resolvePolicyPaths(policy.DenyWrite))
}

func denyWriteRulesFromPaths(paths []string) []string {
	return denySeatbeltPathRules("file-write*", paths)
}

func denySeatbeltPathRules(action string, paths []string) []string {
	resolved := normalizeProfilePaths(paths)
	if len(resolved) == 0 {
		return nil
	}
	out := make([]string, 0, len(resolved)*2)
	for _, path := range resolved {
		filters := []string{`(subpath "` + sandboxProfileString(path) + `")`}
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			filters = []string{`(literal "` + sandboxProfileString(path) + `")`}
		} else {
			filters = append(filters, `(literal "`+sandboxProfileString(path)+`")`)
		}
		for _, filter := range filters {
			out = append(out, "(deny "+action+" "+filter+")")
			if action == "file-read*" {
				out = append(out, "(deny file-write-unlink "+filter+")")
			}
		}
	}
	return out
}

// networkRuleFor returns the seatbelt network clause for a policy.
func networkRuleFor(policy Policy) string {
	return networkRuleForProfile(NetworkPolicy{Mode: policy.Network})
}

func networkRuleForProfile(network NetworkPolicy) string {
	switch network.Mode {
	case NetworkAllow:
		return "(allow network*)"
	default:
		return "(deny network*)"
	}
}

func sandboxProfileString(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`)
	return replacer.Replace(value)
}

func sandboxProfileRegex(value string) string {
	replacer := strings.NewReplacer(`"`, `\"`, "\n", `\n`, "\r", `\r`)
	return replacer.Replace(value)
}

func appendSeatbeltSubpathFilter(filters []string, root string) []string {
	root = strings.TrimSpace(root)
	if root == "" {
		return filters
	}
	return append(filters, `(subpath "`+sandboxProfileString(root)+`")`)
}

func macosPlatformReadRoots() []string {
	return []string{
		"/bin",
		"/sbin",
		"/Applications",
		"/Library/Apple/System/Library/Frameworks",
		"/Library/Apple/System/Library/PrivateFrameworks",
		"/Library/Apple/usr/lib",
		"/usr/bin",
		"/usr/sbin",
		"/usr/lib",
		"/usr/libexec",
		"/usr/share",
		// User-installed package trees (Homebrew on Apple Silicon, /usr/local on
		// Intel / older installs). Broadened from the bare lib dirs to the whole
		// tree so the interpreters in .../bin and their Cellar/opt dylibs are
		// readable; writes stay confined to the workspace.
		"/usr/local",
		"/opt/homebrew",
		"/etc",
		"/private/etc",
		"/var/db",
		"/private/var/db",
		"/System/Library",
		"/System/iOSSupport/System/Library/Frameworks",
		"/System/iOSSupport/System/Library/PrivateFrameworks",
		"/System/iOSSupport/System/Library/SubFrameworks",
		"/Library/Apple",
		"/Library/Preferences",
		"/dev",
	}
}

func seatbeltPlatformRuntimeRules() string {
	return strings.Join([]string{
		`(allow file-map-executable`,
		`  (subpath "/Library/Apple/System/Library/Frameworks")`,
		`  (subpath "/Library/Apple/System/Library/PrivateFrameworks")`,
		`  (subpath "/Library/Apple/usr/lib")`,
		`  (subpath "/System/Library/Extensions")`,
		`  (subpath "/System/Library/Frameworks")`,
		`  (subpath "/System/Library/PrivateFrameworks")`,
		`  (subpath "/System/Library/SubFrameworks")`,
		`  (subpath "/System/iOSSupport/System/Library/Frameworks")`,
		`  (subpath "/System/iOSSupport/System/Library/PrivateFrameworks")`,
		`  (subpath "/System/iOSSupport/System/Library/SubFrameworks")`,
		`  (subpath "/usr/lib")`,
		// User-installed tools load their own dylibs (e.g. node -> libnode/libuv,
		// python3 -> its framework) from these trees; without map-executable here
		// a Homebrew/usr-local binary fails to start even when it is on PATH.
		`  (subpath "/usr/local")`,
		`  (subpath "/opt/homebrew"))`,
		`(allow system-mac-syscall (mac-policy-name "vnguard"))`,
		`(allow system-mac-syscall (require-all (mac-policy-name "Sandbox") (mac-syscall-number 67)))`,
		`(allow file-read-metadata file-test-existence`,
		`  (literal "/etc")`,
		`  (literal "/tmp")`,
		`  (literal "/var")`,
		`  (literal "/private/etc/localtime"))`,
		`(allow file-read-metadata file-test-existence (path-ancestors "/System/Volumes/Data/private"))`,
		// realpath()/getcwd() lstat every ancestor of a path, so resolving a tool
		// under /opt/homebrew or /usr/local also stats the parent dirs (e.g. /opt,
		// /usr). The subpath read rules above cover the trees themselves but not
		// those ancestors, so without this a Homebrew python3 fails at startup with
		// "realpath: /opt/homebrew/bin/: Operation not permitted" even though it is
		// on PATH and readable. node only exec()s (kernel traversal) so it is not
		// affected — which is exactly how this asymmetry was diagnosed.
		`(allow file-read-metadata file-test-existence (path-ancestors "/opt/homebrew") (path-ancestors "/usr/local"))`,
		`(allow file-read* file-test-existence (literal "/"))`,
		`(allow system-fsctl (fsctl-command FSIOC_CAS_BSDFLAGS))`,
		`(allow file-read* file-test-existence`,
		`  (literal "/dev/autofs_nowait")`,
		`  (literal "/dev/random")`,
		`  (literal "/dev/urandom")`,
		`  (literal "/private/etc/master.passwd")`,
		`  (literal "/private/etc/passwd")`,
		`  (literal "/private/etc/protocols")`,
		`  (literal "/private/etc/services"))`,
		`(allow file-read* file-test-existence file-write-data`,
		`  (literal "/dev/null")`,
		`  (literal "/dev/zero"))`,
		`(allow file-read-data file-test-existence file-write-data (subpath "/dev/fd"))`,
		`(allow file-read* file-test-existence file-write-data file-ioctl (literal "/dev/dtracehelper"))`,
		`(allow file-read* file-test-existence file-write* (subpath "/tmp"))`,
		`(allow file-read* file-write* (subpath "/private/tmp"))`,
		`(allow file-read* file-write* (subpath "/var/tmp"))`,
		`(allow file-read* file-write* (subpath "/private/var/tmp"))`,
		`(allow file-read* file-test-existence`,
		`  (literal "/System/Library/CoreServices")`,
		`  (literal "/System/Library/CoreServices/.SystemVersionPlatform.plist")`,
		`  (literal "/System/Library/CoreServices/SystemVersion.plist"))`,
		`(allow file-read-metadata (subpath "/var"))`,
		`(allow file-read-metadata (subpath "/private/var"))`,
		`(allow network-outbound (literal "/private/var/run/syslog"))`,
		`(allow ipc-posix-shm-read* (ipc-posix-name "apple.shm.notification_center"))`,
		`(allow file-read* (literal "/private/var/db/eligibilityd/eligibility.plist"))`,
		`(allow file-read* (extension "com.apple.app-sandbox.read"))`,
		`(allow file-read* file-write* (extension "com.apple.app-sandbox.read-write"))`,
	}, "\n")
}

func nonEmptyStrings(values []string) []string {
	out := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func regexpQuoteMeta(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`.`, `\.`,
		`+`, `\+`,
		`*`, `\*`,
		`?`, `\?`,
		`(`, `\(`,
		`)`, `\)`,
		`|`, `\|`,
		`[`, `\[`,
		`]`, `\]`,
		`{`, `\{`,
		`}`, `\}`,
		`^`, `\^`,
		`$`, `\$`,
	)
	return replacer.Replace(value)
}
