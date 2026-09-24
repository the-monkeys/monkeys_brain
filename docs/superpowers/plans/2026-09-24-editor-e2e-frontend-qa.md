# Editor Frontend E2E QA and Repair Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make create → draft → edit every block → preview → publish a stable Google Docs–like editor on desktop and mobile, with no uneditable stubs, no page-wide Select All, and no editor/preview layout change.

**Architecture:** Frontend-only. Keep the existing page chrome (date / BlogHeading / byline on preview and published). Scope Ctrl/Cmd+A to `[data-post-canvas]`. Register every block type the JSON can contain, including `title`, so EditorJS never shows “The block can not be displayed correctly.” Undo stays keyboard + `editorjs-undo`, no extra Online-bar icons.

**Tech Stack:** Next.js app in `local/the_monkeys/apps/the_monkeys`, EditorJS via `@themonkeys/monkeys-editor`, Vitest + Testing Library, cursor-ide-browser for logged-in UI.

## Global Constraints

- Do not touch `microservices/`, proto, ES, gateway, or any Go file.
- Do not change published/preview visual design (BlogHeading stays in the page header; do not move it into the canvas).
- Do not add Undo/Redo buttons next to Online.
- Do not rewrite draft JSON on load (no “convert every first block to X” on fetch).
- Do not use `document.execCommand('selectAll')`. Do not set `user-select: none` on `html`/`body`.
- PowerShell: no `&&`. Do not commit unless the user asks.
- Tests: `npx vitest --run <file>` from `local/the_monkeys/apps/the_monkeys`.

### Confirmed bugs (2026-09-24)

1. **Uneditable Title stub.** Writable `getEditorConfig` unregistered `title`. Drafts whose first block is `type: "title"` render EditorJS’s stub: “Title / The block can not be displayed correctly.” Readonly config still registers TitleBlock, so preview/published body can render it while Edit cannot. Screenshot: `/edit/edgeverify2` with paragraph “Hello thete”.
2. **Select All selects nav + chrome.** Capture Ctrl/Cmd+A is supposed to `preventDefault` and `selectNodeContents` on `[data-post-canvas]`. Preview and published no longer wrap with `PostArticleCanvas` (test now forbids it), so those pages fall through to native page Select All (Monkeys nav, topics, footer). On Edit, `isChrome === true` returns without `preventDefault`, so native Select All can include nav. Site header/nav is not `data-shortcut-scope="chrome"`.
3. **QA browser has no session.** Agent tab at `localhost:3000/edit/edgeverify2` redirected to Log in (cookie names: `_clck` only). Logged-in E2E must use the user’s already-authenticated window.

### Code review (frontend-only, no edits)

Independent reviews of [select-all](dca91ad1-d86f-4ec1-8fcf-73b627f8c337), [undo/blocks](ef556b2c-aeac-4a03-9960-bbfd89dbb27f), and [title tools](c488adf1-fc82-47c4-acf1-f88abd1183e6) match the bugs above:

- Ctrl/Cmd+A `preventDefault` runs only after chrome/canvas checks; chrome returns with `stopPropagation` only, so native page Select All can include nav. Preview and published never mount `usePostCanvasShortcuts`.
- Writable config omits `title`; leftover `type: "title"` stubs in Edit. TitleBlock class itself is fine. Undo icons are already off the Online bar — keep them off.
- Publish/schedule still uses `type !== 'header' && level !== 1`, so a first `type: "title"` block stays blocked after the stub is gone. Extra white-screen risks for Task 5: `preview.tsx` `destroy()` has no try/catch; edit `api.saver.save()` has no catch; `editorjs-undo` `getRangeAt(0)` can throw when `rangeCount === 0`.

### File map

| File | Role |
| --- | --- |
| `local/the_monkeys/apps/the_monkeys/src/config/editor/monkeys_editor.config.ts` | Writable tools; must include `title` |
| `local/the_monkeys/apps/the_monkeys/src/config/editor/monkeys_editor_readonly.config.ts` | Preview/published tools |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TitleBlock/index.tsx` | Existing TitleBlock |
| `local/the_monkeys/apps/the_monkeys/src/app/edit/[blogId]/page.tsx` | Create/edit chrome; INITIAL_DATA |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/index.tsx` | Edit canvas + shortcuts |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/shortcuts.ts` | Select All / chrome |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/usePostCanvasShortcuts.ts` | Capture keydown |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/BlogPreview.tsx` | Preview layout |
| `local/the_monkeys/apps/the_monkeys/src/app/blog/[slug]/BlogPageClient.tsx` | Published layout |

---

### Task 1: Register TitleBlock on the writable editor

**Files:**
- Modify: `local/the_monkeys/apps/the_monkeys/src/config/editor/monkeys_editor.config.ts`
- Test: `local/the_monkeys/apps/the_monkeys/src/config/editor/editorTools.contract.test.ts` (create)

**Interfaces:**
- Consumes: `TitleBlockTool` default export
- Produces: `getEditorConfig(blogId).tools.title.class === TitleBlockTool`

- [ ] **Step 1: Write the failing test**

```ts
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const dir = dirname(fileURLToPath(import.meta.url));

