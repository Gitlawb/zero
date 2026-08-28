package sandbox

import (
	"os"
	"path/filepath"
	"strings"
)

// sshConfigMaxIncludeDepth bounds Include recursion. Unreadable or cyclic
// includes are skipped rather than failing the profile build.
const sshConfigMaxIncludeDepth = 16

const sshConfigMaxBytes = 1 << 20

const sshIncludeMatchCap = 64

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
	entries, err := os.ReadDir(sshDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			path := filepath.Join(sshDir, name)
			if isSSHPrivateKeyFileName(name) || sshFileLooksLikePrivateKey(path) {
				candidates = append(candidates, path)
			}
		}
	}
	candidates = append(candidates, sshConfigReferencedPaths(home, sshDir)...)
	return candidates
}

func isSSHPrivateKeyFileName(name string) bool {
	if sshPublicOrConfigName(name) {
		return false
	}
	if strings.HasPrefix(name, "id_") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(name), ".pem")
}

func sshPublicOrConfigName(name string) bool {
	switch name {
	case "config", "known_hosts", "known_hosts.old", "authorized_keys", "authorized_keys2":
		return true
	}
	return strings.HasSuffix(name, ".pub")
}

func sshFileLooksLikePrivateKey(path string) bool {
	if sshPublicOrConfigName(filepath.Base(path)) {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	buf := make([]byte, 128)
	n, err := f.Read(buf)
	if n == 0 && err != nil {
		return false
	}
	s := strings.TrimSpace(string(buf[:n]))
	if !strings.HasPrefix(s, "-----BEGIN ") {
		return false
	}
	return strings.Contains(s, "PRIVATE KEY")
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

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if len(data) > sshConfigMaxBytes {
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
	if strings.Contains(value, "%") {
		return ""
	}
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
	return !sshPublicOrConfigName(filepath.Base(cleaned))
}
