package sandbox

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// sshConfigMaxIncludeDepth bounds Include recursion. Cycles terminate normally;
// unreadable inputs and exceeded limits make the profile refuse execution.
const sshConfigMaxIncludeDepth = 16

const sshConfigMaxBytes = 1 << 20

const sshIncludeMatchCap = 64

// Page directory reads so busy SSH directories do not require one large
// allocation or become incomplete merely because they contain many entries.
const sshPrivateKeyWalkPageSize = 256

const sshPrivateKeySniffBytes = 128

// sshSymlinkMaxDirs and sshSymlinkMaxEntries bound traversal of directory
// trees reached through symlinks inside ~/.ssh. Real ~/.ssh directory trees
// remain unbounded.
const sshSymlinkMaxDirs = 64

const sshSymlinkMaxEntries = 1024

// sshWellKnownPrivateKeyNames are the OpenSSH default private-key basenames.
// They are emitted even when ~/.ssh is absent so pathname-policy backends can
// reserve them; mount-based Linux must refuse unprotected future key paths.
var sshWellKnownPrivateKeyNames = []string{
	"id_rsa",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"id_ecdsa_sk",
	"id_ed25519_sk",
}

// sshKeyMaterialDirectives are directives whose values name key material:
// denied unless an explicit exemption applies.
var sshKeyMaterialDirectives = map[string]bool{
	"certificatefile": true,
	"identityfile":    true,
	"revokedhostkeys": true,
}

// sshSupportDirectives are directives whose values name support files and sockets:
// denied only when content-sniffing identifies a private key payload, so custom
// known-hosts names, control sockets, and agent sockets keep working.
var sshSupportDirectives = map[string]bool{
	"controlpath":          true,
	"globalknownhostsfile": true,
	"identityagent":        true,
	"userknownhostsfile":   true,
}

// sshPrivateKeyDenyCandidates returns deny-read candidates for SSH private key
// material under home. ~/.ssh itself is not denied: config, known_hosts, and
// *.pub stay readable so git host resolution still works. Keys named outside
// ~/.ssh are discovered by parsing ~/.ssh/config (and Include) for IdentityFile
// and the other path-valued directives.
type sshDiscovery struct {
	errors     []string
	env        []string
	workingDir string
}

func (s *sshDiscovery) fail(path, reason string) {
	s.errors = append(s.errors, fmt.Sprintf("SSH discovery incomplete for %s: %s", path, reason))
}

func (s *sshDiscovery) privateKeyDenyCandidates(home string) []string {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	sshDir := filepath.Join(home, ".ssh")
	var candidates []string
	for _, name := range sshWellKnownPrivateKeyNames {
		candidates = append(candidates, filepath.Join(sshDir, name))
	}
	candidates = append(candidates, s.walkPrivateKeyFiles(sshDir)...)
	candidates = append(candidates, s.collectConfigPaths(filepath.Join(sshDir, "config"), home, sshDir, make(map[string]bool), 0)...)
	return candidates
}

var testSSHWalkChildHook func(dir string)

