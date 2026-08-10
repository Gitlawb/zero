// Package memory is a durable, scoped note store that survives a session.
//
// WHAT IT IS FOR. A session ends and everything it worked out goes with it —
// the convention this repo actually follows, the decision behind a strange-
// looking guard, the finding an audit already confirmed. AGENTS.md holds what
// someone sat down and wrote; this holds what accumulates, and it is scoped so
// the two do not become one file nobody prunes.
//
// NOT A SECOND PLAN STORE. The "no new store" invariant is about PLAN STATE —
// "plan state is session events", ARCHITECTURE.md — because two stores for one
// fact eventually disagree. Notes are not derivable from any event log, so there
// is nothing here for a second store to contradict.
//
// THE SAFETY RULES ARE PLAN_STORE'S, deliberately reused rather than rewritten:
// an allow-list name that cannot spell a traversal component, containment of
// every operation to a handle on the workspace (internal/pathjail), and an
// O_EXCL temp file renamed into place. A note store is a write primitive pointed
// at a path the model chooses, which is the same shape as "save my plan" and
// needs the same answers.
package memory

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Gitlawb/zero/internal/pathjail"
)

// Scope is where a note lives, and who else sees it.
type Scope string

const (
	// ScopeProject is checked in beside the repo: shared with everyone who clones
	// it, and therefore reviewed like any other file in the tree.
	ScopeProject Scope = "project"
	// ScopeLocal is this machine only. The natural home for anything specific to
	// one checkout, one operator, or one afternoon.
	ScopeLocal Scope = "local"
)

// fileExt is the stored extension. Markdown with frontmatter, because a note is
// meant to be readable by the person whose repo it is sitting in.
const fileExt = ".md"

// tempExt is what an in-progress write carries. Deliberately not fileExt, so a
// temp file a crash left behind is never listed as a note.
const tempExt = ".tmp"

// maxNoteBytes bounds one note. Generous for prose, small enough that a runaway
// write cannot quietly fill a repo.
const maxNoteBytes = 64 << 10

// namePattern is an ALLOW-LIST, the same rule plan names use: enumerate what is
// permitted rather than forbidding traversal, because every deny-list in this
// repo has leaked at least once.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

var (
	ErrBadName  = errors.New("a memory name may use only letters, digits, hyphen and underscore, and be at most 64 characters")
	ErrNoStore  = errors.New("memory is not available in this run")
	ErrTooLarge = fmt.Errorf("a memory note may be at most %d bytes", maxNoteBytes)
	ErrNotFound = errors.New("no such memory")
	ErrBadScope = errors.New(`scope must be "project" or "local"`)
	// ErrIsSymlink is pathjail's refusal, kept under this package's own name so
	// callers testing for it keep working. It now covers a Windows junction as
	// well as a symlink, which the old ModeSymlink-only check did not.
	ErrIsSymlink = pathjail.ErrReparse
)

// Paths locates the two scopes. An empty directory means that scope is simply
// unavailable, and a write to it is refused with a reason rather than silently
// written somewhere else.
type Paths struct {
	// Root is the containment boundary. Every operation below is performed
	// relative to a handle on it, so no component of ProjectDir or LocalDir can
	// redirect a write or a delete outside the tree. Empty means no store: a
	// boundary is not optional, because without one the directories below are
	// just strings the filesystem re-resolves on every syscall.
	Root       string
	ProjectDir string
	LocalDir   string
}

// DefaultPaths puts project memory beside the repo and local memory under it, so
// one .gitignore line separates shared from private.
func DefaultPaths(workspaceRoot string) Paths {
	if strings.TrimSpace(workspaceRoot) == "" {
		return Paths{}
	}
	base := filepath.Join(workspaceRoot, ".zero", "memory")
	return Paths{Root: workspaceRoot, ProjectDir: base, LocalDir: filepath.Join(base, "local")}
}

func (paths Paths) dirFor(scope Scope) (string, error) {
	switch scope {
	case ScopeProject:
		if paths.ProjectDir == "" {
			return "", ErrNoStore
		}
		return paths.ProjectDir, nil
	case ScopeLocal:
		if paths.LocalDir == "" {
			return "", ErrNoStore
		}
		return paths.LocalDir, nil
	default:
		return "", ErrBadScope
	}
}

// openScope opens a handle on the containment root and returns the scope's
// store directory relative to it. The caller closes the handle.
//
// Every filesystem operation in this file goes through here. The store used to
// Lstat its own directory and file and then hand those same strings to
// MkdirAll, CreateTemp, Rename and Remove, which re-resolve every ancestor: a
// link anywhere above the store redirected the write, and the checks passed
// because they were looking at the wrong components. On Windows they also
// missed a junction outright, since a junction is a reparse point but not a
// symlink.
func (paths Paths) openScope(scope Scope) (*os.Root, string, error) {
	dir, err := paths.dirFor(scope)
	if err != nil {
		return nil, "", err
	}
	if strings.TrimSpace(paths.Root) == "" {
		return nil, "", ErrNoStore
	}
	return pathjail.Open(paths.Root, dir)
}

// Note is one stored memory.
type Note struct {
	Name string
	// Description is the one-line summary from frontmatter. It is what a listing
	// shows, so a reader can decide what to open WITHOUT reading everything —
	// which is the whole reason notes carry frontmatter at all.
	Description string
	Scope       Scope
	Body        string
}

