# Editor undo and select-all — design spec

Date: 2026-09-21  
Status: **approved.** Plan: `docs/superpowers/plans/2026-09-21-editor-undo-select-all.md`.  
Scope: Google Docs–style **Select All** and **Undo/Redo** for the post canvas. **Frontend only** (`local/the_monkeys`, branch `codex/group-scoped-blogs`). Engine, ES, gateway, and publish APIs unchanged. Saved EditorJS JSON unchanged.

Product model (locked): [Google Docs](https://docs.google.com). One document, not the website around it.

Hard constraints (locked): **backward compatible**, **mobile-first**, **no client-side exception / white screen**, **DRY**, fast render.

Related (do not reopen): Chart / Trend / Markdown spec `docs/superpowers/specs/2026-09-21-editor-chart-trend-markdown-design.md`.

---

## 1. Product (locked)

Today `Ctrl/Cmd+A` on the edit, Preview, and published post pages selects the **whole HTML document** (nav, tabs, footer, Cookie Policy). `Ctrl/Cmd+Z` is native field undo only. There is no EditorJS history, so add/remove/reorder blocks cannot be undone. Chart / Trend / Markdown React fields are outside that native stack.

After this work, those pages behave like a Docs canvas:

| Shortcut | In the post (edit / Preview / published article) | In search, dialog, or publish drawer |
| --- | --- | --- |
| `Ctrl/Cmd+A` | Entire post canvas (title + all blocks) | That field only |
| `Ctrl/Cmd+Z` | Previous **document** step (edit only) | Native field undo |
| `Ctrl+Y` / `Ctrl/Cmd+Shift+Z` | Redo (edit only) | Native redo |

**Select all** does **not** stop at the current paragraph, heading, or Markdown textarea. If the caret is in a field **inside** the canvas, Select All still covers the whole post.

**Undo** is one in-memory history for the writable editor: typing that EditorJS reports, and structural edits (add / remove / reorder / custom-block save). Preview and published pages are read-only: Select All only, no undo stack, no Undo/Redo buttons.

Refresh or leaving the edit page clears history (same as an unsaved Docs tab). History is **not** written to the draft JSON.

---

## 2. Backward compatibility (locked)

- Do not change `OutputData`, block type names, or any block `data` fields.
- Do not add required fields. Undo state is **not** saved.
- Posts that never hit this code path (home, groups, settings) keep native browser Select All / Undo.
- If `editorjs-undo` fails to import or attach, the editor still mounts and saves. Select All still works. Undo/Redo buttons are **hidden** (not shown disabled). When the plugin works and the stack is empty, buttons are **visible and disabled**. No white screen.
- Existing 500ms draft `onChange` debounce, WebSocket save, and orphan-file deletion stay as they are.
- Duplicate holder id `monkeys_editor_editor-container` (edit vs preview, only one mounted) stays. Do not rename it in this work.

---

## 3. Mobile-first (locked)

Phones have no Ctrl. Design for ~360px edit chrome first.

- **Select all:** do not use `user-select: none` on `html`/`body`. Canvas gets `user-select: text` so long-press / drag selection stays on the post. Site chrome CSS does not change. Shortcuts, not CSS, own `Ctrl/Cmd+A`.
- **Undo / Redo:** two buttons on the existing edit-page bar (Online, then Undo/Redo, then Edit/Preview tabs, then Publish). The bar may wrap (`flex-wrap`). `min-h-11 min-w-11` tap targets. `aria-label` “Undo” / “Redo”. Disabled when that stack is empty. Visible only in **Edit** tab, not Preview, not the published article.
- No extra floating toolbar, no swipe-from-edge gesture, no blocking of native copy/paste or IME.
- Desktop keeps the same buttons (they do not hurt) plus the keyboard shortcuts.

---

## 4. Canvas (locked)

Mark the post with `data-post-canvas` on a wrapper that is **only** the document:

| Surface | Canvas includes | Canvas excludes |
| --- | --- | --- |
| Edit (`components/editor/index.tsx`) | The EditorJS holder (H1 title is the first block) | Online pill, Edit/Preview tabs, Publish drawer trigger, Saving toast, site nav/footer |
| Preview (`BlogPreview`) | `BlogHeading` + read-only editor | Date line, author card, site chrome |
| Published (`BlogPageClient`) | `BlogHeading` + read-only editor | Back, Edit dialog, date, scope line, author, reactions, topics, Social Snapshot, recommendations, site chrome |

One canvas node per page. Edit vs Preview are mutually exclusive, so they never both exist.

**Chrome** (native shortcuts, never canvas Select All / document Undo): any focus inside `[data-shortcut-scope="chrome"]`, `[role="dialog"]`, or a Radix dialog/drawer content node. Put `data-shortcut-scope="chrome"` on the site search field and the Publish drawer if those are not already inside a dialog role.

---

## 5. Architecture (locked)

Approach **A**: post canvas + shortcut helper + `editorjs-undo`. Not CSS-only Select All. Not a custom React replay of every `onChange`.

### 5.1 Units

| Unit | Does | Used by | Depends on |
| --- | --- | --- | --- |
| `isChromeShortcutTarget(node)` | True if native field undo/select-all must win | Hook | DOM `closest` |
| `selectPostCanvas(root)` | Selects the contents of `root` | Hook | `window.getSelection` + `Range` |
| `usePostCanvasShortcuts({ canvasRef, undo })` | Capture-phase `keydown` for A / Z / Y | Edit editor, `PostArticleCanvas` | The two helpers; optional undo controller |
| `PostArticleCanvas` | Wrapper: `data-post-canvas` + hook **without** undo | `BlogPreview`, `BlogPageClient` | Hook |
| Edit `Editor` | `data-post-canvas` on holder; after `isReady`, attach undo plugin; pass controller into hook | Edit page | `editorjs-undo` (dynamic import) |
| Edit-bar Undo/Redo | Call `undo()` / `redo()`; subscribe via plugin `onUpdate` | `edit/[blogId]/page.tsx` | Controller exposed from Editor (callback/`ref`) |

Keep helpers in `src/components/editor/postCanvas/` so pages do not copy `keydown` logic.

Do **not** add undo as an EditorJS `tools` entry. It wraps the instance after `isReady`.

Editor grows an optional `onUndoHandleChange?: (handle: PostUndoHandle | null) => void`. It reports the handle after the plugin attaches and `null` on unmount. The edit page stores that in state for the bar. Do not use an imperative ref on `Editor`.

### 5.2 Shortcut ownership

Document **capture** listener (so it runs before `editorjs-undo` bubble handlers). The hook listens on `document` while mounted. Any non-chrome focus on that page — including `document.body` or reactions below the article — treats Select All as canvas select, not whole-page select.

1. If the event target is a chrome shortcut target → `stopPropagation` and **return**. Do not `preventDefault` (native field select-all / undo must run).
2. If the canvas ref is null → **return**.
3. `Ctrl/Cmd+A` (no Shift) → `preventDefault` + `stopPropagation`, then `selectPostCanvas(canvas)`.
4. Undo/redo keys → only if `undo` was passed (edit). Call `undo.undo()` / `undo.redo()`, then `preventDefault` + `stopPropagation`. Preview/published pass no `undo`, so Z/Y are left to the browser.

Mac: `metaKey`. Windows/Linux: `ctrlKey`. Redo is `Ctrl+Y` (ctrl, not meta) **or** `Ctrl/Cmd+Shift+Z`. Do not bind `Cmd+Y`. Ignore `Alt+Ctrl` combos.

`editorjs-undo` default shortcuts: disable them in the constructor when the API supports it. We still instantiate the plugin for the **history stack**. Our listener is the only keybinder; `stopPropagation` on handled keys prevents double-undo if the plugin still binds.

### 5.3 Undo plugin

Package: `editorjs-undo` (Kommitters), the maintained EditorJS undo plugin. Dynamic `import()` after `editor.isReady` so a missing/broken module cannot take down first paint.

- `new Undo({ editor, maxLength: 30, onUpdate })` then `undo.initialize(initialData)` so the first Undo does not wipe a loaded draft to empty.
- Stack is in memory. `maxLength: 30`.
- In the same `useEffect` cleanup that calls `editor.destroy()`: call `undo.destroy()` if that method exists, and `onUndoHandleChange(null)`. The hook removes its own `keydown` listener.
- Custom blocks (Chart, Trend, Markdown, …) enter history when EditorJS reports `onChange` for that block. Do not build a second React history. Intra-block typing granularity is whatever EditorJS + the plugin already snapshot; do not change the 500ms save debounce to “fix” that.

Expose a stable handle to the edit page for buttons:

```ts
type PostUndoHandle = {
  undo: () => void;
  redo: () => void;
  canUndo: boolean;
  canRedo: boolean;
};
```

`onChange` from Editor to `setData` stays the source of truth for the draft. Undo/redo mutate the EditorJS instance; the existing `onChange` path persists the restored data.

### 5.4 Select All implementation

```ts
const range = document.createRange();
range.selectNodeContents(canvas);
const sel = window.getSelection();
sel?.removeAllRanges();
sel?.addRange(range);
```

Do not use `document.execCommand('selectAll')` — that selects the HTML document (the current bug).

---

## 6. Error handling (locked)

Every public helper and the hook body is defensive. Failures are no-ops plus `console.warn`, never thrown to React.

| Failure | Behavior |
| --- | --- |
| `editorjs-undo` import or `new Undo` throws | Editor remains; no undo stack; buttons hidden |
| `editor.isReady` rejects / editor already destroyed | Skip attach |
| `initialize(data)` throws | Skip history; editor still editable |
| `undo()` / `redo()` throws or stack empty | No-op; buttons stay disabled via `canUndo`/`canRedo` |
| Canvas ref is null | Do not intercept the key |
| `getSelection` / `Range` throws (hidden node, detached) | Catch; leave existing selection |
| Hook runs on server | No `window` access until `useEffect` |
| Double mount in React Strict Mode | Cleanup must remove the listener and destroy the plugin; second mount creates a fresh stack |

Do not wrap the whole edit page in a new error boundary for this feature. The existing Chart/Trend `BlockErrorBoundary` stays for blocks.

---

## 7. Testing (locked)

Vitest + Testing Library, same pattern as `customBlocks/**/*.test.ts(x)`. No Playwright in this project unless it already exists for the app.

**Pure (write first in implementation):**

- `isChromeShortcutTarget`: dialog, `[data-shortcut-scope="chrome"]`, canvas input, canvas textarea, canvas `contenteditable`, `document.body`.
- `shouldHandleSelectAll` / `shouldHandleUndo`: chrome vs canvas vs no modifier vs `Ctrl+Shift+A`.
- `selectPostCanvas`: selection anchor/focus are inside the canvas node; does not throw if `getSelection` is missing.

**Component:**

- `PostArticleCanvas`: `data-post-canvas` present; `Ctrl+A` (user-event) selects only nodes inside the wrapper when a sibling “nav” text exists.
- Undo buttons: disabled at start; `aria-label`s present; not rendered when preview mode is on (test the bar fragment, mock the handle).

Do **not** boot a real `MonkeysEditor` in jsdom for this spec. Mock `editorjs-undo`.

Manual check after implementation (not automated): `/edit/:id` Edit + Preview, and a published `/blog/:slug`, on desktop keyboard and a ~360px viewport for the buttons.

---

## 8. Out of scope

- Persisting undo across refresh, tabs, or devices
- Collaborative / OT undo
- Undo on Preview or published
- Changing Chart / Trend / Markdown block UI
- Renaming the editor holder id
- Global `user-select: none` on the shell
- Engine, proto, ES, or API changes

---

## 9. Success

1. `Ctrl/Cmd+A` on edit, Preview, and published post selects title + body only — not nav, tabs, or footer.
2. `Ctrl/Cmd+A` in site search or Publish drawer still selects that field.
3. `Ctrl/Cmd+Z` in the editor undoes the last document change, including adding a block; redo restores it.
4. `Ctrl/Cmd+Z` in search does not change the post.
5. Phone edit bar can Undo/Redo without a keyboard; Preview and published pages do not show those buttons.
6. Loading a pre-feature draft or published post still works. No new client exception from this feature.
