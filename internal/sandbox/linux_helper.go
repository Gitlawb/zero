package sandbox

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

const LinuxSandboxHelperName = "zero-linux-sandbox"

const linuxSandboxBackendEnv = BackendLinuxBwrap

type LinuxSandboxCommandArgsOptions struct {
	SandboxPolicyCWD     string
	CommandCWD           string
	PermissionProfile    PermissionProfile
	UseLandlock          bool
	ApplySeccompThenExec bool
	BlockUnixSockets     bool
	NoProc               bool
	PolicyReportPath     string
	Command              []string
}

type LinuxSandboxHelperConfig struct {
	SandboxPolicyCWD     string
	CommandCWD           string
	PermissionProfile    PermissionProfile
	UseLandlock          bool
	ApplySeccompThenExec bool
	BlockUnixSockets     bool
	NoProc               bool
	PolicyReportPath     string
	Command              []string
}

type LinuxSandboxHelperCommand struct {
	Name       string
	ArgsPrefix []string
	Dir        string
}

type LinuxSandboxBwrapOptions struct {
	Config     LinuxSandboxHelperConfig
	HelperPath string
}

type linuxSandboxBwrapPlan struct {
	Args                   []string
	ProtectedCreateTargets []string
}

type linuxBwrapFilesystemPlan struct {
	Args                   []string
	ProtectedCreateTargets []string
}

var linuxSandboxHelperCommand = findLinuxSandboxHelperCommand

func BuildLinuxSandboxCommandArgs(options LinuxSandboxCommandArgsOptions) ([]string, error) {
	sandboxPolicyCWD := strings.TrimSpace(options.SandboxPolicyCWD)
	if sandboxPolicyCWD == "" {
		return nil, errors.New("linux sandbox helper requires sandbox policy cwd")
	}
	commandCWD := strings.TrimSpace(options.CommandCWD)
	if commandCWD == "" {
		commandCWD = sandboxPolicyCWD
	}
	if len(options.Command) == 0 {
		return nil, errors.New("linux sandbox helper requires command")
	}
	profileJSON, err := json.Marshal(options.PermissionProfile)
	if err != nil {
		return nil, fmt.Errorf("marshal linux sandbox permission profile: %w", err)
	}
	args := []string{
		"--sandbox-policy-cwd", sandboxPolicyCWD,
		"--command-cwd", commandCWD,
		"--permission-profile", string(profileJSON),
	}
	if options.UseLandlock {
		args = append(args, "--use-landlock")
	}
	if options.ApplySeccompThenExec {
		args = append(args, "--apply-seccomp-then-exec")
	}
	if options.BlockUnixSockets {
		args = append(args, "--block-unix-sockets")
	}
	if options.NoProc {
		args = append(args, "--no-proc")
	}
	if strings.TrimSpace(options.PolicyReportPath) != "" {
		args = append(args, "--policy-report-path", options.PolicyReportPath)
	}
	args = append(args, "--")
	args = append(args, options.Command...)
	return args, nil
}