// ValidName reports whether a name is storable.
func ValidName(name string) bool {
	return name != "" && len(name) <= 64 && namePattern.MatchString(name)
}

// List returns every note in both scopes, project first, each sorted by name.
//
// LOCAL SHADOWS NOTHING. Unlike saved plans, where project shadows user because
// a repo's own plan is what its contributors should get, both scopes are listed:
// they hold different KINDS of thing, and hiding one behind the other would lose
// a note rather than resolve a conflict.
func List(paths Paths) []Note {
	var out []Note
	for _, scope := range []Scope{ScopeProject, ScopeLocal} {
		handle, relative, err := paths.openScope(scope)
		if err != nil {
			continue
		}
		directory, err := handle.Open(relative)
		if err != nil {
			handle.Close()
			continue
		}
		entries, err := directory.ReadDir(-1)
		directory.Close()
		handle.Close()
		if err != nil {
			continue
		}
		var scoped []Note
		for _, entry := range entries {
			if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), fileExt) {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			note, err := Read(paths, scope, name)
			if err != nil {
				continue
			}
			scoped = append(scoped, note)
		}
		sort.Slice(scoped, func(i, j int) bool { return scoped[i].Name < scoped[j].Name })
		out = append(out, scoped...)
	}
	return out
}

// Read returns one note.
func Read(paths Paths, scope Scope, name string) (Note, error) {
	if !ValidName(name) {
		return Note{}, ErrBadName
	}
	handle, relative, err := paths.openScope(scope)
	if err != nil {
		return Note{}, err
	}
	defer handle.Close()
	// Reads are confined too. A note read through a link is an exfiltration
	// primitive in a tool the model can call by name, which is the same hole as
	// the write with the arrow reversed.
	body, err := handle.ReadFile(filepath.Join(relative, name+fileExt))
	if err != nil {
		if os.IsNotExist(err) {
			return Note{}, ErrNotFound
		}
		return Note{}, err
	}
	description, text := splitFrontmatter(string(body))
	return Note{Name: name, Description: description, Scope: scope, Body: text}, nil
}

// Write stores a note, replacing any note of the same name in the same scope.
//
// Every step runs against a handle on the containment root, so the directory
// this lands in cannot be changed underneath it: create the tree, refuse a link
// at the destination, write an O_EXCL temp file with an unpredictable name,
// rename into place. An edit is therefore atomic, and a crash mid-write leaves
// the previous note rather than a half-written one.
func Write(paths Paths, scope Scope, name, description, body string) (string, error) {
	if !ValidName(name) {
		return "", ErrBadName
	}
	dir, err := paths.dirFor(scope)
	if err != nil {
		return "", err
	}
	content := renderNote(name, description, body)
	if len(content) > maxNoteBytes {
		return "", ErrTooLarge
	}
	handle, relative, err := paths.openScope(scope)
	if err != nil {
		return "", err
	}
	defer handle.Close()
	if err := handle.MkdirAll(relative, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, name+fileExt)
	relativePath := filepath.Join(relative, name+fileExt)
	if err := pathjail.RefuseReparse(handle, relativePath); err != nil {
		return "", err
	}
	file, temp, err := pathjail.CreateTemp(handle, relative, name, tempExt)
	if err != nil {
		return "", fmt.Errorf("create a temporary file in %s: %w", dir, err)
	}
	writeErr := func() error {
		if _, err := file.WriteString(content); err != nil {
			return err
		}
		return file.Close()
	}()
	if writeErr != nil {
		_ = file.Close()
		_ = handle.Remove(temp)
		return "", fmt.Errorf("write %s: %w", path, writeErr)
	}
	if err := handle.Rename(temp, relativePath); err != nil {
		_ = handle.Remove(temp)
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	return path, nil
}

// Forget removes a note. Missing is not an error: the caller asked for it to be
// gone, and it is.
func Forget(paths Paths, scope Scope, name string) error {
	if !ValidName(name) {
		return ErrBadName
	}
	handle, relative, err := paths.openScope(scope)
	if err != nil {
		return err
	}
	defer handle.Close()
	// A delete is the sharpest of these: a write lands a file, a delete removes
	// somebody else's. Same handle, same reason.
	relativePath := filepath.Join(relative, name+fileExt)
	if err := pathjail.RefuseReparse(handle, relativePath); err != nil {
		return err
	}
	if err := handle.Remove(relativePath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func renderNote(name, description, body string) string {
	var b strings.Builder
	b.WriteString("---\nname: ")
	b.WriteString(name)
	if trimmed := strings.TrimSpace(description); trimmed != "" {
		b.WriteString("\ndescription: ")
		b.WriteString(singleLine(trimmed))
	}
	b.WriteString("\n---\n\n")
	b.WriteString(strings.TrimRight(body, "\n"))
	b.WriteString("\n")
	return b.String()
}

// splitFrontmatter returns the description and the body. A note without
// frontmatter is not an error — it is a file someone wrote by hand, and losing
// it because it lacks a header would be the store punishing the reader it exists
// to serve.
func splitFrontmatter(content string) (description string, body string) {
	if !strings.HasPrefix(content, "---\n") {
		return "", content
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return "", content
	}
	for _, line := range strings.Split(rest[:end], "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "description:"); ok {
			description = strings.TrimSpace(value)
		}
	}
	return description, strings.TrimLeft(rest[end+len("\n---\n"):], "\n")
}

func singleLine(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