func (s *sshDiscovery) walkPrivateKeyFiles(sshDir string) []string {
	type pendingDir struct {
		path       string
		viaSymlink bool
	}

	var out []string
	visitedDirs := make(map[string]bool)
	pending := []pendingDir{{path: sshDir, viaSymlink: false}}
	var symlinkDirsVisited int
	var symlinkEntriesSeen int

	walk := func(item pendingDir) {
		dir := item.path
		realDir := dir
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			realDir = resolved
		}
		if visitedDirs[realDir] {
			return
		}
		visitedDirs[realDir] = true

		if item.viaSymlink {
			symlinkDirsVisited++
			if symlinkDirsVisited > sshSymlinkMaxDirs {
				s.fail(dir, "symlink directory limit exceeded")
				return
			}
		}

		root, err := os.OpenRoot(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				s.fail(dir, err.Error())
			}
			return
		}
		defer root.Close()
		rootStat, err := root.Stat(".")
		if err != nil {
			s.fail(dir, err.Error())
			return
		}
		d, err := root.Open(".")
		if err != nil {
			s.fail(dir, err.Error())
			return
		}
		defer d.Close()
		for {
			entries, err := d.ReadDir(sshPrivateKeyWalkPageSize)
			if err != nil && err != io.EOF {
				s.fail(dir, err.Error())
				return
			}
			if item.viaSymlink {
				symlinkEntriesSeen += len(entries)
				if symlinkEntriesSeen > sshSymlinkMaxEntries {
					s.fail(dir, "symlink entry limit exceeded")
					return
				}
			}
			if testSSHWalkChildHook != nil {
				testSSHWalkChildHook(dir)
			}
			// Verify directory identity has not changed since opening root.
			dirStat, statErr := os.Stat(dir)
			if statErr != nil || !os.SameFile(dirStat, rootStat) {
				s.fail(dir, "directory identity changed during inspection")
				return
			}
			for _, entry := range entries {
				name := entry.Name()
				if name == "." || name == ".." {
					continue
				}
				info, err := root.Lstat(name)
				if err != nil {
					s.fail(filepath.Join(dir, name), err.Error())
					continue
				}
				path := filepath.Join(dir, name)
				mode := info.Mode()
				if mode.Type() == os.ModeSymlink {
					target, err := root.Readlink(name)
					if err != nil {
						s.fail(path, err.Error())
						continue
					}
					targetPath := target
					if !filepath.IsAbs(targetPath) {
						targetPath = filepath.Join(dir, target)
					}
					targetStat, err := os.Stat(targetPath)
					if err == nil && targetStat.IsDir() {
						pending = append(pending, pendingDir{path: targetPath, viaSymlink: true})
						continue
					}
					// Inspect leaf symlinks (bounded, specials rejected) so a
					// custom-named link to a PEM/OpenSSH key is still denied.
					if isSSHPrivateKeyFileName(name) || isSSHPrivateKeyFileName(filepath.Base(targetPath)) || s.fileLooksLikePrivateKey(targetPath) {
						out = append(out, path)
						if targetPath != path {
							out = append(out, targetPath)
						}
					}
					continue
				}
				if info.IsDir() {
					pending = append(pending, pendingDir{path: path, viaSymlink: item.viaSymlink})
					continue
				}
				if !mode.IsRegular() {
					continue
				}
				if isSSHPrivateKeyFileName(name) || s.rootFileLooksLikePrivateKey(root, name, path) {
					out = append(out, path)
				}
			}
			if err == io.EOF {
				return
			}
		}
	}
	// Iteration keeps open directory handles and call-stack depth constant even
	// for deeply nested layouts. The physical-path set still breaks link cycles.
	for len(pending) > 0 {
		item := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		walk(item)
	}
	return out
}

func isSSHPrivateKeyFileName(name string) bool {
	if sshPublicOrConfigName(name) {
		return false
	}
	if strings.HasPrefix(name, "id_") {
		return true
	}
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".pem") || strings.HasSuffix(lower, ".ppk")
}

func sshPublicOrConfigName(name string) bool {
	switch name {
	case "config", "authorized_keys", "authorized_keys2":
		return true
	}
	if strings.HasSuffix(name, ".pub") {
		return true
	}
	return sshKnownHostsFamilyName(name)
}

// sshKnownHostsFamilyName reports the supported OpenSSH known-hosts filenames
// that must stay readable so git host resolution still works. Arbitrary
// known_hosts.* / ssh_known_hosts.* names are not included: a private key
// named known_hosts.private must still be detected. /dev/null is exempted in
// sshShouldDenyReferencedPath, not here (its basename is "null").
func sshKnownHostsFamilyName(name string) bool {
	switch name {
	case "known_hosts", "known_hosts2", "known_hosts.old",
		"ssh_known_hosts", "ssh_known_hosts2":
		return true
	}
	return false
}

func (s *sshDiscovery) rootFileLooksLikePrivateKey(root *os.Root, name, path string) bool {
	f, err := root.Open(name)
	if err != nil {
		s.fail(path, err.Error())
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := io.ReadAll(io.LimitReader(f, sshPrivateKeySniffBytes))
	if err != nil {
		s.fail(path, err.Error())
		return false
	}
	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "PuTTY-User-Key-File") {
		return true
	}
	if !strings.HasPrefix(content, "-----BEGIN ") {
		return false
	}
	return strings.Contains(content, "PRIVATE KEY")
}

