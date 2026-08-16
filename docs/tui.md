# Terminal UI

```
todo tui
```

A list on the left, a detail pane on the right, and forms to add and edit. Press `w` to toggle
between the backlog and the wiki; the same keys serve both. A task's detail shows the docs it links
to, its relations by type (blocked by, blocks), its comment thread, and the commits recorded against
it; a doc's detail shows the tasks that point at it. `Tab` moves focus into the pane, where long
tasks scroll and every reference — a tag, a related task, a doc — is a link `n`/`p` walk and `Enter`
follows.

The status line carries only the common keys; `?` opens the full reference below. It is truncated to
the terminal width, so it never wraps.

## Keys

### List

- `↑` / `↓` — move
- `Enter` / `e` — edit
- `a` — add
- `d` — delete (to trash)
- `c` — record a commit
- `m` — add a comment
- `l` — link a doc
- `/` — full-text search
- `f` — filter by tag
- `p` — filter by epic
- `s` — cycle status
- `t` — trash view
- `ctrl+r` — refresh (pull in tasks added elsewhere)
- `1`–`4` — sort by that column (repeat flips direction, `0` restores the store's order)
- `v` — full-screen view
- `Tab` — focus the detail
- `w` — backlog ⇄ wiki
- `x` — export the current view to markdown (`backlog.md` / `wiki.md`)
- `?` — the full key help
- `q` — quit

### Detail (`Tab`)

- `↑` / `↓` — scroll
- `n` / `p` — walk links
- `Enter` — follow
- `Tab` / `Esc` — back

### Modals

- Any modal closes on `Esc` (cancel) as well as `ctrl+c`.

### Trash (`t`)

- `r` — restore the selected task

## Filters

Epic and tags are independent axes, both applied together with the status filter. Filtering by a
parent epic (e.g. `todo-mcp`) includes its nested sub-epics (`todo-mcp/graphify`) — see
[the model](model.md#epics--and-nested-epics). If a filtered list comes up empty because the tasks
are in a status the status filter hides (an all-done project viewed as open), the status line says
so — press `s` to change it.

## Export

`x` writes the current view to a markdown file in the working directory: the backlog (the current
filter's tasks) to `backlog.md`, or the wiki (its pages) to `wiki.md`. It is the interactive twin of
`todo render`.