func ParseLinuxSandboxHelperArgs(args []string) (LinuxSandboxHelperConfig, error) {
	var config LinuxSandboxHelperConfig
	var profileJSON string
	flags := flag.NewFlagSet(LinuxSandboxHelperName, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&config.SandboxPolicyCWD, "sandbox-policy-cwd", "", "sandbox policy cwd")
	flags.StringVar(&config.CommandCWD, "command-cwd", "", "command cwd")
	flags.StringVar(&profileJSON, "permission-profile", "", "permission profile JSON")
	flags.BoolVar(&config.UseLandlock, "use-landlock", false, "use Landlock backend")
	flags.BoolVar(&config.ApplySeccompThenExec, "apply-seccomp-then-exec", false, "apply seccomp before exec")
	flags.BoolVar(&config.BlockUnixSockets, "block-unix-sockets", false, "block AF_UNIX sockets before exec")
	flags.BoolVar(&config.NoProc, "no-proc", false, "skip proc mount")
	flags.StringVar(&config.PolicyReportPath, "policy-report-path", "", "structured policy report path")
	if err := flags.Parse(args); err != nil {
		return LinuxSandboxHelperConfig{}, err
	}
	config.SandboxPolicyCWD = strings.TrimSpace(config.SandboxPolicyCWD)
	if config.SandboxPolicyCWD == "" {
		return LinuxSandboxHelperConfig{}, errors.New("missing --sandbox-policy-cwd")
	}
	config.CommandCWD = strings.TrimSpace(config.CommandCWD)
	if config.CommandCWD == "" {
		config.CommandCWD = config.SandboxPolicyCWD
	}
	profileJSON = strings.TrimSpace(profileJSON)
	if profileJSON == "" {
		return LinuxSandboxHelperConfig{}, errors.New("missing --permission-profile")
	}
	if err := json.Unmarshal([]byte(profileJSON), &config.PermissionProfile); err != nil {
		return LinuxSandboxHelperConfig{}, fmt.Errorf("invalid --permission-profile: %w", err)
	}
	config.Command = flags.Args()
	config.PolicyReportPath = strings.TrimSpace(config.PolicyReportPath)
	if len(config.Command) == 0 {
		return LinuxSandboxHelperConfig{}, errors.New("missing command after --")
	}
	return config, nil
}

func BuildLinuxSandboxBwrapArgs(options LinuxSandboxBwrapOptions) ([]string, error) {
	plan, err := buildLinuxSandboxBwrapPlan(options)
	if err != nil {
		return nil, err
	}
	return plan.Args, nil
}

func buildLinuxSandboxBwrapPlan(options LinuxSandboxBwrapOptions) (linuxSandboxBwrapPlan, error) {
	config := options.Config
	if config.ApplySeccompThenExec {
		return linuxSandboxBwrapPlan{}, errors.New("inner seccomp stage cannot be wrapped by bubblewrap again")
	}
	if config.UseLandlock {
		return linuxSandboxBwrapPlan{}, errors.New("linux landlock helper mode is not implemented yet")
	}
	if err := validateLinuxBwrapPermissionProfile(config.PermissionProfile); err != nil {
		return linuxSandboxBwrapPlan{}, err
	}
	helperPath := strings.TrimSpace(options.HelperPath)
	if helperPath == "" {
		return linuxSandboxBwrapPlan{}, errors.New("linux sandbox helper path is required")
	}
	commandCWD := strings.TrimSpace(config.CommandCWD)
	if commandCWD == "" {
		commandCWD = config.SandboxPolicyCWD
	}
	innerArgs, err := BuildLinuxSandboxCommandArgs(LinuxSandboxCommandArgsOptions{
		SandboxPolicyCWD:     config.SandboxPolicyCWD,
		CommandCWD:           commandCWD,
		PermissionProfile:    config.PermissionProfile,
		ApplySeccompThenExec: true,
		BlockUnixSockets:     config.BlockUnixSockets,
		NoProc:               config.NoProc,
		Command:              config.Command,
	})
	if err != nil {
		return linuxSandboxBwrapPlan{}, err
	}
	args := []string{
		"--new-session",
		"--die-with-parent",
	}
	filesystemPlan := buildLinuxBwrapFilesystemPlan(config.PermissionProfile)
	args = append(args, filesystemPlan.Args...)
	if pathExists(helperPath) {
		args = append(args, "--ro-bind", helperPath, helperPath)
	}
	args = append(args,
		"--unshare-user",
		"--unshare-pid",
	)
	// Keep IPC and UTS shared for compatibility with the host CLI environment;
	// network isolation is still applied below when the policy denies egress.
	if shouldUnshareLinuxNetwork(config.PermissionProfile.Network) {
		args = append(args, "--unshare-net")
	}
	if !config.NoProc {
		args = append(args, "--proc", "/proc")
	}
	args = append(args, "--chdir", commandCWD)
	for _, env := range linuxHelperSandboxEnvironmentOverrides(config.PermissionProfile) {
		key, value, ok := strings.Cut(env, "=")
		if ok {
			args = append(args, "--setenv", key, value)
		}
	}
	args = append(args, "--", helperPath)
	args = append(args, innerArgs...)
	return linuxSandboxBwrapPlan{
		Args:                   args,
		ProtectedCreateTargets: filesystemPlan.ProtectedCreateTargets,
	}, nil
}

