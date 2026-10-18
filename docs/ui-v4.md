# UI v4 — less noise, fewer clicks

Round four of the Web UI. Goal: same sand-light look and the same home layout,
but calmer, denser where it matters, and readable at 1024×655 (the smallest
window we support).

Screenshots aren't in the repo. Locally they live in
`tidebot-shots/ui-v4/{before,after}/` (`1024-*` and `1440-*` for every page),
with side-by-side sheets in `contact-sheet-1024.png` / `contact-sheet-1440.png`.

## What was wrong (audit of every page at 1024×655 and 1440×900)

- **Sidebar too wide.** 240px at 1024 left the content ~780px, so the tables cut columns
  and the home quick-buttons truncated to "Dai…", "Sy…".
- **Tasks / runs tables** lost their last columns at 1024 (today's count, duration).
- **The breadcrumb repeated the h1** on top-level pages ("任务 / 任务").
- **Subtitles everywhere**, some of them stale (spend page still talked about
  `--max-budget-usd`). The runs toolbar had a whole line just for the timezone.
- **Spend "花在哪了"**: the header wrapped and the segmented control broke because
  two `.seg` rules fought each other.
- **Provider drawer**: helper text under every field, 4 footer buttons (测试连接 /
  删除 / 保存 / 保存并测试), and price source + date inline next to the numbers.
- **Type scale**: 16 different font sizes and a lot of 700 weight. Headings shouted.
- **Contrast**: `--text-3` was #8a90a0 on sand (3.76:1, fails AA). `--muted`,
  `--bad` and `--card` were used but never defined.
- **Too many primaries**: the plugin wall had a blue button on every card, the
  plugin header had three buttons, and task detail had 3 rows of chips.
- **Common flows took too many clicks**: running a task meant opening it first;
  deleting a button had a confirm dialog but no undo; no shortcut to add a provider
  or import a skill.

## Principles (and where they come from)

| Principle | Source |
|---|---|
| One control height (36, small 28), `text-sm` medium for buttons, two-ring focus | shadcn/ui `button.tsx` (h-9 / sm h-8, `focus-visible:ring-[3px]`), `input.tsx` h-9 |
| Table head 40px, quiet head text, row actions only on hover | shadcn/ui `table.tsx`, Plane list rows |
| Spacing on a 4/8 grid: 4 8 12 16 24 32 | n8n `_primitives.scss` spacing scale; Cherry Studio `tokens/spacing.css` |
| Few type sizes: 12 13 14 16 20 24, weights 400/500/600 | n8n `_primitives.scss` font sizes; Cherry Studio `tokens/typography.css` |
| Soft borders, two-layer shadows, crisp 1px | Vercel web-interface-guidelines (layered shadows, crisp borders); dify `themes/manual-light.css` |
| Inline help first, tooltips for secondary info, short delay | Vercel guidelines (tooltip timing; "inline help first, tooltips last resort"); Open WebUI `Tooltip.svelte` |
| Every destructive action: do it, then offer undo | Vercel guidelines (optimistic updates, undo for destructive actions) |
| Keyboard everywhere: `g x` to go, `n x` to create, ⌘K palette with key hints | Plane Power-K (`power-k/config/creation/command.ts` keySequence "ni"/"np", `navigation/commands.ts` "gh"/"gp"); cmdk README; Raycast |
| Tabular numbers for money and counts; status shown by color *and* text | Vercel guidelines (tabular-nums, redundant status cues) |
| Ghost/secondary by default, one primary per view | lobe-ui `Button/style.ts` variants; Linear / Plane headers |

## What changed

**Tokens and base** (`styles.css`, "ui-v4" layer at the end)
- `--fs-*`, `--sp-*`, `--ctl-h`/`--ctl-h-sm` tokens. All sizes normalized to
  12/13/14/16/20/24 and 700 → 600.
- AA colors: `--text-3` #646c7e (≈4.8:1 on the sand background), `--ok` #1c7a43, `--warn` #9a6000. Defined
  the missing `--muted`, `--bad`, `--card`.
- Softer border `#ebe6dc`, two-layer shadows, radius 10 (cards) / 6 (controls),
  two-ring focus.
- At ≤1279px: sidebar 200px, optional table columns (`.c-opt`) hide, home
  quick-buttons wrap instead of truncating.

**Components**
- Buttons, inputs, select, segmented (one `.seg` definition), tabs, filter chips,
  table, pills/tags, stats, empty states, drawer, modal, toast, palette now share
  the same heights, radius and spacing.
- CSS-only `[data-tip]` tooltips and a small `details.pop-info` popover (CSP-safe, no
  inline styles). A `tip()` helper in app.js replaces most `<small>` helper text.

**Pages**
- Top bar: breadcrumb only shows when there's more than one level.
- Page headers: title + short count/subtitle on the left, the one primary action on
  the right (新建任务, 快速运行, 新建按钮, …).
- Tasks: name is a link, the description is clamped to one line, a ▷ run-now button
  on hover per row, and batch pause/resume can be undone.
- Runs: the timezone moved into the subtitle, rerun is an icon with a tooltip, title is a link.
- Task detail: the status pill sits next to the title, one meta line instead of three
  chip rows, delete is an icon button.
- Buttons: add lives in the header, delete is immediate with undo.
- Plugins: one primary per view, card install buttons are secondary.
- Settings: no page subtitle; FX / budget hints are tooltips; new **价格目录** page.
- Provider drawer: hints are ⓘ tooltips, footer is 删除 (text) · 保存 · 保存并测试.
  The price section shows only the numbers plus a pill (官方 / 自定义 / 未设置). Source
  link, fetch date, the fallback estimate and the unit note are in the ⓘ popover.
  Restoring official prices can be undone.

**Keyboard**
- `g h/t/r/s/b/p/c/,` go to a page; `n t` new task, `n q` quick run, `n s` import
  skill, `n p` add provider, `n b` new button; `r` / `e` run / edit on task detail.
- The palette lists the create commands with their key hints.

## Not done / next

- No dark theme in this round.
- The tables still aren't virtualized. That's fine at the current sizes.
