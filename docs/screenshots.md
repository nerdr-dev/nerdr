# Screenshots and recordings for the docs

What to capture, and how, so new pictures match the ones already on the
site. Diagrams are not here: they are inline SVG in
`src/components/diagrams/` and follow the code, not a screen.

## The demo world

All the pictures show one made-up `~/develop`, so a reader can follow a
tab from page to page. Keep it.

- **Repositories:** `infra`, `shop-api`, `web` (all on GitHub as
  `acme/…`, with pull requests and CI).
- **Tabs in `shop-api`:**
  1. `rate-limit` (claude): per-client token bucket, `+98-4`, reviewed by
     codex, walkthrough written.
  2. `fix-cart-rounding` (claude): float kroner to integer øre; asks to
     run `go test` (red light).
  3. `checkout-api` (codex): working (yellow).
  4. `json-errors` `#46` with failing CI (`✗`).
  5. `validate-product-ids`, merged.
  6. `pagination` `#47` approved (`✓`), three tasks in F3 (`✉3`).
- **Other machines:** two servers, `box` (folder `~/src`) and `gpu`,
  for the pictures that show machines. Leave them out of the rest.
- **Agents:** claude and codex (`gpt-6.2-astra`), with gemini and grok
  installed too, so agent lists show the full set.

## How to capture

- The same terminal, font and theme as before: a dark theme, the
  window **3660×2052** pixels on a Retina screen (PNG), so the sidebar,
  agent and panel have the widths they have now.
- `layout`, `panel_width`, `tab_bar` and `panel_labels` at what the
  pictures show: the sidebar on the left; the panel labels on (F1
  changes, F2 github …) where a picture has them.
- Make sure the panel shows **F1–F6**. Every current picture but
  `split.png` has F1–F5 only, from before F6 came.
- Recordings: about one minute, MP4 (H.264) and WebM (VP9) of the same
  take, and for the README a GIF of `overview` at 1445×921.
- Save PNGs over the old ones in `src/assets/`, recordings in `public/`,
  with the same names, so the pages need no change.

## Take again

| File | Page | Shows | What changed |
|---|---|---|---|
| `sidebar.png` | `index.mdx` | `fix-cart-rounding` asking to run `go test`; F1 with the `cart.go` diff | F6 |
| `waiting.png` | `use/progress.mdx` | The waiting list (`ctrl+q w`) over the same screen | F6 |
| `review.png` | `panel/code-changes.mdx`, `use/code-review.mdx` | F1 of `rate-limit` in review mode: files in reading order, `config.go` | F6 |
| `findings.png` | `panel/code-changes.mdx` | F1 of `rate-limit`: `ratelimit.go` with the high finding and a why note | F6 |
| `agent-review.png` | `use/code-review.mdx` | Codex reviewing claude beside it; the "Review of rate-limit done" dialog | F6; the coloured `rev:` row above the agent |
| `review-sent.png` | `use/code-review.mdx` | The findings sent to claude, codex's pane beside | F6; the coloured `rev:` row |
| `my-prs.png` | `panel/github.mdx` | F2, my PRs, four pull requests in three repositories | F6 |
| `tasks.png` | `panel/tasks.mdx` | F3 of `pagination`, three tasks | F6 |
| `skills.png` | `panel/skills.mdx` | F4, `nerdr-handoff` off and picked | F6; check the list of skills is current |
| `split.png` | `use/agents-together.mdx` | The spinoff dialog, part 1 of 3, over `api-cleanup` | The dialog's new repository and machine rows |
| `walkthrough.png` | `panel/code-changes.mdx` | The walkthrough page of `rate-limit` in a browser, at 2880×2200 | Small changes to the page since; check it still looks this way |
| `overview.gif` / `.mp4` / `.webm` | `README.md`, `index.mdx`, `start/about.mdx` | A new tab with codex, switching repositories, F1, tests in a terminal, codex reviewing claude, the waiting list | F6; worth a tab with two repositories and a glimpse of a server |
| `plugins.mp4` / `.webm` | `panel/plugins.mdx` | `logwatch` in F5, a panic reaching claude, spinoff to a new tab, its pull request | F6; the spinoff dialog's new rows |
| `chess.mp4` / `.webm` | `use/agents-together.mdx` | Claude and codex play chess with `nerdr send` | F6 only: take it last, or leave it |

## New

In order of need.

| File | Page, where | Shows |
|---|---|---|
| `machines.png` | `use/remote.mdx`, under *The Machines dialog* | `ctrl+q k` with this machine, `box` connected and `gpu` grey with its reason; behind it the sidebar with the machines at the foot of the tabs |
| `move.mp4` / `.webm` | `use/remote.mdx`, under *Moving a tab to another machine* | `ctrl+q a m` on `rate-limit`: picking `box`, the confirm screen with what goes and what does not, the handoff, and the tab coming up on `box` with its changes and conversation. About 40 seconds |
| `new-tab.png` | `use/tabs.mdx`, under *Making a tab* | The new-tab dialog with the machines above the repositories, `shop-api` and `web` both picked, a branch name and the agent list |
| `workbench.png` | `use/workbench.mdx`, under *Panes beside the agent* | One tab with claude, a terminal running `go test`, and `2:codex` beside, the status panel showing `last:`, `status:` and `note:` |
| `history.png` | `panel/history.mdx`, under *The F6 screen* | F6 with a few kinds of message, a long one picked and shown in full, such as a sandboxed agent not started with its paths |