func validateLinuxBwrapPermissionProfile(profile PermissionProfile) error {
	if files := profile.FileSystem.ProcessTrustedDenyReadFiles; len(files) > 0 {
		return fmt.Errorf("bubblewrap cannot securely deny credential files outside the Zero config directory across atomic replacement: %s; move the store under $XDG_CONFIG_HOME/zero or add its path to sandbox allowRead", strings.Join(files, ", "))
	}
	// The same limitation, for a token store named by the command's own
	// environment. A /dev/null bind over the pathname is detached by the store's
	// next atomic rename, and an absent one is skipped entirely — so plaintext
	// published during the run stays readable. Refusing the command is the only
	// honest answer bubblewrap can give; a pathname-policy backend enforces
	// these without help. The path is not created or mutated to make the mask
	// work: doing that for a command-supplied value would let a command steer
	// Zero into creating host directories.
	if files := profile.FileSystem.CommandDenyReadFinalFiles; len(files) > 0 {
		return fmt.Errorf("bubblewrap cannot securely deny command-supplied credential files outside the Zero config directory across atomic replacement: %s; move the store under $XDG_CONFIG_HOME/zero or add its path to sandbox allowRead", strings.Join(files, ", "))
	}
	for _, dir := range profile.FileSystem.CommandDenyReadDirs {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("bubblewrap cannot securely deny command-supplied credential directory created after launch: %s; create the directory before running the command, move it outside the command environment, or add its path to sandbox allowRead", dir)
		}
	}
	return nil
}

func linuxBwrapFilesystemArgs(profile PermissionProfile) []string {
	return buildLinuxBwrapFilesystemPlan(profile).Args
}

func buildLinuxBwrapFilesystemPlan(profile PermissionProfile) linuxBwrapFilesystemPlan {
	fs := profile.FileSystem
	if fs.Kind == FileSystemUnrestricted {
		// Disabled filesystem policy means no write jail: expose the host root
		// read-write, including the host /dev tree, rather than synthesizing a
		// restricted bubblewrap filesystem.
		args := []string{"--bind", "/", "/"}
		for _, root := range fs.WriteRoots {
			if pathExists(root.Root) {
				args = append(args, "--bind", root.Root, root.Root)
			}
		}
		return linuxBwrapFilesystemPlan{Args: args}
	}

	args := []string{}
	protectedCreateTargets := []string{}
	if linuxProfileHasFullReadRoot(fs) {
		args = append(args, "--ro-bind", "/", "/", "--dev", "/dev")
	} else {
		args = append(args, "--tmpfs", "/", "--dev", "/dev")
		if fs.IncludePlatformRoots {
			for _, root := range linuxPlatformReadRoots() {
				args = append(args, "--ro-bind", root, root)
			}
		}
		for _, root := range fs.ReadRoots {
			if pathExists(root) {
				args = append(args, "--ro-bind", root, root)
			}
		}
	}
	if fs.AllowTemp {
		fs.WriteRoots = linuxWriteRootsWithTemp(fs)
	}
	for _, root := range linuxSortedWriteRoots(fs.WriteRoots) {
		if !pathExists(root.Root) {
			continue
		}
		args = append(args, "--bind", root.Root, root.Root)
		for _, subpath := range root.ReadOnlySubpaths {
			args = appendReadOnlyLinuxPathArgs(args, subpath)
		}
		for _, name := range root.ProtectedMetadataNames {
			path := filepath.Join(root.Root, name)
			if pathExists(path) {
				args = appendReadOnlyLinuxPathArgs(args, path)
			} else {
				protectedCreateTargets = append(protectedCreateTargets, path)
			}
		}
	}
	for _, path := range fs.DenyWrite {
		args = appendReadOnlyLinuxPathArgs(args, path)
	}
	var unreadable []string
	unreadable = append(unreadable, fs.DenyRead...)
	// The profile includes only trusted, process-environment-derived directories
	// here. Command-controlled credential roots remain deny-if-present and must
	// never cause host filesystem mutations before sandbox launch.
	ensureLinuxDenyReadDirs(fs.EnsureDenyReadDirs)
	for _, path := range fs.DenyReadIfExists {
		if !pathExists(path) && !pathExistsNoFollow(path) {
			// A baseline credential path is emitted for every run, so an absent
			// entry is the common case on a fresh machine — a third-party store
			// such as ~/.aws that Zero must not create. The read-all profile starts
			// from a read-only host-root bind where bubblewrap cannot create a
			// missing mount destination, and masking the nearest existing parent
			// could hide HOME, /tmp, or the workspace. Path-based backends
			// (seatbelt) still deny these paths before they exist. A dangling
			// symlink still exists as a pathname and must be masked so a later
			// retarget cannot reopen it.
			continue
		}
		unreadable = append(unreadable, path)
	}
	args = appendUnreadableLinuxPaths(args, unreadable, fs.DenyReadCarveouts, fs.WriteRoots)
	return linuxBwrapFilesystemPlan{
		Args:                   args,
		ProtectedCreateTargets: dedupeStrings(protectedCreateTargets),
	}
}