func (s *sshDiscovery) fileLooksLikePrivateKey(path string) bool {
	// Always sniff. IdentityFile ~/keys/config (or authorized_keys / *.pub /
	// known_hosts) can hold a PEM/OpenSSH/PuTTY private-key payload and must
	// not stay readable. Real config, authorized_keys, public keys, and
	// known-hosts files do not match these headers, so name-only exemptions
	// in sshShouldDenyReferencedPath still keep genuine support files readable.
	data, ok := readRegularFileBounded(path, sshPrivateKeySniffBytes)
	if !ok {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			s.fail(path, "cannot inspect potential private key")
		} else if err != nil && !os.IsNotExist(err) {
			s.fail(path, err.Error())
		}
		return false
	}
	content := strings.TrimSpace(string(data))
	if strings.HasPrefix(content, "PuTTY-User-Key-File") {
		return true
	}
	if !strings.HasPrefix(content, "-----BEGIN ") {
		return false
	}
	return strings.Contains(content, "PRIVATE KEY")
}

// readRegularFileBounded reads only from an inspected regular-file descriptor.
// SSH paths intentionally may reference files outside ~/.ssh; this is file-type
// validation, not a claim that path resolution is contained inside that tree.
func readRegularFileBounded(path string, maxBytes int) ([]byte, bool) {
	if maxBytes <= 0 {
		return nil, false
	}
	f, err := openSSHInspectionFile(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, false
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
	if err != nil {
		return nil, false
	}
	return data, true
}

func (s *sshDiscovery) collectConfigPaths(path, home, sshDir string, seen map[string]bool, depth int) []string {
	if depth > sshConfigMaxIncludeDepth {
		s.fail(path, "config Include depth limit exceeded")
		return nil
	}
	identity := sshConfigIdentity(path)
	if identity == "" || seen[identity] {
		return nil
	}
	seen[identity] = true

	data, ok := readRegularFileBounded(path, sshConfigMaxBytes+1)
	if !ok {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			s.fail(path, "cannot read regular config file")
		}
		return nil
	}
	if len(data) > sshConfigMaxBytes {
		s.fail(path, "config size limit exceeded")
		data = data[:sshConfigMaxBytes]
	}

	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		key, values := parseSSHDirective(line)
		if key == "" || len(values) == 0 {
			continue
		}
		if key == "include" {
			for _, pattern := range values {
				for _, include := range s.includePaths(pattern, home, sshDir) {
					out = append(out, s.collectConfigPaths(include, home, sshDir, seen, depth+1)...)
				}
			}
			continue
		}
		keyMaterial := sshKeyMaterialDirectives[key]
		if !keyMaterial && !sshSupportDirectives[key] {
			continue
		}
		for _, raw := range values {
			expanded := expandSSHConfigPath(raw, home, s.effectiveWorkingDir(sshDir), s.env...)
			if expanded == "" {
				continue
			}
			if !keyMaterial {
				if s.fileLooksLikePrivateKey(expanded) {
					out = append(out, expanded)
				}
				continue
			}
			if !s.shouldDenyReferencedPath(expanded, home, sshDir) {
				continue
			}
			out = append(out, expanded)
		}
	}
	return out
}

func (s *sshDiscovery) effectiveWorkingDir(fallback string) string {
	if strings.TrimSpace(s.workingDir) != "" {
		return s.workingDir
	}
	return fallback
}

func sshConfigIdentity(path string) string {
	if n := normalizeProfilePath(path); n != "" {
		return n
	}
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == "" {
		return ""
	}
	return cleaned
}

var testSSHIncludeGlobHook func(dir string) error

func (s *sshDiscovery) includePaths(pattern, home, sshDir string) []string {
	expanded := expandSSHConfigPath(pattern, home, sshDir, s.env...)
	if expanded == "" {
		return nil
	}
	matches := s.globIncludePaths(expanded)
	if len(matches) == 0 {
		return nil
	}
	if len(matches) > sshIncludeMatchCap {
		s.fail(expanded, "config Include match limit exceeded")
		return nil
	}
	return matches
}

func hasGlobMagic(path string) bool {
	return strings.ContainsAny(path, "*?[]")
}

func cleanGlobDir(dir string) string {
	if dir == "" {
		return "."
	}
	cleaned := filepath.Clean(dir)
	if cleaned == "" {
		return "."
	}
	return cleaned
}

