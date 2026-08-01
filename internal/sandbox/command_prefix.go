package sandbox

import (
	"strings"
	"sync"
)

type commandPrefixGrantSet struct {
	mu     sync.Mutex
	grants []CommandPrefixGrant
}

type CommandPrefixGrant struct {
	ToolName   string   `json:"toolName"`
	Prefix     []string `json:"prefix"`
	ApprovedAt string   `json:"approvedAt,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Session    bool     `json:"session,omitempty"`
}

type CommandPrefixInput struct {
	ToolName string
	Prefix   []string
	Reason   string
}

func newCommandPrefixGrantSet() *commandPrefixGrantSet {
	return &commandPrefixGrantSet{}
}

func (set *commandPrefixGrantSet) add(grant CommandPrefixGrant) {
	if set == nil || grant.ToolName == "" || len(grant.Prefix) == 0 {
		return
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	for _, existing := range set.grants {
		if existing.ToolName == grant.ToolName && sameStringSlice(existing.Prefix, grant.Prefix) {
			return
		}
	}
	grant.Prefix = append([]string(nil), grant.Prefix...)
	set.grants = append(set.grants, grant)
}

func (set *commandPrefixGrantSet) match(toolName string, command []string) (CommandPrefixGrant, bool) {
	if set == nil || toolName == "" || len(command) == 0 {
		return CommandPrefixGrant{}, false
	}
	set.mu.Lock()
	defer set.mu.Unlock()
	for _, grant := range set.grants {
		if grant.ToolName == toolName && hasStringPrefix(command, grant.Prefix) {
			grant.Prefix = append([]string(nil), grant.Prefix...)
			return grant, true
		}
	}
	return CommandPrefixGrant{}, false
}

func hasStringPrefix(values []string, prefix []string) bool {
	if len(prefix) == 0 || len(prefix) > len(values) {
		return false
	}
	for index := range prefix {
		if values[index] != prefix[index] {
			return false
		}
	}
	return true
}

func NormalizeCommandPrefix(prefix []string) ([]string, bool) {
	cleaned := make([]string, 0, len(prefix))
	for _, part := range prefix {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		cleaned = append(cleaned, part)
	}
	if unsafeCommandPrefix(cleaned) {
		return nil, false
	}
	return cleaned, true
}

func ValidCommandPrefix(prefix []string) bool {
	_, ok := NormalizeCommandPrefix(prefix)
	return ok
}

var bannedCommandPrefixSuggestions = [][]string{
	{"python3"},
	{"python3", "-"},
	{"python3", "-c"},
	{"python"},
	{"python", "-"},
	{"python", "-c"},
	{"py"},
	{"py", "-3"},
	{"pythonw"},
	{"pyw"},
	{"pypy"},
	{"pypy3"},
	{"git"},
	{"bash"},
	{"bash", "-lc"},
	{"sh"},
	{"sh", "-c"},
	{"sh", "-lc"},
	{"zsh"},
	{"zsh", "-lc"},
	{"/bin/zsh"},
	{"/bin/zsh", "-lc"},
	{"/bin/bash"},
	{"/bin/bash", "-lc"},
	{"pwsh"},
	{"pwsh", "-Command"},
	{"pwsh", "-c"},
	{"powershell"},
	{"powershell", "-Command"},
	{"powershell", "-c"},
	{"powershell.exe"},
	{"powershell.exe", "-Command"},
	{"powershell.exe", "-c"},
	{"env"},
	{"sudo"},
	{"node"},
	{"node", "-e"},
	{"perl"},
	{"perl", "-e"},
	{"ruby"},
	{"ruby", "-e"},
	{"php"},
	{"php", "-r"},
	{"lua"},
	{"lua", "-e"},
	{"osascript"},
}

func unsafeCommandPrefix(prefix []string) bool {
	if len(prefix) == 0 {
		return true
	}
	for _, part := range prefix {
		if unsafeCommandPrefixPart(part) {
			return true
		}
	}
	for _, banned := range bannedCommandPrefixSuggestions {
		if sameStringSlice(prefix, banned) {
			return true
		}
	}
	return unsafeCommandPrefixLauncher(prefix[0])
}

func unsafeCommandPrefixPart(part string) bool {
	part = strings.TrimSpace(part)
	return part == "" ||
		strings.ContainsAny(part, "\x00\r\n*?[]{}") ||
		strings.Contains(part, "$(") ||
		strings.Contains(part, "`")
}

func unsafeCommandPrefixLauncher(program string) bool {
	program = strings.ToLower(strings.TrimSpace(program))
	if strings.ContainsAny(program, `/\`) {
		return true
	}
	switch program {
	case "bash", "sh", "zsh", "/bin/bash", "/bin/zsh",
		"pwsh", "powershell", "powershell.exe",
		"env", "sudo", "doas", "su", "run0", "osascript",
		"command", "eval", "exec", "time",
		"find", "xargs", "timeout", "nice", "nohup", "watch", "setsid", "stdbuf", "ionice",
		"ssh", "make", "npm", "npx",
		"python", "python3", "py", "pythonw", "pyw", "pypy", "pypy3",
		"node", "perl", "ruby", "php", "lua", "deno", "bun":
		return true
	default:
		return false
	}
}

func sameStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