func linuxWriteRootsWithTemp(fs FileSystemPolicy) []WritableRoot {
	roots := append([]WritableRoot{}, fs.WriteRoots...)
	for _, tempRoot := range defaultTempWriteRoots() {
		found := false
		for _, root := range roots {
			if filepath.Clean(root.Root) == filepath.Clean(tempRoot) {
				found = true
				break
			}
		}
		if !found {
			roots = append(roots, WritableRoot{Root: tempRoot})
		}
	}
	return roots
}

func linuxSortedWriteRoots(roots []WritableRoot) []WritableRoot {
	sorted := append([]WritableRoot{}, roots...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return pathDepth(sorted[i].Root) < pathDepth(sorted[j].Root)
	})
	return sorted
}

func pathDepth(path string) int {
	cleaned := filepath.Clean(path)
	if cleaned == "" || filepath.Dir(cleaned) == cleaned {
		return 0
	}
	trimmed := strings.Trim(cleaned, string(filepath.Separator))
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, string(filepath.Separator)) + 1
}

func linuxProfileHasFullReadRoot(fs FileSystemPolicy) bool {
	for _, root := range fs.ReadRoots {
		if filepath.Clean(root) == string(filepath.Separator) {
			return true
		}
	}
	return false
}

func linuxPlatformReadRoots() []string {
	candidates := []string{"/bin", "/sbin", "/usr", "/etc", "/lib", "/lib64", "/nix/store", "/run/current-system/sw"}
	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if pathExists(candidate) {
			roots = append(roots, candidate)
		}
	}
	return roots
}

func appendReadOnlyLinuxPathArgs(args []string, path string) []string {
	path = strings.TrimSpace(path)
	if path == "" {
		return args
	}
	if pathExists(path) {
		return append(args, "--ro-bind", path, path)
	}
	return append(args, "--perms", "555", "--tmpfs", path, "--remount-ro", path)
}

