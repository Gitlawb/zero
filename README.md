# Origin session history

This branch is written automatically by [Origin](https://getorigin.io). It is
**not part of your codebase** — nothing here is built, imported, or deployed. It
records which AI agent wrote the code on the other branches, and the prompt
behind each change.

You don't need Origin installed to read any of it. Plain `git` is enough.

## Layout

    sessions/<session-id>/metadata.json   model, cost, tokens, duration, commits
    sessions/<session-id>/prompts.md      the prompts, in order
    sessions/<session-id>/changes.json    per-prompt diffs and files touched

## Reading it

List the sessions:

    git ls-tree --name-only origin/origin-sessions:sessions/

Read the prompts behind one:

    git show origin/origin-sessions:sessions/<session-id>/prompts.md

## Per-commit attribution (one extra step)

Origin also writes a git note per commit, so `git log` can show you which agent
wrote it and why. Notes live in `refs/notes/origin`, and **`git clone` does not
fetch them** — that's a git default, not an Origin choice. Bring them down once:

    git fetch origin refs/notes/origin:refs/notes/origin

Then, with plain git:

    git log --show-notes=origin

To keep them arriving on every ordinary `git pull`, add the refspec:

    git config --add remote.origin.fetch '+refs/notes/origin:refs/notes/origin-remote'

(That stages them into `refs/notes/origin-remote`. Origin merges the staging ref
into `refs/notes/origin` so your own local notes are never overwritten — mapping
the remote straight onto `refs/notes/origin` with a forced refspec would silently
destroy any note you hadn't pushed yet.)
