package sandbox

import "sync"

type commandPrefixGrantSet struct {
	mu     sync.Mutex
	grants []CommandPrefixGrant
}

type CommandPrefixGrant struct {
	ToolName string
	Prefix   []string
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