// appendUnreadableLinuxPaths emits bwrap args that hide the given deny paths.
// Directories keep the existing tmpfs mask. Regular files stay `--ro-bind
// /dev/null path`. Symlink dests cannot use that bind: mount(2) LOOKUP_FOLLOW
// would mask the current target (so a later retarget reopens a new credential)
// or ENOENT a dangling link. Instead mask the resolved regular-file target and,
// when the parent is a credential directory, tmpfs-overlay the parent omitting
// denied basenames so the lexical dentry disappears without following.
func appendUnreadableLinuxPaths(args []string, paths []string, carveouts []string, writeRoots []WritableRoot) []string {
	classified := classifyUnreadableLinuxPaths(paths)
	for _, dir := range classified.dirs {
		args = appendUnreadableLinuxDirArgs(args, dir, carveouts)
	}
	omits := linuxDeniedBasenamesByParent(classified.files, classified.links)
	seenParents := make(map[string]struct{})
	for _, link := range classified.links {
		args = appendUnreadableLinuxResolvedSymlinkArgs(args, link, carveouts)
		parent := filepath.Clean(filepath.Dir(link))
		overlayParent := linuxCanonicalDest(parent)
		if linuxParentOverlaid(seenParents, overlayParent) {
			continue
		}
		if !linuxCredentialParentSafeToTmpfs(overlayParent, writeRoots) && !linuxCredentialParentSafeToTmpfs(parent, writeRoots) {
			continue
		}
		var applied bool
		args, applied = appendLinuxParentTmpfsOmitting(args, overlayParent, omits[overlayParent])
		if applied {
			// Record every spelling of the parent only after the overlay is
			// actually added. macOS /var vs /private/var (and similar aliases)
			// must skip file binds using either form, otherwise --ro-bind
			// /dev/null and --tmpfs name different dests for the same directory.
			recordLinuxParentSpellings(seenParents, parent)
			recordLinuxParentSpellings(seenParents, overlayParent)
		}
	}
	for _, file := range classified.files {
		parent := filepath.Clean(filepath.Dir(file))
		if linuxParentOverlaid(seenParents, parent) {
			// Parent was already tmpfs-overlaid (symlink sibling in the same
			// credential dir). Re-binding /dev/null onto the regular file would
			// target a dest that no longer exists after the overlay and can
			// abort bubblewrap at startup.
			continue
		}
		args = append(args, "--ro-bind", "/dev/null", file)
	}
	return args
}

type linuxUnreadableClassified struct {
	files []string
	dirs  []string
	links []string
}

func classifyUnreadableLinuxPaths(paths []string) linuxUnreadableClassified {
	var out linuxUnreadableClassified
	seen := make(map[string]struct{}, len(paths))
	add := func(bucket *[]string, path string) {
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		*bucket = append(*bucket, path)
	}
	for _, path := range paths {
		lexical := normalizeProfilePathLexically(path)
		canonical := normalizeProfilePath(path)
		inspect := lexical
		if inspect == "" {
			inspect = canonical
		}
		if inspect == "" {
			continue
		}
		info, err := os.Lstat(inspect)
		if err != nil && canonical != "" && canonical != inspect {
			info, err = os.Lstat(canonical)
			inspect = canonical
		}
		if err != nil {
			continue
		}
		switch {
		case info.Mode().Type() == os.ModeSymlink:
			// Keep the lexical dentry so a later retarget still hits the dest.
			add(&out.links, inspect)
		case info.IsDir():
			dest := inspect
			if canonical != "" && !linuxNonPlatformSymlinkInPath(inspect) {
				dest = canonical
			}
			add(&out.dirs, dest)
		default:
			dest := inspect
			if canonical != "" && !linuxNonPlatformSymlinkInPath(inspect) {
				dest = canonical
			}
			add(&out.files, dest)
		}
	}
	return out
}

func linuxCanonicalDest(path string) string {
	path = filepath.Clean(path)
	if canonical := normalizeProfilePath(path); canonical != "" {
		return canonical
	}
	return path
}

// linuxNonPlatformSymlinkInPath reports a symlink in path's resolution other
// than host aliases such as macOS /var -> /private/var. Those aliases should
// use the canonical bwrap dest so overlay and file binds name the same place.
// A credential directory symlink (for example ~/.ssh -> a store) must keep the
// lexical dest so a later retarget is still denied.
func linuxNonPlatformSymlinkInPath(path string) bool {
	current := normalizeProfilePathLexically(path)
	if current == "" {
		current = filepath.Clean(path)
	}
	for {
		info, err := os.Lstat(current)
		if err == nil && info.Mode().Type() == os.ModeSymlink && !linuxPlatformPrefixSymlink(current) {
			return true
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false
		}
		current = parent
	}
}