func (s *sshDiscovery) globIncludePaths(pattern string) []string {
	if !hasGlobMagic(pattern) {
		if testSSHIncludeGlobHook != nil {
			if err := testSSHIncludeGlobHook(pattern); err != nil {
				s.fail(pattern, err.Error())
				return nil
			}
		}
		info, err := os.Lstat(pattern)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			s.fail(pattern, err.Error())
			return nil
		}
		if info.IsDir() {
			return nil
		}
		return []string{pattern}
	}

	dir, file := filepath.Split(pattern)
	dir = cleanGlobDir(dir)

	var parentDirs []string
	if hasGlobMagic(dir) {
		parentDirs = s.globIncludePaths(dir)
	} else {
		parentDirs = []string{dir}
	}

	var matches []string
	for _, parent := range parentDirs {
		if testSSHIncludeGlobHook != nil {
			if err := testSSHIncludeGlobHook(parent); err != nil {
				s.fail(parent, err.Error())
				continue
			}
		}
		fi, err := os.Stat(parent)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			s.fail(parent, err.Error())
			continue
		}
		if !fi.IsDir() {
			continue
		}
		f, err := os.Open(parent)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			s.fail(parent, err.Error())
			continue
		}
		names, err := f.Readdirnames(-1)
		_ = f.Close()
		if err != nil && err != io.EOF {
			s.fail(parent, err.Error())
			continue
		}
		for _, name := range names {
			matched, err := filepath.Match(file, name)
			if err != nil {
				s.fail(pattern, err.Error())
				return nil
			}
			if matched {
				matches = append(matches, filepath.Join(parent, name))
			}
		}
	}
	return matches
}

func parseSSHDirective(line string) (string, []string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", nil
	}
	tokens := splitSSHTokens(line)
	if len(tokens) == 0 {
		return "", nil
	}
	first := tokens[0]
	rest := tokens[1:]
	if i := strings.IndexByte(first, '='); i > 0 {
		val := first[i+1:]
		first = first[:i]
		if val != "" {
			rest = append([]string{val}, rest...)
		}
	} else if len(rest) > 0 {
		if rest[0] == "=" {
			rest = rest[1:]
		} else if strings.HasPrefix(rest[0], "=") {
			rest[0] = rest[0][1:]
			if rest[0] == "" {
				rest = rest[1:]
			}
		}
	}
	key := strings.ToLower(first)
	if key == "" || len(rest) == 0 {
		return "", nil
	}
	return key, rest
}

func splitSSHTokens(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := byte(0)
	flush := func() {
		if cur.Len() == 0 {
			return
		}
		out = append(out, cur.String())
		cur.Reset()
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
				continue
			}
			if c == '\\' && inQuote == '"' && i+1 < len(s) {
				cur.WriteByte(s[i+1])
				i++
				continue
			}
			cur.WriteByte(c)
			continue
		}
		if c == '\\' && i+1 < len(s) {
			cur.WriteByte(s[i+1])
			i++
			continue
		}
		switch c {
		case '\'', '"':
			inQuote = c
		case ' ', '\t':
			flush()
		case '#':
			if cur.Len() == 0 {
				return out
			}
			cur.WriteByte(c)
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

func expandSSHConfigPath(value, home, sshDir string, env ...string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") || strings.EqualFold(value, "SSH_AUTH_SOCK") {
		return ""
	}
	// OpenSSH expands environment variables in IdentityFile. ${HOME}/$HOME
	// resolves to the supplied home argument. Other variables resolve from the
	// supplied environment, falling back to the process environment. Unset or
	// invalid $VAR drops the path, like an unsupported token, so discovery never
	// follows an unresolved pattern.
	expandedEnv, ok := expandSSHConfigPathEnv(value, home, env...)
	if !ok {
		return ""
	}
	expanded, ok := expandSSHConfigPathTokens(expandedEnv, home)
	if !ok {
		return ""
	}
	value = expanded
	switch {
	case value == "~":
		return filepath.Clean(home)
	case strings.HasPrefix(value, "~/"):
		return filepath.Join(home, value[2:])
	case strings.HasPrefix(value, "~"):
		return ""
	case filepath.IsAbs(value):
		return filepath.Clean(value)
	default:
		return filepath.Join(sshDir, value)
	}
}

