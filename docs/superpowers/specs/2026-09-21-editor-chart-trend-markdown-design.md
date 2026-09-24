# Editor Chart, Trend, and Markdown — design spec

Date: 2026-09-21  
Status: **approved.** Plan: `docs/superpowers/plans/2026-09-21-editor-chart-trend-markdown.md`.  
Scope: create/edit plus menu and the same blocks on preview + published posts. **Frontend only** (`local/the_monkeys`, branch `codex/group-scoped-blogs`). Engine, ES, gateway, and publish APIs unchanged.

Hard constraints (locked): **backward compatible**, **mobile-first**.

---

## 1. Product (locked)

Writers already have Chart and Trend in the plus menu. Those tools **are implemented** and already persist as EditorJS JSON. They fail as product: the graphic sits under a long form, and there is no how-to.

This project does two things:

1. Make Chart and Trend **visible and usable** on a phone first, then desktop, without changing saved JSON.
2. Add a **Markdown** block to the same plus menu: type/paste Markdown, or upload a `.md` file **into that block**. It does not convert the rest of the article.

| Tool | Today | After this work |
| --- | --- | --- |
| Chart (`type: 'chart'`) | Form first, preview last, no instructions | Preview first, one-line how-to, collapsed “Edit data” |
| Trend (`type: 'trend'`) | Form first, tiny sparkline, jargon badges | Sparkline + plain-language summary first, how-to, collapsed “Edit data” |
| Markdown (`type: 'markdown'`) | Missing | New block: write/paste + optional `.md` upload |
| Header, paragraph, list, code, formula, citation, methodology, dataset, embed, image, table | Unchanged | Unchanged |

Code stays a syntax highlighter. It is **not** Markdown.

---

## 2. Backward compatibility (locked)

Existing published and draft EditorJS documents must load, save, and render after this change.

- Keep block type names `chart` and `trend`. Do not rename, split, or migrate stored blocks.
- Keep `ChartBlockData` and `TrendBlockData` field names and types. Do not add **required** fields. Optional UI-only state (CSV textarea, “edit data open”) is **not** saved.
- `normalizeData` still fills missing fields from the current defaults (Chart: Jan–Mar line sample; Trend: 100, 118, 121). Posts with partial data keep working.
- Do not change Chart/Trend default sample values in a way that rewrites old posts on open. Defaults apply only when the field is missing or invalid.
- Markdown is **additive**. Old clients and old documents have no `markdown` block. Feed/search/SEO that ignore unknown block types stay valid.
- Engine stores EditorJS JSON as an opaque body. No proto, migration, or ES mapping change.
- Posts that never use Markdown look and save the same as today.
- Allowed tiny sanitizer fix: Chart `showLegend` is currently `false` in EditorJS `sanitize` (the field is stripped on save; `normalizeData` then defaults it to `true`). Change that key to `true` so a writer’s “hide legend” choice actually persists. Posts that never had the field still default to `true`.

Do **not** convert Markdown into header/paragraph/list blocks. Chart, Trend, Formula, and other JSON tools would not survive a whole-document Markdown round-trip.

---

## 3. Mobile-first (locked)

Design and default CSS for a **~360px** wide editor (create/edit on a phone). Desktop is the enhancement (`sm:` and up), not the baseline.

- **Graphic before form.** On insert, the writer sees the chart or trend without scrolling past eight fields.
- **One column** for edit controls below 640px. Two columns only at `sm:` and up.
- **No hover-only** instructions or expand. Collapse/expand is a tap target at least **44×44px**.
- Help text is a normal sentence under the title, **not** a tooltip. Minimum ~14px, readable in dark mode.
- Chart SVG already uses `viewBox` + `width: 100%`. Keep that. Do not lock a 600px width.
- Trend sparkline uses `width: 100%` of the card (drop the 200px max-width cap that hides it on a phone).
- Markdown Write/Preview is a **full-width segmented control**, stacked textarea, then preview. Not side-by-side on small screens.
- `.md` upload is a labeled control with `accept=".md,text/markdown,text/plain"`. Full-width button on small screens. Uses the native file picker (works on iOS/Android). Not drag-and-drop only.
- No horizontal overflow inside `.ce-block__content`. Existing editor CSS already sets `max-width: none` on that class; do not reintroduce a 650px cap on custom blocks.
- Dark mode: axis ticks, grid, and labels must contrast on `dark` (today they use `rgba(100,116,139,0.7)`, which disappears on the dark editor). Use tokens that match `text-slate-600` / `dark:text-slate-300`.
- Touch: “Parse CSV” and “Upload .md” are full-width on small screens (`w-full sm:w-auto`).

