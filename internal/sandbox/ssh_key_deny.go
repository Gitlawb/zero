package sandbox

import (
	"io"
	"os"
	"path/filepath"
	"strings"
)

// sshConfigMaxIncludeDepth bounds Include recursion. Unreadable or cyclic
// includes are skipped rather than failing the profile build.
const sshConfigMaxIncludeDepth = 16

const sshConfigMaxBytes = 1 << 20

const sshIncludeMatchCap = 64

// sshPrivateKeyWalkMaxDepth bounds recursive discovery under ~/.ssh. Nested
// directories such as ~/.ssh/keys are walked; directory symlinks are not
// followed, so a cycle cannot hang profile construction.
const sshPrivateKeyWalkMaxDepth = 8

// sshPrivateKeyWalkMaxEntries is a per-directory cap on entries considered
// under ~/.ssh. Extra entries in one directory (a large known_hosts.d, for
// example) are skipped; walking continues in sibling and parent directories
// so a private key elsewhere is still discovered. It is not a process-wide
// abort that unwinds the whole tree.
const sshPrivateKeyWalkMaxEntries = 256

const sshPrivateKeySniffBytes = 128

// sshWellKnownPrivateKeyNames are the OpenSSH default private-key basenames.
// They are emitted even when ~/.ssh is absent so pathname-policy backends can
// reserve them; mount-based Linux still masks only paths that exist.
var sshWellKnownPrivateKeyNames = []string{
	"id_rsa",
	"id_dsa",
	"id_ecdsa",
	"id_ed25519",
	"id_ecdsa_sk",
	"id_ed25519_sk",
}

// sshPathValuedDirectives are ssh_config keywords whose values name files or
// sockets. IdentityFile is the important one for relocated keys; the rest are
// collected so a CertificateFile or RevokedHostKeys path outside ~/.ssh is not
// left readable. UserKnownHostsFile / GlobalKnownHostsFile values that resolve
// to known_hosts are dropped later so option 2 keeps host resolution working.
var sshPathValuedDirectives = map[string]bool{
	"certificatefile":      true,
	"controlpath":          true,
	"globalknownhostsfile": true,
	"identityagent":        true,
	"identityfile":         true,
	"revokedhostkeys":      true,
	"userknownhostsfile":   true,
}

// sshPrivateKeyDenyCandidates returns deny-read candidates for SSH private key
// material under home. ~/.ssh itself is not denied: config, known_hosts, and
// *.pub stay readable so git host resolution still works. Keys named outside
// ~/.ssh are discovered by parsing ~/.ssh/config (and Include) for IdentityFile
// and the other path-valued directives.
func sshPrivateKeyDenyCandidates(home string) []string {
	home = strings.TrimSpace(home)
	if home == "" {
		return nil
	}
	sshDir := filepath.Join(home, ".ssh")
	var candidates []string
	for _, name := range sshWellKnownPrivateKeyNames {
		candidates = append(candidates, filepath.Join(sshDir, name))
	}
	candidates = append(candidates, walkSSHPrivateKeyFiles(sshDir)...)
	candidates = append(candidates, sshConfigReferencedPaths(home, sshDir)...)
	return candidates
}

func walkSSHPrivateKeyFiles(sshDir string) []string {
	var out []string
	visitedDirs := make(map[string]bool)
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if depth > sshPrivateKeyWalkMaxDepth {
			return
		}
		realDir := dir
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			realDir = resolved
		}
		if visitedDirs[realDir] {
			return
		}
		visitedDirs[realDir] = true

		d, err := os.Open(dir)
		if err != nil {
			return
		}
		// Bound allocation to the per-directory cap. os.ReadDir would load the
		// whole directory first. Overflow of one dir must not abort siblings.
		entries, err := d.ReadDir(sshPrivateKeyWalkMaxEntries)
		_ = d.Close()
		if err != nil && err != io.EOF {
			return
		}
		n := 0
		for _, entry := range entries {
			if n >= sshPrivateKeyWalkMaxEntries {
				// Skip the rest of this directory only; sibling dirs still walk.
				break
			}
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}
			path := filepath.Join(dir, name)
			n++
			info, err := os.Lstat(path)
			if err != nil {
				continue
			}
			mode := info.Mode()
			if mode.Type() == os.ModeSymlink {
				targetStat, err := os.Stat(path)
				if err == nil && targetStat.IsDir() {
					walk(path, depth+1)
					continue
				}
				// Inspect leaf symlinks (bounded, specials rejected) so a
				// custom-named link to a PEM/OpenSSH key is still denied.
				if isSSHPrivateKeyFileName(name) || sshFileLooksLikePrivateKey(path) {
					out = append(out, path)
				}
				continue
			}
			if info.IsDir() {
				walk(path, depth+1)
				continue
			}
			if !mode.IsRegular() {
				continue
			}
			if isSSHPrivateKeyFileName(name) || sshFileLooksLikePrivateKey(path) {
				out = append(out, path)
			}
		}
	}
	walk(sshDir, 0)
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