// expandSSHConfigPathEnv resolves ${VAR} and $VAR. ${HOME} and $HOME resolve
// to the supplied home argument. Other variables prefer the supplied environment
// over the inherited process environment.
// An undefined variable, dangling $, or malformed ${...} drops the path.
func expandSSHConfigPathEnv(value, home string, env ...string) (string, bool) {
	if !strings.Contains(value, "$") {
		return value, true
	}
	var b strings.Builder
	b.Grow(len(value) + len(home))
	for i := 0; i < len(value); i++ {
		if value[i] != '$' {
			b.WriteByte(value[i])
			continue
		}
		if i+1 >= len(value) {
			return "", false
		}
		var name string
		if value[i+1] == '{' {
			end := strings.IndexByte(value[i+2:], '}')
			if end < 0 {
				return "", false
			}
			name = value[i+2 : i+2+end]
			i += 2 + end
		} else {
			if !sshEnvVarStart(value[i+1]) {
				return "", false
			}
			j := i + 1
			for j < len(value) && sshEnvVarChar(value[j]) {
				j++
			}
			name = value[i+1 : j]
			i = j - 1
		}
		if name == "HOME" {
			b.WriteString(home)
		} else {
			val, ok := sshDiscoveryEnvValue(env, name)
			if !ok {
				return "", false
			}
			b.WriteString(val)
		}
	}
	return b.String(), true
}

// Command overrides use last-entry precedence, including an explicitly empty
// value. Only a missing override falls back to the inherited environment.
func sshDiscoveryEnvValue(env []string, key string) (string, bool) {
	for i := len(env) - 1; i >= 0; i-- {
		name, value, ok := strings.Cut(env[i], "=")
		if ok && name == key {
			return value, true
		}
	}
	return os.LookupEnv(key)
}

func sshEnvVarStart(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
}

func sshEnvVarChar(c byte) bool {
	return sshEnvVarStart(c) || (c >= '0' && c <= '9')
}

// expandSSHConfigPathTokens resolves OpenSSH path tokens we can expand without
// a live connection: %d is the supplied local home, %% is a literal %. Any
// remaining percent token (%h, a trailing %, ...) is unsupported and the path
// is dropped so we never deny (or follow) an unresolved pattern.
func expandSSHConfigPathTokens(value, home string) (string, bool) {
	if !strings.Contains(value, "%") {
		return value, true
	}
	var b strings.Builder
	b.Grow(len(value) + len(home))
	for i := 0; i < len(value); i++ {
		if value[i] != '%' {
			b.WriteByte(value[i])
			continue
		}
		if i+1 >= len(value) {
			return "", false
		}
		switch value[i+1] {
		case '%':
			b.WriteByte('%')
		case 'd':
			b.WriteString(home)
		default:
			return "", false
		}
		i++
	}
	return b.String(), true
}

func sshShouldDenyReferencedPath(path, home, sshDir string) bool {
	return (&sshDiscovery{}).shouldDenyReferencedPath(path, home, sshDir)
}

func (s *sshDiscovery) shouldDenyReferencedPath(path, home, sshDir string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	cleaned := filepath.Clean(path)
	if cleaned == string(filepath.Separator) {
		return false
	}
	if home != "" && cleaned == filepath.Clean(home) {
		return false
	}
	if sshDir != "" && cleaned == filepath.Clean(sshDir) {
		return false
	}
	if sshIsDevNullPath(cleaned) {
		return false
	}
	// Sniff before the public-name exemption so IdentityFile ~/keys/work.pub
	// (or a relocated key named config / authorized_keys / known_hosts) with a
	// private-key payload is denied. Genuine public keys, genuine known-hosts,
	// config, and authorized_keys do not match and stay readable.
	if s.fileLooksLikePrivateKey(cleaned) {
		return true
	}
	return !sshPublicOrConfigName(filepath.Base(cleaned))
}

// sshIsDevNullPath reports UserKnownHostsFile /dev/null (and the host equivalent
// os.DevNull). The basename of that path is "null", which is not a known-hosts
// name; denying it would install a Seatbelt deny file-read* on /dev/null.
func sshIsDevNullPath(path string) bool {
	cleaned := filepath.Clean(path)
	if cleaned == os.DevNull || strings.EqualFold(cleaned, os.DevNull) {
		return true
	}
	return filepath.ToSlash(cleaned) == "/dev/null"
}
