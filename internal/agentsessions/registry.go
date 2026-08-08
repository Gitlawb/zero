package agentsessions

import (
	"errors"
	"strings"

	"github.com/Gitlawb/zero/internal/sessions"
)

// Adapters returns every adapter in a stable order.
//
// Adding an agent is meant to be this line plus one file. The order is the
// order results are listed in when two sessions share a timestamp, so it stays
// deterministic rather than depending on map iteration.
func Adapters(env Env) []Adapter {
	return []Adapter{
		ClaudeCode(env),
		FactoryDroid(env),
		Pi(env),
		Codex(env),
	}
}

// DiscoverAll indexes every adapter's store and returns the sessions belonging
// to cwd, most recently updated first. An empty cwd means every session.
//
// One adapter failing never denies the user the others: these are undocumented
// formats owned by other products, and one of them changing shape must not take
// discovery down with it. Errors are returned alongside the results so a caller
// can mention them without withholding what did work.
func DiscoverAll(env Env, cwd string) ([]ForeignSession, []error) {
	found := []ForeignSession{}
	problems := []error{}
	for _, adapter := range Adapters(env) {
		discovered, err := adapter.Discover(cwd)
		if err != nil {
			problems = append(problems, errors.New(adapter.Name()+": "+err.Error()))
			continue
		}
		found = append(found, discovered...)
	}
	sortByRecency(found)
	return found, problems
}

// ParseRef splits an "<agent>:<id>" reference and resolves the adapter.
//
// The agent prefix is required rather than inferred. Two agents can hold
// sessions with the same id — they are all uuids — and guessing which one the
// user meant would silently import the wrong conversation.
func ParseRef(env Env, ref string) (Adapter, string, error) {
	trimmed := strings.TrimSpace(ref)
	name, id, found := strings.Cut(trimmed, ":")
	if !found {
		return nil, "", errors.New("expected <agent>:<id>, for example claude-code:" +
			"3f2a1b4c-... — run `zero sessions discover` to list them")
	}
	name = strings.TrimSpace(name)
	id = strings.TrimSpace(id)
	if name == "" || id == "" {
		return nil, "", errors.New("both an agent and a session id are required, as <agent>:<id>")
	}
	for _, adapter := range Adapters(env) {
		if strings.EqualFold(adapter.Name(), name) {
			return adapter, id, nil
		}
	}
	return nil, "", errors.New("unknown agent " + name + "; known agents: " + strings.Join(AdapterNames(env), ", "))
}

// AdapterNames lists the agents this build can read.
func AdapterNames(env Env) []string {
	names := []string{}
	for _, adapter := range Adapters(env) {
		names = append(names, adapter.Name())
	}
	return names
}

// importTagPrefix marks a Zero session as a copy of another agent's transcript.
const importTagPrefix = "imported:"

// ImportTag is the provenance stamp an imported session carries:
// "imported:<agent>:<foreign session id>".
//
// The foreign id is part of the tag, not a second metadata field, so there is
// exactly one record of where a session came from (repo invariant #5 — two
// places holding the same fact will drift). It is what lets a caller tell an
// already-imported session apart from one still only on the other agent's disk,
// which the /resume picker needs in order not to list both.
func ImportTag(agent string, sourceID string) string {
	return importTagPrefix + agent + ":" + sourceID
}

// ParseImportTag splits an import tag back into its agent and source id.
// Reports false for a tag that is not an import stamp, including the older
// two-part "imported:<agent>" form, which records no source id to return.
func ParseImportTag(tag string) (agent string, sourceID string, ok bool) {
	rest := strings.TrimPrefix(strings.TrimSpace(tag), importTagPrefix)
	if rest == strings.TrimSpace(tag) {
		return "", "", false
	}
	agent, sourceID, found := strings.Cut(rest, ":")
	if !found || agent == "" || sourceID == "" {
		return "", "", false
	}
	return agent, sourceID, true
}

// ImportedAgent is the agent a session was imported from, or "" for a session
// Zero produced itself. Unlike ParseImportTag this accepts the older
// "imported:<agent>" form, so sessions imported before the tag carried a source
// id still group under the right agent.
func ImportedAgent(tag string) string {
	rest := strings.TrimSpace(tag)
	trimmed := strings.TrimPrefix(rest, importTagPrefix)
	if trimmed == rest {
		return ""
	}
	agent, _, _ := strings.Cut(trimmed, ":")
	return strings.TrimSpace(agent)
}

// ImportResult reports what an import produced.
type ImportResult struct {
	Session sessions.Metadata
	Events  int
	Source  ForeignSession
}

// Import copies one foreign session into the Zero session store and returns the
// new Zero session.
//
// The store assigns the id rather than deriving one from the foreign id. An
// import is a SNAPSHOT of another agent's transcript at a moment in time, and
// that session may well be continued in its own tool afterwards; a second
// import is therefore a legitimate second snapshot, not a mistake to be refused.
// Provenance lives in the tag ("imported:claude-code") and in the title.
//
// Nothing is written to the foreign store at any point.
func Import(store *sessions.Store, adapter Adapter, id string, options ReadOptions) (ImportResult, error) {
	source, err := describe(adapter, id)
	if err != nil {
		return ImportResult{}, err
	}
	if strings.TrimSpace(options.Cwd) == "" {
		options.Cwd = source.Cwd
	}
	events, err := adapter.Read(id, options)
	if err != nil {
		return ImportResult{}, err
	}

	created, err := store.Create(sessions.CreateInput{
		Title:   source.Title,
		Cwd:     source.Cwd,
		ModelID: source.ModelID,
		Tag:     ImportTag(adapter.Name(), id),
	})
	if err != nil {
		return ImportResult{}, err
	}
	if len(events) > 0 {
		if _, err := store.AppendEvents(created.SessionID, events); err != nil {
			return ImportResult{}, err
		}
	}
	return ImportResult{Session: created, Events: len(events), Source: source}, nil
}

// describe finds the index entry for an id so the imported session inherits the
// title, cwd and model. Discovery is unfiltered here because the session being
// imported need not belong to the current directory — importing a session from
// another workspace is a reasonable thing to want.
func describe(adapter Adapter, id string) (ForeignSession, error) {
	found, err := adapter.Discover("")
	if err != nil {
		return ForeignSession{}, err
	}
	for _, session := range found {
		if session.ID == id {
			return session, nil
		}
	}
	return ForeignSession{}, errors.New("no " + adapter.Name() + " session with id " + id +
		" — run `zero sessions discover --all` to list them")
}