func sshFileLooksLikePrivateKey(path string) bool {
	// Always sniff. IdentityFile ~/keys/config (or authorized_keys / *.pub /
	// known_hosts) can hold a PEM/OpenSSH/PuTTY private-key payload and must
	// not stay readable. Real config, authorized_keys, public keys, and
	// known-hosts files do not match these headers, so name-only exemptions
	// in sshShouldDenyReferencedPath still keep genuine support files readable.
	data, ok := readRegularFileBounded(path, sshPrivateKeySniffBytes)
	if !ok {
		return false
	}
	s := strings.TrimSpace(string(data))
	if strings.HasPrefix(s, "PuTTY-User-Key-File") {
		return true
	}
	if !strings.HasPrefix(s, "-----BEGIN ") {
		return false
	}
	return strings.Contains(s, "PRIVATE KEY")
}

// readRegularFileBounded Lstats first and refuses FIFOs, devices, and
// sockets so profile construction cannot block on a special file. Regular-file
// symlinks are followed: OpenSSH reads ~/.ssh/config and Include targets
// through them, so a relocated IdentityFile would otherwise stay readable.
// The resolved path is Lstat'd again and opened (bounded LimitReader) so a
// FIFO or device behind the link is never opened.
func readRegularFileBounded(path string, maxBytes int) ([]byte, bool) {
	if maxBytes <= 0 {
		return nil, false
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, false
	}
	readPath := path
	if info.Mode().Type() == os.ModeSymlink {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, false
		}
		info, err = os.Lstat(resolved)
		if err != nil {
			return nil, false
		}
		readPath = resolved
	}
	if !info.Mode().IsRegular() {
		return nil, false
	}
	f, err := os.Open(readPath)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(maxBytes)))
	if err != nil {
		return nil, false
	}
	return data, true
}

func sshConfigReferencedPaths(home, sshDir string) []string {
	return collectSSHConfigPaths(filepath.Join(sshDir, "config"), home, sshDir, make(map[string]bool), 0)
}

func collectSSHConfigPaths(path, home, sshDir string, seen map[string]bool, depth int) []string {
	if depth > sshConfigMaxIncludeDepth {
		return nil
	}
	identity := sshConfigIdentity(path)
	if identity == "" || seen[identity] {
		return nil
	}
	seen[identity] = true

	data, ok := readRegularFileBounded(path, sshConfigMaxBytes)
	if !ok {
		return nil
	}

	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		key, values := parseSSHDirective(line)
		if key == "" || len(values) == 0 {
			continue
		}
		if key == "include" {
			for _, pattern := range values {
				for _, include := range sshIncludePaths(pattern, home, sshDir) {
					out = append(out, collectSSHConfigPaths(include, home, sshDir, seen, depth+1)...)
				}
			}
			continue
		}
		if !sshPathValuedDirectives[key] {
			continue
		}
		for _, raw := range values {
			expanded := expandSSHConfigPath(raw, home, sshDir)
			if !sshShouldDenyReferencedPath(expanded, home, sshDir) {
				continue
			}
			out = append(out, expanded)
		}
	}
	return out
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

func sshIncludePaths(pattern, home, sshDir string) []string {
	expanded := expandSSHConfigPath(pattern, home, sshDir)
	if expanded == "" {
		return nil
	}
	matches, err := filepath.Glob(expanded)
	if err != nil || len(matches) == 0 {
		return nil
	}
	if len(matches) > sshIncludeMatchCap {
		matches = matches[:sshIncludeMatchCap]
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
		rest = append([]string{first[i+1:]}, rest...)
		first = first[:i]
		if rest[0] == "" {
			rest = rest[1:]
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
			flush()
			return out
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

func expandSSHConfigPath(value, home, sshDir string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.EqualFold(value, "none") || strings.EqualFold(value, "SSH_AUTH_SOCK") {
		return ""
	}
	// OpenSSH expands environment variables in IdentityFile. ${HOME}/$HOME
	// resolves to the supplied home argument. Other variables resolve from the
	// process environment. Unset or invalid $VAR is treated like an unsupported
	// token: drop the path so we never deny or follow an unresolved pattern.
	expandedEnv, ok := expandSSHConfigPathEnv(value, home)
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
// to the supplied home argument. Other variables resolve from the environment.
// An undefined variable, dangling $, or malformed ${...} drops the path.
func expandSSHConfigPathEnv(value, home string) (string, bool) {
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
			val := os.Getenv(name)
			if val == "" {
				return "", false
			}
			b.WriteString(val)
		}
	}
	return b.String(), true
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
	if sshFileLooksLikePrivateKey(cleaned) {
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