Desktop keeps the same components; it only gains a two-column form inside the collapsed “Edit data” panel.

---

## 4. Chart and Trend UX

### 4.1 Shared edit chrome

In **edit** mode, each block is:

1. Title row (Chart title or “Trend”)
2. One-line how-to
3. Live graphic (always mounted)
4. Collapsed **Edit data** (`<details>` or equivalent button). Open = current fields.

In **readOnly** (preview tab and published article): title + graphic + legend/summary only. No how-to, no Edit data, no CSV box.

### 4.2 Copy (locked)

- Chart: `Paste CSV (first row = headers) or one series per line: Revenue:10,20,30`
- Trend: `Comma-separated values. Optional labels. Percent and direction are calculated for you.`

Trend badges in edit/readOnly use plain language: **Up / Down / Flat**, **+21%**, **+21** — not `Direction: up`, `Pct`, `Delta`.

### 4.3 Rendering

Keep the existing hand-rolled SVG. **Do not** rewrite Chart with D3 in this project (`d3` is already a dependency for other UI; Chart comments that say “uses D3” are false and must be corrected). Pie stays the conic-gradient HTML preview unless a test shows it is actually invisible after contrast/layout fixes.

`createBlock` + `BlockWrapper` stay the mount path.

---

## 5. Markdown block (option A, locked)

Toolbox title **Markdown**. Same `createBlock` factory as Chart/Trend. Registered in `getEditorConfig` and `editorConfig` (read-only).

### 5.1 Data

```ts
interface MarkdownBlockData {
  markdown: string;
  sourceFileName?: string;
}
```

- `markdown` is the source of truth. Empty string is valid.
- `sourceFileName` is optional display-only (last uploaded file name). Missing on old-or-typed blocks.
- Default: `{ markdown: '' }`.
- Sanitize: `markdown: true`, `sourceFileName: true`.

### 5.2 Edit UI

- Empty: `Write or paste Markdown, or upload a .md file.`
- Edit mode has two tabs: **Write** (textarea) and **Preview** (rendered HTML). Default tab is **Write**. The writer can switch to Preview without leaving the block.
- Read-only (preview tab and published article) is always rendered HTML, with no tabs, textarea, or upload.
- **Upload .md**: `FileReader.readAsText`. Replaces `markdown` with file contents and sets `sourceFileName`. Confirm if the textarea already has content: native `confirm('Replace current Markdown with this file?')`. Cancel leaves the block unchanged.
- Reject files larger than **256 KiB** with an inline error: `File is too large (max 256 KB).` Do not upload to MinIO. This is not the Image tool.
- Reject non-text by extension/type; if the user forces a `.png`, show `Use a .md or text file.`

### 5.3 Allowed Markdown

Subset, GitHub-flavored lists/fences:

- Headings (`#`–`###` only; deeper headings render as `h3`)
- Paragraphs, line breaks
- Bold, italic
- Ordered and unordered lists
- Links (`http`/`https`/`mailto` only; `javascript:` stripped)
- Inline code and fenced code blocks
- Blockquotes

**Disallowed (strip, do not render):** raw HTML, images, scripts, iframes, tables, footnotes, Math. Images stay the existing Image tool. Formulas stay the Formula tool.

### 5.4 Render path

- Add **`marked`**. Lazy-import it the same way Formula lazy-imports KaTeX so the create page does not pay for Markdown until the block mounts.
- Run HTML through existing **`isomorphic-dompurify`**. Allowlist: `h1,h2,h3,p,br,ul,ol,li,a,code,pre,strong,em,blockquote,span`. Links: `href` + `rel="noopener noreferrer"` + `target="_blank"` for http(s).
- Do **not** use `@mdx-js/*` for writer content (compile-to-JS, wrong threat model).
- Non-empty read-only: rendered Markdown inside `BlockWrapper`, no textarea, no upload. Empty read-only: render nothing.

### 5.5 Published and cards

Preview and published remount the same read-only EditorJS config; registering `markdown` there is enough for the article page.