describe('editor tools contract', () => {
  it('registers title on both write and read configs so drafts never stub', () => {
    const write = readFileSync(join(dir, 'monkeys_editor.config.ts'), 'utf8');
    const read = readFileSync(
      join(dir, 'monkeys_editor_readonly.config.ts'),
      'utf8'
    );
    expect(write).toMatch(/title:\s*\{/);
    expect(write).toContain('TitleBlockTool');
    expect(read).toMatch(/title:\s*\{/);
    expect(read).toContain('TitleBlockTool');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `npx vitest --run src/config/editor/editorTools.contract.test.ts`

Expected: FAIL — writable config has no `title:` tool.

- [ ] **Step 3: Minimal implementation**

In `getEditorConfig` `tools`, add `title: { class: TitleBlockTool }` (same as readonly). Import `TitleBlockTool`. Hide it from the plus menu if the tool config allows (`toolbox: false` or equivalent) so main’s menu stays Header-only. Do not change Header/paragraph/markdown/chart/trend.

Also fix publish/schedule first-block check in `edit/[blogId]/page.tsx`: valid if `type === 'title'` **or** (`type === 'header'` and `level === 1`). Do not keep `type !== 'header' && level !== 1`.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: UI check (logged-in)**

Open `/edit/edgeverify2`. First block must be an editable title field, not “The block can not be displayed correctly.” Type in it. Reload. Still editable.

---

### Task 2: Select All must never include nav or app chrome

**Files:**
- Modify: `.../postCanvas/shortcuts.ts`
- Modify: `.../postCanvas/usePostCanvasShortcuts.ts`
- Modify: `.../postCanvas/usePostCanvasShortcuts.test.tsx`
- Modify: `.../app/edit/[blogId]/page.tsx` (mark Online/Edit/Preview row `data-shortcut-scope="chrome"`)
- Modify: `.../components/editor/BlogPreview.tsx` and `.../app/blog/[slug]/BlogPageClient.tsx` only to add an invisible `data-post-canvas` wrapper around the article column **without moving BlogHeading into the editor canvas or changing classes that affect layout**
- Modify: `.../postCanvas/chromeScope.test.ts`

**Interfaces:**
- Consumes: `KeyboardEvent`, `[data-post-canvas]`, `[data-shortcut-scope="chrome"]`
- Produces:
  - `selectPostCanvas(root)` range `commonAncestor` is `root` or a descendant
  - Ctrl/Cmd+A `preventDefault` whenever a post canvas exists on the page, except when the event target is `input, textarea, [contenteditable]` inside `[data-shortcut-scope="chrome"]` (search, publish drawer) so those fields keep native select-in-field

- [ ] **Step 1: Failing tests**

Add to `usePostCanvasShortcuts.test.tsx`:

```ts
it('Ctrl+A selects only [data-post-canvas], not a sibling nav', () => {
  function Page() {
    const canvasRef = useRef<HTMLDivElement>(null);
    usePostCanvasShortcuts({ canvasRef, undo: null });
    return (
      <div>
        <nav>Monkeys Business Tech</nav>
        <div ref={canvasRef} data-post-canvas>
          Untitled Post
        </div>
      </div>
    );
  }
  render(<Page />);
  fireEvent.keyDown(document, { key: 'a', ctrlKey: true });
  const canvas = document.querySelector('[data-post-canvas]') as HTMLElement;
  const sel = window.getSelection();
  const node = sel?.anchorNode;
  expect(canvas.contains(node instanceof Node ? node : (node as Node))).toBe(
    true
  );
  expect(
    document.querySelector('nav')?.contains(sel?.anchorNode as Node)
  ).toBe(false);
});
```

Add chromeScope assertion: edit toolbar row includes `data-shortcut-scope="chrome"`. Keep the assertion that BlogHeading is not inside `PostArticleCanvas` if that would move title visually — wrap at a parent that already contains heading + body as they are today.

- [ ] **Step 2: Run tests, confirm FAIL**

- [ ] **Step 3: Minimal fix**

In `onKeyDown`, when `shouldHandleSelectAll` and a canvas exists: always `preventDefault` + `stopPropagation` + `selectPostCanvas(canvas)`. Do not `return` on `isChrome` before preventDefault unless the target is an editable chrome field (`input, textarea, select, [contenteditable]` inside chrome). If canvas is null, do nothing (do not select the page).

Preview/published: wrap the existing article column in `data-post-canvas` without changing BlogHeading markup/classes.

- [ ] **Step 4: Tests PASS**

- [ ] **Step 5: UI**

Logged-in: Edit, Preview, published `/blog/...`. Focus body, Ctrl+A. Selection highlight must not cover Monkeys logo, topic nav, Search, Online, Edit/Preview, Publish, footer. Then Backspace on Edit clears only the document and restores first Heading 1 / title.

---

### Task 3: First block stays an editable title; no JSON rewrite on load

**Files:**
- `.../app/edit/[blogId]/page.tsx` (`INITIAL_DATA`)
- `.../postCanvas/clearPostCanvasDocument.ts`
- `.../postCanvas/withFirstBlockTitleId.ts` (already stamps `id: "title"` on save only)

**Rule:** New posts: `{ id: 'title', type: 'header', data: { text: 'Untitled Post', level: 1 } }` like main. Old drafts with `type: 'title'` keep that type and become editable because Task 1 registered the tool. Save may set `blocks[0].id = 'title'` only; do not change `type` on load.

- [ ] **Step 1:** Tests already in `clearPostCanvasDocument.test.ts` and `withFirstBlockTitleId.test.ts`. Add: opening fixture `{ type: 'title', id: 'title', data: { text: 'Hello' } }` is not converted to header in any load effect (grep `ensureFirstBlock` / type rewrite — must be absent).

- [ ] **Step 2–4:** If a load rewriter exists, delete it. Do not add a new one.

---

### Task 4: Every EditorJS tool remains editable (no stubs)

**Tools to click in the plus menu and confirm edit + delete + undo:**

header, paragraph, list, quote, delimiter, table, code, image, embed, markdown, chart, trend, formula, citation, methodology, dataset, mention.

- [ ] **Step 1:** Contract test: writable and readonly configs both mention each of those tool keys (plus `title`).

- [ ] **Step 2:** UI (mobile 390px and desktop 1280px): add each block, type/edit, select that one block, change it, delete it, Ctrl+Z restores, no white Next.js error, no stub banner.

- [ ] **Step 3:** Image: upload, caption, replace. Embed: paste a URL, must remain editable (not a dead stub). Chart/Trend/Markdown: existing edit UI, collapsed “Edit data” where already designed — do not restyle.

---

### Task 5: Undo/redo stability (no flicker, no extra icons)

**Files:** `attachEditorUndo.ts`, `editor/index.tsx`

- [ ] Confirm `EditUndoRedoButtons` is not imported in `edit/[blogId]/page.tsx`.
- [ ] Ctrl+Z / Ctrl+Y / Ctrl+Shift+Z; Cmd variants on mac.
- [ ] Strict Mode remount: one `.codex-editor` in the holder after load (`dropStaleEditorRoots`).
- [ ] After undo, no Online/Offline flash, no editor unmount (selection may move; the canvas must not blank).
- [ ] Wrap `preview.tsx` `destroy()` in try/catch; catch `api.saver.save()` failures in edit `onChange`; do not let `getRangeAt(0)` with empty selection white-screen the page.

---

### Task 6: End-to-end article lifetime (logged-in UI)

Run in the user’s authenticated browser. Agent cannot complete this while `/edit/*` redirects to Log in.

| # | Path | Pass |
| --- | --- | --- |
| 1 | Create post (`/edit/{id}?isNew=true`) | First line Untitled Post, editable, Online |
| 2 | Type title + 2 paragraphs, wait for save | No stub, no white screen |
| 3 | Reload draft URL | Same blocks, still editable |
| 4 | Add then remove header/list/image/embed/md/chart | Each block editable while present |
| 5 | Ctrl+A then Backspace | Empty doc + first title/header; nav not selected |
| 6 | Type again, Ctrl+Z / Ctrl+Y | Stable, no icon bar |
| 7 | Preview tab | Date, BlogHeading, byline, body; no stub; Ctrl+A is article only |
| 8 | Publish (or stay draft if not publishing) | Profile card title matches first-block text the user typed; article heading matches; body does not repeat that heading |
| 9 | Re-open published in editor | Every remaining block editable |
| 10 | Viewport 390×844 | Plus menu, typing, select one block, no horizontal trap |

Do not publish to production during QA unless the user says to.

---

### Task 7: Remove leftover frontend that changes product chrome

- [ ] `PostArticleCanvas` may wrap for Select All but must not restyle heading/byline.
- [ ] No extra undo icons.
- [ ] No backend diffs (`git status -- microservices/` must stay clean aside from pre-existing untracked docs).

---

### Task 8: Verification commands

```powershell
npx vitest --run src/config/editor/editorTools.contract.test.ts src/components/editor/postCanvas
```

```powershell
npx vitest --run src/components/editor src/components/blog/getBlogContent.test.ts __tests__/src/app/blog
```

Then repeat Task 6 in a logged-in window. Quote screenshots of Select All (no nav highlight) and Edit (no Title stub).

---

## Spec coverage

- Backward compatible drafts with `type: title` → Task 1 + 3
- Uniform first-block id `title` → Task 3
- Select All = document only → Task 2
- Google Docs undo, no flicker, no extra UI → Task 5
- All components editable, mobile-first → Task 4 + 6
- Preview/editor design unchanged → Task 2 wrap + Task 7
- No backend → Global Constraints
- Next.js client crash watch → Task 4/5/6