func linuxPlatformPrefixSymlink(path string) bool {
	switch filepath.Clean(path) {
	case "/var", "/etc", "/tmp", "/private/var", "/private/etc", "/private/tmp":
		return true
	default:
		return false
	}
}

func linuxParentSpellings(parent string) []string {
	parent = filepath.Clean(parent)
	seen := make(map[string]struct{})
	var out []string
	add := func(path string) {
		path = filepath.Clean(strings.TrimSpace(path))
		if path == "" {
			return
		}
		if _, ok := seen[path]; ok {
			return
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	add(parent)
	add(normalizeProfilePathLexically(parent))
	add(normalizeProfilePath(parent))
	return out
}

func recordLinuxParentSpellings(seen map[string]struct{}, parent string) {
	for _, spelling := range linuxParentSpellings(parent) {
		seen[spelling] = struct{}{}
	}
}

func linuxParentOverlaid(seen map[string]struct{}, parent string) bool {
	for _, spelling := range linuxParentSpellings(parent) {
		if _, ok := seen[spelling]; ok {
			return true
		}
	}
	return false
}

func linuxDeniedBasenamesByParent(files, links []string) map[string]map[string]struct{} {
	out := make(map[string]map[string]struct{})
	add := func(path string) {
		parent := linuxCanonicalDest(filepath.Dir(path))
		base := filepath.Base(path)
		m, ok := out[parent]
		if !ok {
			m = make(map[string]struct{})
			out[parent] = m
		}
		m[base] = struct{}{}
	}
	for _, path := range files {
		add(path)
	}
	for _, path := range links {
		add(path)
	}
	return out
}

func appendUnreadableLinuxResolvedSymlinkArgs(args []string, path string, carveouts []string) []string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || resolved == "" {
		return args
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return args
	}
	if info.IsDir() {
		return appendUnreadableLinuxDirArgs(args, resolved, carveouts)
	}
	return append(args, "--ro-bind", "/dev/null", resolved)
}

func appendUnreadableLinuxDirArgs(args []string, path string, carveouts []string) []string {
	nested := nestedCarveoutPaths(path, carveouts)
	if len(nested) == 0 {
		return append(args, "--perms", "000", "--tmpfs", path, "--remount-ro", path)
	}
	// A carveout has to stay reachable, and traversing into a directory needs the
	// execute bit, so the mask is 111 (--x--x--x) instead of 000: the directory's
	// contents remain unlistable and unreadable, while an explicitly re-bound
	// subpath below it can still be resolved. The binds must precede the
	// --remount-ro, which is what freezes the tmpfs.
	args = append(args, "--perms", "111", "--tmpfs", path)
	for _, carveout := range nested {
		if info, err := os.Lstat(carveout); err == nil && info.IsDir() {
			args = append(args, "--ro-bind", carveout, carveout)
		}
	}
	return append(args, "--remount-ro", path)
}

// linuxCredentialParentSafeToTmpfs reports that parent may be reconstructed
// inside the sandbox to hide a lexical symlink dentry. HOME, `/`, `/tmp`,
// `/etc`, `/var`, and write roots must never be tmpfs-overlaid: reconstructing
// HOME is forbidden and would hide the workspace. Only credential directories
// such as ~/.ssh and ~/.gnupg (including nested dirs under them) qualify.
func linuxCredentialParentSafeToTmpfs(parent string, writeRoots []WritableRoot) bool {
	parent = filepath.Clean(parent)
	if parent == "" || parent == "." || parent == string(filepath.Separator) {
		return false
	}
	switch parent {
	case "/tmp", "/etc", "/var", "/usr", "/home", "/root", "/opt", "/dev", "/proc", "/sys", "/run", "/mnt", "/media":
		return false
	}
	if !linuxCredentialDirPath(parent) {
		return false
	}
	for _, wr := range writeRoots {
		root := filepath.Clean(strings.TrimSpace(wr.Root))
		if root != "" && parent == root {
			return false
		}
	}
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() {
		return false
	}
	return true
}

func linuxCredentialDirPath(path string) bool {
	base := filepath.Base(filepath.Clean(path))
	switch base {
	case ".ssh", ".gnupg", ".aws", ".azure":
		return true
	}
	slash := filepath.ToSlash(filepath.Clean(path))
	for _, marker := range []string{"/.ssh/", "/.gnupg/", "/.aws/", "/.azure/"} {
		if strings.Contains(slash, marker) {
			return true
		}
	}
	return false
}

func appendLinuxParentTmpfsOmitting(args []string, parent string, omit map[string]struct{}) ([]string, bool) {
	parent = filepath.Clean(parent)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return args, false
	}
	// 555 keeps option-2 public names (config, known_hosts, *.pub) listable
	// after the overlay; denied basenames are simply not rebound.
	args = append(args, "--perms", "555", "--tmpfs", parent)
	for _, entry := range entries {
		name := entry.Name()
		if name == "." || name == ".." {
			continue
		}
		if _, skip := omit[name]; skip {
			continue
		}
		sibling := filepath.Join(parent, name)
		if !pathExists(sibling) {
			// os.ReadDir returns dangling symlinks; bwrap --ro-bind sources
			// must resolve, so skip them rather than aborting sandbox startup.
			continue
		}
		args = append(args, "--ro-bind", sibling, sibling)
	}
	return append(args, "--remount-ro", parent), true
}