Feed/profile card excerpts already skip Chart/Trend. **Do not** change excerpt parsing in this project. A post whose body is only a Markdown block may have an empty card excerpt; the title still shows. Changing excerpts later is additive.

---

## 6. Files (implementation locus)

All under `local/the_monkeys/apps/the_monkeys/src/` unless noted.

| Path | Role |
| --- | --- |
| `components/editor/customBlocks/shared/types.ts` | Add `MarkdownBlockData` + `MARKDOWN_TOOLBOX`. No Chart/Trend shape change. |
| `components/editor/customBlocks/ChartBlock/ChartComponent.tsx` | Layout, copy, dark-mode SVG colors, collapsible form. |
| `components/editor/customBlocks/ChartBlock/index.ts` | Sanitizer `showLegend: true` only. |
| `components/editor/customBlocks/TrendBlock/TrendComponent.tsx` | Layout, copy, sparkline width, badge labels. Deduplicate `computeTrend` by importing from `index.ts` if that does not create a cycle; otherwise extract `computeTrend` to `trendMath.ts`. |
| `components/editor/customBlocks/MarkdownBlock/` | New `index.ts` + `MarkdownComponent.tsx`. |
| `config/editor/monkeys_editor.config.ts` | Register `markdown`. |
| `config/editor/monkeys_editor_readonly.config.ts` | Register `markdown`. |
| Tests next to the blocks (`*.test.tsx`) | Vitest + Testing Library, same as other UI tests. |

No engine files.

---

## 7. Errors

| Case | Behavior |
| --- | --- |
| Chart/Trend with empty series/values | Existing `EmptyState`; do not crash. |
| Invalid Chart `type` / palette | `normalizeData` falls back to line / ocean (already). |
| Markdown empty | Empty state in edit. In readOnly, render nothing (no bordered empty card). |
| `.md` too large / not text | Inline error; previous markdown kept. |
| Upload confirm cancelled | No change. |
| `marked` or DOMPurify throw | Show `Could not render Markdown.` Keep source in the textarea. |
| Unknown HTML in paste | Stripped by DOMPurify; remaining Markdown still shows. |

---

## 8. Testing

Vitest in the frontend app (`pnpm test` from `apps/the_monkeys`).

**Chart / Trend**

- Sample default data still produces a preview container (SVG or pie node) in edit and `readOnly`.
- How-to text is in the document in edit; **absent** in `readOnly`.
- “Edit data” starts collapsed in edit.
- Saving returns the same field set as `ChartBlockData` / `TrendBlockData` (no extra keys).
- Dark-mode class on a parent still leaves axis/label fill not equal to the old `rgba(100,116,139,0.7)` (assert the generator uses the new contrast colors).
- Trend sparkline SVG style includes `width:100%` and does not include `max-width:200px`.

**Markdown**

- Default save is `{ markdown: '' }` (no required `sourceFileName`).
- Typing `# Hello` and switching to preview renders an `h1`.
- `<script>alert(1)</script>` and `javascript:alert(1)` links do not survive into the sanitized HTML.
- Images in Markdown (`![](x)`) are not rendered as `img`.
- A File with `.md` content populates `markdown` and `sourceFileName` (mock `FileReader` / pass a File into the handler).
- File > 256 KiB leaves `markdown` unchanged and shows the size error.
- `readOnly` with markdown shows rendered HTML and no file input.

**Regression**

- Readonly config still includes `chart` and `trend`.
- Existing Chart JSON `{ type:'bar', labels:['A'], series:[{name:'S', values:[1]}] }` still renders.

Browser check after implementation (create page, ~375px and desktop): insert Chart, Trend, Markdown; upload a small `.md`; toggle preview; publish and open the article.

---

## 9. Out of scope

- Whole-post `.md` import / export
- Replacing EditorJS with a Markdown editor
- Dataset file upload or Chart CSV **file** upload (CSV paste stays)
- Formula, Citation, Methodology, Dataset, Embed help text (follow-up)
- Toolbox categories (`TIER_LABELS`)
- D3 rewrite of Chart
- Engine, proto, Elasticsearch, gateway
- Feed card excerpt of Markdown
- Discussions

---

## 10. Success

A writer on a phone can insert Chart or Trend, **see the graphic immediately**, understand how to enter data from one sentence, and save. They can insert Markdown, paste or upload a `.md` file, preview it, publish, and read it on the article page. Old posts without Markdown and old Chart/Trend JSON still open and look correct.
