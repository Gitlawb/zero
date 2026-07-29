package specialist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Named plans: a plan that ran once, saved and run again.
//
// A plan is stored as the ARGUMENTS ParsePlan accepts, never as a serialised
// Plan, and it is re-admitted through ParsePlan on load. That is what keeps the
// "no path from stored data to an executable that skipped validation" property:
// a saved plan is re-validated against the CURRENT run's limits, so a plan saved
// when the tier was large is refused on a run where it is small, and a plan
// naming a tool this run does not hold is refused rather than granted.
//
// TWO SCOPES, project shadowing user, mirroring usercommands and the specialist
// loader. A project plan is checked into the repo and shared with the team.
//
// HOW ONE IS RUN, and this is a deliberate divergence from the gap report. The
// report wants saved plans to become `/name` slash commands. They do not: a
// plan is not a prompt, and giving each saved plan its own command would need a
// path from the TUI straight into tool execution — a SECOND way to run a plan,
// beside the model calling orchestrate. Instead the tool takes a `saved`
// argument. One execution path, one set of guards, and the model can compose a
// saved plan into a turn the way it composes anything else.
//
// MALFORMED IS AN ERROR, NEVER A SILENT SKIP (invariant 13). A plan file that
// does not parse is reported by name; dropping it would leave a user believing
// they had run something.

// planFileExt is the stored extension. JSON, not a script: the argument shape is
// already data, and the whole feature exists partly because a script language
// would have needed an evaluator.
const planFileExt = ".json"

// PlanPaths are the directories scanned for saved plans, project first.
type PlanPaths struct {
	ProjectDir string
	UserDir    string
}

// DefaultPlanPaths returns the project and user plan directories.
func DefaultPlanPaths(workspaceRoot, userConfigDir string) PlanPaths {
	var paths PlanPaths
	if strings.TrimSpace(workspaceRoot) != "" {
		paths.ProjectDir = filepath.Join(workspaceRoot, ".zero", "plans")
	}
	if strings.TrimSpace(userConfigDir) != "" {
		paths.UserDir = filepath.Join(userConfigDir, "zero", "plans")
	}
	return paths
}

// SavedPlan is a stored plan as found on disk. Args is the raw argument map,
// not a Plan: it becomes a Plan only by going through ParsePlan.
type SavedPlan struct {
	Name        string
	Description string
	TaskCount   int
	Path        string
	Project     bool
	Args        map[string]any
}

// validPlanName is an ALLOW-LIST, and it is the path guard as well as the name
// guard: no separator, no dot, no traversal component can be spelled with these
// characters, so "../../etc/passwd" is refused by the same rule that refuses a
// space. A deny-list of dangerous sequences is the pattern that has leaked
// repeatedly in this repo.
func validPlanName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	return planIDPattern.MatchString(name)
}

// SavePlan writes a validated plan under dir as name.json.
//
// It REFUSES to follow a symlink, on the directory and on the file. A saved
// plan is written into a repo-checked-in location, and a `.zero/plans/x.json`
// symlinked at ~/.ssh/config would otherwise make "save my plan" a file
// overwrite primitive.
func SavePlan(dir, name string, plan Plan) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("no directory to save plans in")
	}
	if !validPlanName(name) {
		return "", fmt.Errorf("plan name %q must use only letters, digits, hyphen and underscore, and be at most 64 characters", name)
	}
	if err := refuseSymlink(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	path := filepath.Join(dir, name+planFileExt)
	if err := refuseSymlink(path); err != nil {
		return "", err
	}
	body, err := json.MarshalIndent(plan.Args(), "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode plan: %w", err)
	}
	// Write-then-rename, so a crash mid-write leaves the previous plan intact
	// rather than a truncated file that fails to parse on the next run.
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(body, '\n'), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	return path, nil
}

// refuseSymlink reports an error when path exists and is a symlink. Lstat, not
// Stat: Stat follows the link and would report the target's kind, which is the
// whole thing being guarded against.
func refuseSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		// Does not exist yet is fine; anything else is reported rather than
		// assumed safe.
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; refusing to write through it", path)
	}
	return nil
}

// LoadPlans reads every saved plan under the given paths, project shadowing
// user. It returns the plans sorted by name and, separately, the files it could
// not read — malformed data is reported, never silently skipped.
func LoadPlans(paths PlanPaths) (plans []SavedPlan, problems []string) {
	byName := map[string]SavedPlan{}
	// User first so a project plan of the same name overwrites it.
	for _, dir := range []string{paths.UserDir, paths.ProjectDir} {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		found, bad := loadPlanDir(dir, dir == paths.ProjectDir)
		problems = append(problems, bad...)
		for _, plan := range found {
			byName[plan.Name] = plan
		}
	}
	for _, plan := range byName {
		plans = append(plans, plan)
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].Name < plans[j].Name })
	sort.Strings(problems)
	return plans, problems
}

func loadPlanDir(dir string, project bool) (plans []SavedPlan, problems []string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A missing directory is the ordinary case, not a problem worth naming.
		return nil, nil
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), planFileExt) {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
		path := filepath.Join(dir, entry.Name())
		if !validPlanName(name) {
			problems = append(problems, fmt.Sprintf("%s: name is not a valid plan name", path))
			continue
		}
		if err := refuseSymlink(path); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		var args map[string]any
		if err := json.Unmarshal(raw, &args); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v", path, err))
			continue
		}
		plans = append(plans, SavedPlan{
			Name:        name,
			Description: planString(args, "description"),
			TaskCount:   savedTaskCount(args),
			Path:        path,
			Project:     project,
			Args:        args,
		})
	}
	return plans, problems
}

// savedTaskCount is for LISTING only — a length for a display line, before any
// validation has happened. The authoritative count is Plan.TaskCount, after
// ParsePlan; nothing decides anything from this number.
func savedTaskCount(args map[string]any) int {
	list, _ := args["tasks"].([]any)
	return len(list)
}

// FindSavedPlan returns the named plan, with project shadowing user.
func FindSavedPlan(paths PlanPaths, name string) (SavedPlan, error) {
	if !validPlanName(name) {
		return SavedPlan{}, fmt.Errorf("plan name %q must use only letters, digits, hyphen and underscore", name)
	}
	plans, problems := LoadPlans(paths)
	for _, plan := range plans {
		if plan.Name == name {
			return plan, nil
		}
	}
	if len(problems) > 0 {
		// Say that something was unreadable. "No plan named x" while x sits on
		// disk unparseable is a lie by omission.
		return SavedPlan{}, fmt.Errorf("no saved plan named %q; some plan files could not be read: %s",
			name, strings.Join(problems, "; "))
	}
	return SavedPlan{}, fmt.Errorf("no saved plan named %q", name)
}