// nestedCarveoutPaths returns the carveouts that sit strictly inside root,
// shallowest first so a parent bind is created before a nested one.
func nestedCarveoutPaths(root string, carveouts []string) []string {
	if len(carveouts) == 0 {
		return nil
	}
	out := credentialCarveoutPaths([]string{root}, carveouts)
	sort.SliceStable(out, func(i, j int) bool { return pathDepth(out[i]) < pathDepth(out[j]) })
	return dedupeStrings(out)
}

// ensureLinuxDenyReadDirs creates trusted Zero-process directories a deny mask
// needs to exist for. Best effort: a failure leaves the path unmasked and never
// blocks the command.
func ensureLinuxDenyReadDirs(dirs []string) {
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || pathExists(dir) {
			continue
		}
		_ = os.MkdirAll(dir, 0o700)
	}
}

func shouldUnshareLinuxNetwork(policy NetworkPolicy) bool {
	return NormalizeNetworkMode(policy.Mode) == NetworkDeny
}

func linuxHelperSandboxEnvironment(profile PermissionProfile, base []string) []string {
	return upsertEnvList(base, linuxHelperSandboxEnvironmentOverrides(profile)...)
}

func linuxHelperSandboxEnvironmentOverrides(profile PermissionProfile) []string {
	return []string{
		EnvSandboxBackend + "=" + string(linuxSandboxBackendEnv),
		"ZERO_SANDBOX_NETWORK=" + string(profile.Network.Mode),
		EnvSandboxed + "=1",
	}
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func pathExistsNoFollow(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

func findLinuxSandboxHelperCommand() (LinuxSandboxHelperCommand, error) {
	if exe, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(exe), LinuxSandboxHelperName)
		if executableRegularFile(candidate) {
			return LinuxSandboxHelperCommand{Name: candidate}, nil
		}
	}
	if path, err := exec.LookPath(LinuxSandboxHelperName); err == nil && path != "" {
		return LinuxSandboxHelperCommand{Name: path}, nil
	}
	if root := linuxSandboxRepoRoot(); root != "" {
		mainPath := filepath.Join(root, "cmd", LinuxSandboxHelperName, "main.go")
		if _, err := os.Stat(mainPath); err == nil {
			if goPath, lookErr := exec.LookPath("go"); lookErr == nil && goPath != "" {
				return LinuxSandboxHelperCommand{
					Name:       goPath,
					ArgsPrefix: []string{"run", "./cmd/" + LinuxSandboxHelperName},
					Dir:        root,
				}, nil
			}
		}
	}
	return LinuxSandboxHelperCommand{}, errors.New("zero-linux-sandbox helper is not available")
}

func executableRegularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

func linuxSandboxRepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return ""
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return ""
	}
	return root
}
