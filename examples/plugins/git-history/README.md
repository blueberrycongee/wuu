# Git History

Shows the recent commits of the repository behind the active Wuu workspace.

The plugin adds a branch button to the workspace tool rail and a `Git history`
entry to the workspace page picker. The branch button appears only when the
active workspace is inside a Git repository with at least one commit. The page
picker entry is always available and shows a quiet empty state outside a
repository. Opening the entry shows a colored Git graph with branch lanes,
commit nodes, and merge connections alongside the subject, hash, author,
relative time, and refs. It reads up to 50 commits across refs (excluding stashes) in topological
order, with the repository name and current branch in the header. Lines at the
bottom continue into older history outside the loaded window. The page picker
shows only the title. The open graph refreshes when the workspace changes,
when the window regains focus, and every 30 seconds. The rail only checks once
per workspace change.

The desktop view gets the active workspace through `api.listWorkspaces()`, so it
does not depend on the application provider. The runtime provides a
`git.history` v1 service that runs a read-only `git log` with `-C`. It keeps the
host's Git wrapper active by supplying `WUU_INTERNAL_GIT_ORIGINAL_PATH` from a
PATH without the wrapper directory; plugin processes do not inherit that
variable directly. The optional `ui.application` declaration lets the rail open
the view without blocking a mixed plugin's service graph before the renderer
owns the application provider.

Non-repository workspaces and repositories without commits produce no rail
button. Opening the view from the page picker in such a workspace shows a quiet
empty state.

## Try it

```sh
WUU_HOME=/path/to/wuu-home wuu plugin dev examples/plugins/git-history/git-history.ts
```

The plugin is a single TypeScript file. Editing it publishes a development
generation; activate it in a conversation after the host reaches an idle
boundary. No build step or side-car files are required.
