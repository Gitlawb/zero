package zerogit

import (
	"strconv"
	"strings"
)

// DiffStat is the parsed summary line of a `git diff --stat` invocation. The
// summary line ("N files changed, A insertions(+), B deletions(-)") carries no
// secret-bearing tokens, so it is safe to parse from an already-redacted stat.
type DiffStat struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

// NetLOC reports insertions minus deletions. The value may be zero or negative
// when a change removes at least as many lines as it adds.
func (stat DiffStat) NetLOC() int {
	return stat.Insertions - stat.Deletions
}

// ParseDiffStat extracts file/insertion/deletion counts from the trailing
// summary line of a `git diff --stat` output, e.g.
// "3 files changed, 12 insertions(+), 4 deletions(-)". Missing insertion or
// deletion clauses default to zero, and any malformed input yields a zero-value
// DiffStat without panicking.
func ParseDiffStat(stat string) DiffStat {
	summary := ""
	for _, line := range strings.Split(stat, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "changed,") || strings.Contains(trimmed, "changed ") {
			if strings.Contains(trimmed, "file") {
				summary = trimmed
			}
		}
	}
	if summary == "" {
		return DiffStat{}
	}
	result := DiffStat{}
	for _, segment := range strings.Split(summary, ",") {
		segment = strings.TrimSpace(segment)
		fields := strings.Fields(segment)
		if len(fields) < 2 {
			continue
		}
		count, err := strconv.Atoi(fields[0])
		if err != nil {
			continue
		}
		switch {
		case strings.HasPrefix(fields[1], "file"):
			result.FilesChanged = count
		case strings.HasPrefix(fields[1], "insertion"):
			result.Insertions = count
		case strings.HasPrefix(fields[1], "deletion"):
			result.Deletions = count
		}
	}
	return result
}
