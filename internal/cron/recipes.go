package cron

// RecipeDef is a named preset job (cron expression + prompt).
type RecipeDef struct {
	ID     string
	Expr   string
	Prompt string
}

var recipes = []RecipeDef{
	{ID: "git-recap", Expr: "*/30 * * * *", Prompt: "Summarize the git commits since your last run; note anything risky."},
	{ID: "ci-watch", Expr: "*/15 * * * *", Prompt: "Check CI status for the current branch and report failures with the failing step."},
	{ID: "todo-pulse", Expr: "0 * * * *", Prompt: "Scan the working tree for new TODO/FIXME markers added recently and list them."},
	{ID: "daily-summary", Expr: "0 9 * * *", Prompt: "Write a short progress summary of what changed in this repo over the last day."},
}

// Recipes returns the built-in preset recipes.
func Recipes() []RecipeDef { return append([]RecipeDef(nil), recipes...) }

// Recipe returns the preset with the given id.
func Recipe(id string) (RecipeDef, bool) {
	for _, r := range recipes {
		if r.ID == id {
			return r, true
		}
	}
	return RecipeDef{}, false
}
