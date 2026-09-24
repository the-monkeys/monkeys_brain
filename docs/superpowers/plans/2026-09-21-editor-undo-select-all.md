# Editor Undo and Select-All Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `Ctrl/Cmd+A` select only the post (edit, Preview, published) and give the writable editor a Google Docs–style document undo/redo stack, including phone buttons.

**Architecture:** Mark the post with `data-post-canvas`. A capture-phase `keydown` helper selects that node and, on edit, drives `editorjs-undo`. Search and drawers keep native shortcuts. No saved JSON change.

**Tech Stack:** React 18, EditorJS (`@themonkeys/monkeys-editor`), `editorjs-undo`, Vitest + Testing Library, Tailwind.

**Spec:** `docs/superpowers/specs/2026-09-21-editor-undo-select-all-design.md`

## Global Constraints

- Frontend only under `local/the_monkeys/apps/the_monkeys`. Engine, proto, ES, gateway: do not touch.
- Do not change `OutputData`, block type names, or block `data` fields. Undo state is not saved.
- Do not use `document.execCommand('selectAll')`. Do not use `user-select: none` on `html`/`body`.
- Do not add undo as an EditorJS `tools` entry. Attach `editorjs-undo` after `isReady`.
- Do not boot a real `MonkeysEditor` in tests. Mock `editorjs-undo`.
- If the undo plugin fails, the editor still mounts; Undo/Redo buttons are hidden.
- PowerShell: never chain with `&&`. Use `;` or separate commands.
- Do not git commit unless the user explicitly asks. Skip every commit step until then.
- Tests run from `local/the_monkeys/apps/the_monkeys` with `pnpm test -- <file>`.

### File map

| File | Responsibility |
| --- | --- |
| `.../editor/postCanvas/types.ts` | `PostUndoController`, `PostUndoHandle` |
| `.../editor/postCanvas/shortcuts.ts` | Chrome check, shortcut kind, `selectPostCanvas` |
| `.../editor/postCanvas/usePostCanvasShortcuts.ts` | Document capture `keydown` |
| `.../editor/postCanvas/PostArticleCanvas.tsx` | Wrapper for Preview + published (no undo) |
| `.../editor/postCanvas/attachEditorUndo.ts` | Dynamic import + adapter around `editorjs-undo` |
| `.../editor/postCanvas/EditUndoRedoButtons.tsx` | Mobile/desktop bar buttons |
| `.../editor/index.tsx` | `data-post-canvas`, hook + undo attach, `onUndoHandleChange` |
| `.../editor/BlogPreview.tsx` | Wrap title + read-only editor |
| `.../app/blog/[slug]/BlogPageClient.tsx` | Same wrap on published post |
| `.../app/edit/[blogId]/page.tsx` | Undo/Redo on the existing bar |
| `.../components/search/SearchInput.tsx` | `data-shortcut-scope="chrome"` |
| `.../components/blog/actions/PublishBlogDrawer.tsx` | `data-shortcut-scope="chrome"` on `DrawerContent` |
| `.../components/icon.tsx` | `RiArrowGoBack`, `RiArrowGoForward` |

---

### Task 1: Shortcut helpers

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/types.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/shortcuts.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/shortcuts.test.ts`

**Interfaces:**
- Consumes: DOM `Element`, `KeyboardEvent`-like objects
- Produces:
  - `export type PostUndoController = { undo: () => void; redo: () => void }`
  - `export type PostUndoHandle = PostUndoController & { canUndo: boolean; canRedo: boolean }`
  - `export type ShortcutKind = 'selectAll' | 'undo' | 'redo'`
  - `export function isChromeShortcutTarget(node: EventTarget | null): boolean`
  - `export function shortcutKind(e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'>): ShortcutKind | null`
  - `export function shouldHandleSelectAll(e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'>, opts: { isChrome: boolean }): boolean`
  - `export function shouldHandleUndo(e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'>, opts: { isChrome: boolean; hasUndo: boolean }): boolean`
  - `export function shouldHandleRedo(e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'>, opts: { isChrome: boolean; hasUndo: boolean }): boolean`
  - `export function selectPostCanvas(root: HTMLElement): void`

- [ ] **Step 1: Write the failing test**

Create `shortcuts.test.ts`:

```ts
import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  isChromeShortcutTarget,
  selectPostCanvas,
  shouldHandleRedo,
  shouldHandleSelectAll,
  shouldHandleUndo,
  shortcutKind,
} from './shortcuts';

function key(
  partial: Partial<KeyboardEvent> & Pick<KeyboardEvent, 'key'>
): Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'> {
  return {
    shiftKey: false,
    ctrlKey: false,
    metaKey: false,
    altKey: false,
    ...partial,
  };
}

afterEach(() => {
  document.body.innerHTML = '';
  vi.restoreAllMocks();
});

describe('isChromeShortcutTarget', () => {
  it('is true for dialog, chrome scope, and vaul drawer', () => {
    document.body.innerHTML = `
      <div data-shortcut-scope="chrome"><input id="search" /></div>
      <div role="dialog"><input id="dialog" /></div>
      <div data-radix-dialog-content><input id="radix" /></div>
      <div data-vaul-drawer><input id="drawer" /></div>
      <div data-post-canvas>
        <textarea id="md"></textarea>
        <input id="chart" />
        <div id="heading" contenteditable="true"></div>
      </div>
    `;
    expect(isChromeShortcutTarget(document.getElementById('search'))).toBe(true);
    expect(isChromeShortcutTarget(document.getElementById('dialog'))).toBe(true);
    expect(isChromeShortcutTarget(document.getElementById('radix'))).toBe(true);
    expect(isChromeShortcutTarget(document.getElementById('drawer'))).toBe(true);
    expect(isChromeShortcutTarget(document.getElementById('md'))).toBe(false);
    expect(isChromeShortcutTarget(document.getElementById('chart'))).toBe(false);
    expect(isChromeShortcutTarget(document.getElementById('heading'))).toBe(false);
    expect(isChromeShortcutTarget(document.body)).toBe(false);
    expect(isChromeShortcutTarget(null)).toBe(false);
  });
});

describe('shortcutKind', () => {
  it('maps Docs shortcuts and ignores Alt+Ctrl and Shift+A', () => {
    expect(shortcutKind(key({ key: 'a', ctrlKey: true }))).toBe('selectAll');
    expect(shortcutKind(key({ key: 'A', metaKey: true }))).toBe('selectAll');
    expect(shortcutKind(key({ key: 'a', ctrlKey: true, shiftKey: true }))).toBe(
      null
    );
    expect(shortcutKind(key({ key: 'z', ctrlKey: true }))).toBe('undo');
    expect(shortcutKind(key({ key: 'z', metaKey: true, shiftKey: true }))).toBe(
      'redo'
    );
    expect(shortcutKind(key({ key: 'y', ctrlKey: true }))).toBe('redo');
    expect(shortcutKind(key({ key: 'y', metaKey: true }))).toBe(null);
    expect(shortcutKind(key({ key: 'z', ctrlKey: true, altKey: true }))).toBe(
      null
    );
    expect(shortcutKind(key({ key: 'a' }))).toBe(null);
  });
});

describe('shouldHandle*', () => {
  it('does not steal chrome or fire undo without a controller', () => {
    const select = key({ key: 'a', ctrlKey: true });
    const undo = key({ key: 'z', ctrlKey: true });
    const redo = key({ key: 'y', ctrlKey: true });
    expect(shouldHandleSelectAll(select, { isChrome: false })).toBe(true);
    expect(shouldHandleSelectAll(select, { isChrome: true })).toBe(false);
    expect(shouldHandleUndo(undo, { isChrome: false, hasUndo: true })).toBe(
      true
    );
    expect(shouldHandleUndo(undo, { isChrome: true, hasUndo: true })).toBe(
      false
    );
    expect(shouldHandleUndo(undo, { isChrome: false, hasUndo: false })).toBe(
      false
    );
    expect(shouldHandleRedo(redo, { isChrome: false, hasUndo: true })).toBe(
      true
    );
    expect(shouldHandleRedo(redo, { isChrome: false, hasUndo: false })).toBe(
      false
    );
  });
});

describe('selectPostCanvas', () => {
  it('adds a range inside the canvas', () => {
    const canvas = document.createElement('div');
    canvas.append('hello');
    document.body.append(canvas);
    const removeAllRanges = vi.fn();
    const addRange = vi.fn();
    vi.spyOn(window, 'getSelection').mockReturnValue({
      removeAllRanges,
      addRange,
    } as unknown as Selection);
    selectPostCanvas(canvas);
    expect(removeAllRanges).toHaveBeenCalled();
    expect(addRange).toHaveBeenCalled();
    const range = addRange.mock.calls[0][0] as Range;
    expect(range.commonAncestorContainer === canvas || canvas.contains(range.commonAncestorContainer)).toBe(
      true
    );
  });

  it('does not throw if getSelection is missing', () => {
    vi.spyOn(window, 'getSelection').mockReturnValue(
      null as unknown as Selection
    );
    const el = document.createElement('div');
    expect(() => selectPostCanvas(el)).not.toThrow();
  });
});
```

Create `types.ts` only after the test exists if you prefer TDD purity; the test does not import it yet. Implement types in Step 3 with the helpers.

- [ ] **Step 2: Run test to verify it fails**

Run:

```
cd local/the_monkeys/apps/the_monkeys
pnpm test -- src/components/editor/postCanvas/shortcuts.test.ts
```

Expected: FAIL — cannot find module `./shortcuts`.

- [ ] **Step 3: Write minimal implementation**

`types.ts`:

```ts
export type PostUndoController = {
  undo: () => void;
  redo: () => void;
};

export type PostUndoHandle = PostUndoController & {
  canUndo: boolean;
  canRedo: boolean;
};
```

`shortcuts.ts`:

```ts
export type ShortcutKind = 'selectAll' | 'undo' | 'redo';

const CHROME_SELECTOR = [
  '[data-shortcut-scope="chrome"]',
  '[role="dialog"]',
  '[data-radix-dialog-content]',
  '[data-vaul-drawer]',
].join(', ');

export function isChromeShortcutTarget(node: EventTarget | null): boolean {
  if (!node || !(node instanceof Element)) return false;
  return Boolean(node.closest(CHROME_SELECTOR));
}

function hasMod(
  e: Pick<KeyboardEvent, 'ctrlKey' | 'metaKey' | 'altKey'>
): boolean {
  if (e.altKey) return false;
  return e.ctrlKey || e.metaKey;
}

export function shortcutKind(
  e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'>
): ShortcutKind | null {
  if (!hasMod(e)) return null;
  const key = e.key.length === 1 ? e.key.toLowerCase() : e.key.toLowerCase();
  if (key === 'a' && !e.shiftKey) return 'selectAll';
  if (key === 'z' && !e.shiftKey) return 'undo';
  if (key === 'z' && e.shiftKey) return 'redo';
  if (key === 'y' && e.ctrlKey && !e.metaKey && !e.shiftKey) return 'redo';
  return null;
}

export function shouldHandleSelectAll(
  e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'>,
  opts: { isChrome: boolean }
): boolean {
  return shortcutKind(e) === 'selectAll' && !opts.isChrome;
}

export function shouldHandleUndo(
  e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'>,
  opts: { isChrome: boolean; hasUndo: boolean }
): boolean {
  return shortcutKind(e) === 'undo' && !opts.isChrome && opts.hasUndo;
}

export function shouldHandleRedo(
  e: Pick<KeyboardEvent, 'key' | 'shiftKey' | 'ctrlKey' | 'metaKey' | 'altKey'>,
  opts: { isChrome: boolean; hasUndo: boolean }
): boolean {
  return shortcutKind(e) === 'redo' && !opts.isChrome && opts.hasUndo;
}

export function selectPostCanvas(root: HTMLElement): void {
  try {
    const sel = window.getSelection?.();
    if (!sel) return;
    const range = document.createRange();
    range.selectNodeContents(root);
    sel.removeAllRanges();
    sel.addRange(range);
  } catch (err) {
    console.warn('selectPostCanvas failed', err);
  }
}
```

- [ ] **Step 4: Run test to verify it passes**

Run the same `pnpm test --` command. Expected: PASS.

- [ ] **Step 5: Commit**

Skip unless the user asked to commit.

```
git add local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/types.ts local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/shortcuts.ts local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/shortcuts.test.ts
git commit -m "test: add post canvas shortcut helpers"
```

(Frontend lives in gitignored `local/the_monkeys`; commit there if that repo is used. Do not commit engine `.pnpm-store`.)

---

### Task 2: Capture hook and article canvas

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/usePostCanvasShortcuts.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/PostArticleCanvas.tsx`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/PostArticleCanvas.test.tsx`

**Interfaces:**
- Consumes: helpers from Task 1; `PostUndoController`
- Produces:
  - `export function usePostCanvasShortcuts(opts: { canvasRef: React.RefObject<HTMLElement | null>; undo?: PostUndoController | null }): void`
  - `export function PostArticleCanvas(props: { children: React.ReactNode; className?: string }): JSX.Element` — root has `data-post-canvas` and `select-text`

- [ ] **Step 1: Write the failing test**

`PostArticleCanvas.test.tsx`:

```tsx
import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { PostArticleCanvas } from './PostArticleCanvas';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

function mockSelection() {
  const removeAllRanges = vi.fn();
  const addRange = vi.fn();
  vi.spyOn(window, 'getSelection').mockReturnValue({
    removeAllRanges,
    addRange,
  } as unknown as Selection);
  return { removeAllRanges, addRange };
}

describe('PostArticleCanvas', () => {
  it('marks the wrapper as the post canvas', () => {
    render(
      <PostArticleCanvas>
        <p>Post title</p>
      </PostArticleCanvas>
    );
    expect(screen.getByText('Post title').closest('[data-post-canvas]')).not.toBeNull();
  });

  it('Ctrl+A selects the canvas, not sibling chrome', () => {
    const { addRange } = mockSelection();
    render(
      <div>
        <nav>Cookie Policy</nav>
        <PostArticleCanvas>
          <p>Post title</p>
          <p>Post body</p>
        </PostArticleCanvas>
      </div>
    );
    fireEvent.keyDown(document, { key: 'a', ctrlKey: true });
    expect(addRange).toHaveBeenCalled();
    const range = addRange.mock.calls[0][0] as Range;
    const canvas = document.querySelector('[data-post-canvas]');
    expect(canvas).not.toBeNull();
    expect(
      range.commonAncestorContainer === canvas ||
        canvas!.contains(range.commonAncestorContainer)
    ).toBe(true);
  });

  it('leaves Ctrl+A to a chrome field', () => {
    const { addRange } = mockSelection();
    render(
      <div>
        <div data-shortcut-scope="chrome">
          <input aria-label="Search stories" defaultValue="hello" />
        </div>
        <PostArticleCanvas>
          <p>Post title</p>
        </PostArticleCanvas>
      </div>
    );
    const input = screen.getByLabelText('Search stories');
    input.focus();
    fireEvent.keyDown(input, { key: 'a', ctrlKey: true, bubbles: true });
    expect(addRange).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run:

```
pnpm test -- src/components/editor/postCanvas/PostArticleCanvas.test.tsx
```

Expected: FAIL — cannot find `./PostArticleCanvas`.

- [ ] **Step 3: Write minimal implementation**

`usePostCanvasShortcuts.ts`:

```ts
'use client';

import { useEffect, useRef } from 'react';

import type { PostUndoController } from './types';
import {
  isChromeShortcutTarget,
  selectPostCanvas,
  shouldHandleRedo,
  shouldHandleSelectAll,
  shouldHandleUndo,
  shortcutKind,
} from './shortcuts';

export function usePostCanvasShortcuts(opts: {
  canvasRef: React.RefObject<HTMLElement | null>;
  undo?: PostUndoController | null;
}): void {
  const undoRef = useRef(opts.undo);
  undoRef.current = opts.undo;
  const canvasRef = opts.canvasRef;

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      try {
        const kind = shortcutKind(event);
        if (!kind) return;

        const isChrome = isChromeShortcutTarget(event.target);
        if (isChrome) {
          event.stopPropagation();
          return;
        }

        const canvas = canvasRef.current;
        if (!canvas) return;

        const hasUndo = Boolean(undoRef.current);

        if (shouldHandleSelectAll(event, { isChrome })) {
          event.preventDefault();
          event.stopPropagation();
          selectPostCanvas(canvas);
          return;
        }

        if (shouldHandleUndo(event, { isChrome, hasUndo })) {
          event.preventDefault();
          event.stopPropagation();
          try {
            undoRef.current?.undo();
          } catch (err) {
            console.warn('post canvas undo failed', err);
          }
          return;
        }

        if (shouldHandleRedo(event, { isChrome, hasUndo })) {
          event.preventDefault();
          event.stopPropagation();
          try {
            undoRef.current?.redo();
          } catch (err) {
            console.warn('post canvas redo failed', err);
          }
        }
      } catch (err) {
        console.warn('post canvas shortcut failed', err);
      }
    };

    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, [canvasRef]);
}
```

`PostArticleCanvas.tsx`:

```tsx
'use client';

import { useRef } from 'react';

import { twMerge } from 'tailwind-merge';

import { usePostCanvasShortcuts } from './usePostCanvasShortcuts';

export function PostArticleCanvas({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  const canvasRef = useRef<HTMLDivElement>(null);
  usePostCanvasShortcuts({ canvasRef, undo: null });

  return (
    <div
      ref={canvasRef}
      data-post-canvas
      className={twMerge('select-text', className)}
    >
      {children}
    </div>
  );
}
```

- [ ] **Step 4: Run test to verify it passes**

Run the same test file. Expected: PASS.

- [ ] **Step 5: Commit**

Skip unless asked.

---

### Task 3: Mark search and publish as chrome

**Files:**
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/search/SearchInput.tsx` — outer wrapper
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/blog/actions/PublishBlogDrawer.tsx` — `DrawerContent` (~line 212)

**Interfaces:**
- Consumes: `isChromeShortcutTarget` from Task 1 (`[data-shortcut-scope="chrome"]`)
- Produces: search field and publish drawer participate in chrome shortcut targeting

- [ ] **Step 1: Write the failing test**

Do not mount the full `SearchInput` (auth/router). Extend `shortcuts.test.ts` with a markup contract test, or add `chromeScope.test.ts`:

```ts
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { describe, expect, it } from 'vitest';

const dir = dirname(fileURLToPath(import.meta.url));
const appRoot = join(dir, '../../../');

describe('chrome shortcut scope', () => {
  it('marks SearchInput and PublishBlogDrawer', () => {
    const search = readFileSync(
      join(appRoot, 'components/search/SearchInput.tsx'),
      'utf8'
    );
    const publish = readFileSync(
      join(appRoot, 'components/blog/actions/PublishBlogDrawer.tsx'),
      'utf8'
    );
    expect(search).toContain('data-shortcut-scope="chrome"');
    expect(publish).toContain('data-shortcut-scope="chrome"');
  });
});
```

Path: `postCanvas` is `src/components/editor/postCanvas`, so `join(dir, '../../../')` is `src/`. That is correct for `components/search/...`.

- [ ] **Step 2: Run test to verify it fails**

```
pnpm test -- src/components/editor/postCanvas/chromeScope.test.ts
```

Expected: FAIL — string not found.

- [ ] **Step 3: Write minimal implementation**

In `SearchInput.tsx`, on the existing outer `<div className={twMerge(className)}>`:

```tsx
<div className={twMerge(className)} data-shortcut-scope='chrome'>
```

In `PublishBlogDrawer.tsx`, change:

```tsx
<DrawerContent data-shortcut-scope='chrome'>
```

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS.

- [ ] **Step 5: Commit**

Skip unless asked.

---

### Task 4: Preview and published canvas wrap

**Files:**
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/BlogPreview.tsx`
- Modify: `local/the_monkeys/apps/the_monkeys/src/app/blog/[slug]/BlogPageClient.tsx`

**Interfaces:**
- Consumes: `PostArticleCanvas` from Task 2
- Produces: one `data-post-canvas` wrapping `BlogHeading` + read-only editor. Date, author, back, reactions stay outside.

Layout (required so the canvas is one node): date / scope / author stay in the header container; **title + body** move into `PostArticleCanvas` below that header. Author therefore sits above the title. Do not put reactions, topics, or recommendations inside the canvas.

- [ ] **Step 1: Write the failing test**

`BlogPreview` pulls auth and a dynamic editor — do not render it. Add a source contract test in `chromeScope.test.ts` (or `canvasWrap.test.ts`):

```ts
it('wraps preview and published title+body with PostArticleCanvas', () => {
  const preview = readFileSync(
    join(appRoot, 'components/editor/BlogPreview.tsx'),
    'utf8'
  );
  const published = readFileSync(
    join(appRoot, 'app/blog/[slug]/BlogPageClient.tsx'),
    'utf8'
  );
  expect(preview).toContain('PostArticleCanvas');
  expect(published).toContain('PostArticleCanvas');
});
```

- [ ] **Step 2: Run test to verify it fails**

```
pnpm test -- src/components/editor/postCanvas/chromeScope.test.ts
```

Expected: FAIL — `PostArticleCanvas` missing.

- [ ] **Step 3: Write minimal implementation**

`BlogPreview.tsx` — import `PostArticleCanvas`. Keep date + `UserInfoCardBlogPage` in the first `Container`. Wrap heading + editor:

```tsx
import { PostArticleCanvas } from '@/components/editor/postCanvas/PostArticleCanvas';

// inside the component return:
<>
  <div className='px-4'>
    <Container className='pt-4 sm:pt-6 pb-6 max-w-3xl flex flex-col items-center gap-3 border-b-1 border-border-light/80 dark:border-border-dark/80'>
      <p className='text-sm opacity-90'>
        {moment(date).format('MMM DD, yyyy')}
        {' / '}
        {moment(date).utc().format('hh:mm A')} UTC
      </p>

      <UserInfoCardBlogPage id={session?.account_id} />
    </Container>
  </div>
  <PostArticleCanvas>
    <div className='px-4'>
      <Container className='max-w-3xl'>
        <BlogHeading
          title={sanitizedBlogTitle || 'Untitled Post'}
          className='pt-1 pb-4 font-dm_sans font-semibold text-[28px] sm:text-3xl md:text-4xl !leading-[1.32] text-center'
        />
      </Container>
    </div>
    <div className='p-4'>
      <Container className='max-w-3xl'>
        <div className='px-1 pb-4 overflow-hidden'>
          <Editor key={urlBlogId} data={blogDataWithoutHeading()} />
        </div>
      </Container>
    </div>
  </PostArticleCanvas>
</>
```

`BlogPageClient.tsx` — same pattern: keep Back, Edit, date, `BlogScopeLine`, and `UserInfoCardBlogPage` outside. Wrap `BlogHeading` + preview `Editor` in `PostArticleCanvas`. Leave `BlogReactionsContainer` and everything below it outside.

```tsx
import { PostArticleCanvas } from '@/components/editor/postCanvas/PostArticleCanvas';
```

After the header `Container` that ends with `UserInfoCardBlogPage`, close that section, then:

```tsx
<PostArticleCanvas>
  <div className='px-4'>
    <Container className='max-w-3xl'>
      <BlogHeading
        title={sanitizedBlogTitle || 'Untitled Post'}
        className='pt-1 pb-4 font-dm_sans font-semibold text-[28px] sm:text-3xl md:text-4xl !leading-[1.32] text-center'
      />
    </Container>
  </div>
  <div className='p-4'>
    <Container className='max-w-3xl'>
      <div className='px-1 pb-4 overflow-hidden'>
        <Editor key={blogId} data={blogDataWithoutHeading()} />
      </div>
      {/* reactions stay a sibling, not inside canvas — put them after PostArticleCanvas */}
    </Container>
  </div>
</PostArticleCanvas>
```

Keep `BlogReactionsContainer` **after** `</PostArticleCanvas>`, still inside the `max-w-3xl` page column. If that requires an extra `Container` around reactions, duplicate the existing `Container className='max-w-3xl'` wrapper for the reactions block rather than stuffing reactions into the canvas.

Do not move `BlogHeading` out of the published header until the canvas wrap is in place; the heading must not appear twice.

- [ ] **Step 4: Run test to verify it passes**

Expected: PASS. Also run `pnpm test -- src/components/editor/postCanvas` to keep Task 1–2 green.

- [ ] **Step 5: Commit**

Skip unless asked.

---

### Task 5: Attach `editorjs-undo` on the edit editor

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/attachEditorUndo.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/attachEditorUndo.test.ts`
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/index.tsx`
- Modify: `local/the_monkeys/apps/the_monkeys/package.json` (dependency `editorjs-undo`)

**Interfaces:**
- Consumes: EditorJS instance with `isReady: Promise<unknown>`; `PostUndoHandle`; `usePostCanvasShortcuts`
- Produces:
  - `export type UndoPluginCtor = new (opts: { editor: unknown; maxLength?: number; onUpdate?: () => void; config?: { shortcuts?: { undo?: string; redo?: string } } }) => { undo: () => void; redo: () => void; initialize: (data: unknown) => void; destroy?: () => void; canUndo?: boolean | (() => boolean); canRedo?: boolean | (() => boolean) }`
  - `export async function attachEditorUndo(opts: { editor: { isReady: Promise<unknown> }; initialData: unknown; onHandleChange: (handle: PostUndoHandle | null) => void; loadUndo?: () => Promise<{ default: UndoPluginCtor }> }): Promise<() => void>`
  - `EditorProps` adds optional `onUndoHandleChange?: (handle: PostUndoHandle | null) => void`
  - Holder div: `data-post-canvas`, `select-text`, `ref` for the hook

- [ ] **Step 1: Add the dependency**

From `local/the_monkeys/apps/the_monkeys`:

```
pnpm add editorjs-undo
```

If pnpm store errors, add `"editorjs-undo": "^2.5.4"` to `dependencies` in `package.json` the same way `marked` was added, then `pnpm install`.

- [ ] **Step 2: Write the failing test**

`attachEditorUndo.test.ts`:

```ts
import { describe, expect, it, vi } from 'vitest';

import { attachEditorUndo } from './attachEditorUndo';

function makeUndoClass() {
  const undo = vi.fn();
  const redo = vi.fn();
  const initialize = vi.fn();
  const destroy = vi.fn();

  class UndoMock {
    undo = undo;
    redo = redo;
    initialize = initialize;
    destroy = destroy;
    canUndo = false;
    canRedo = false;
    constructor(public opts: { onUpdate?: () => void }) {}
  }

  return { UndoMock, undo, redo, initialize, destroy };
}

describe('attachEditorUndo', () => {
  it('initializes history and reports a handle', async () => {
    const { UndoMock, initialize, destroy } = makeUndoClass();
    const onHandleChange = vi.fn();
    const cleanup = await attachEditorUndo({
      editor: { isReady: Promise.resolve() },
      initialData: { blocks: [] },
      onHandleChange,
      loadUndo: async () => ({ default: UndoMock }),
    });
    expect(initialize).toHaveBeenCalledWith({ blocks: [] });
    expect(onHandleChange).toHaveBeenCalled();
    const handle = onHandleChange.mock.calls[0][0];
    expect(handle).toMatchObject({ canUndo: false, canRedo: false });
    cleanup();
    expect(destroy).toHaveBeenCalled();
    expect(onHandleChange).toHaveBeenLastCalledWith(null);
  });

  it('reports null when the plugin fails to load', async () => {
    const warn = vi.spyOn(console, 'warn').mockImplementation(() => {});
    const onHandleChange = vi.fn();
    const cleanup = await attachEditorUndo({
      editor: { isReady: Promise.resolve() },
      initialData: {},
      onHandleChange,
      loadUndo: async () => {
        throw new Error('fail');
      },
    });
    expect(onHandleChange).toHaveBeenCalledWith(null);
    cleanup();
    warn.mockRestore();
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

```
pnpm test -- src/components/editor/postCanvas/attachEditorUndo.test.ts
```

Expected: FAIL — cannot find `./attachEditorUndo`.

- [ ] **Step 4: Write minimal implementation**

`attachEditorUndo.ts`:

```ts
import type { PostUndoHandle } from './types';

export type UndoPluginCtor = new (opts: {
  editor: unknown;
  maxLength?: number;
  onUpdate?: () => void;
  config?: { shortcuts?: { undo?: string; redo?: string } };
}) => {
  undo: () => void;
  redo: () => void;
  initialize: (data: unknown) => void;
  destroy?: () => void;
  canUndo?: boolean | (() => boolean);
  canRedo?: boolean | (() => boolean);
};

function flag(value: boolean | (() => boolean) | undefined): boolean {
  try {
    if (typeof value === 'function') return Boolean(value());
    return Boolean(value);
  } catch {
    return false;
  }
}

function toHandle(instance: InstanceType<UndoPluginCtor>): PostUndoHandle {
  return {
    undo: () => {
      try {
        instance.undo();
      } catch (err) {
        console.warn('editor undo failed', err);
      }
    },
    redo: () => {
      try {
        instance.redo();
      } catch (err) {
        console.warn('editor redo failed', err);
      }
    },
    get canUndo() {
      return flag(instance.canUndo);
    },
    get canRedo() {
      return flag(instance.canRedo);
    },
  };
}

export async function attachEditorUndo(opts: {
  editor: { isReady: Promise<unknown> };
  initialData: unknown;
  onHandleChange: (handle: PostUndoHandle | null) => void;
  loadUndo?: () => Promise<{ default: UndoPluginCtor }>;
}): Promise<() => void> {
  let instance: InstanceType<UndoPluginCtor> | null = null;
  const load =
    opts.loadUndo ?? (() => import('editorjs-undo') as Promise<{ default: UndoPluginCtor }>);

  try {
    await opts.editor.isReady;
    const mod = await load();
    const Undo = mod.default;
    instance = new Undo({
      editor: opts.editor,
      maxLength: 30,
      onUpdate() {
        if (instance) opts.onHandleChange(toHandle(instance));
      },
      config: {
        shortcuts: { undo: '', redo: '' },
      },
    });
    instance.initialize(opts.initialData);
    opts.onHandleChange(toHandle(instance));
  } catch (err) {
    console.warn('editorjs-undo failed', err);
    opts.onHandleChange(null);
  }

  return () => {
    try {
      instance?.destroy?.();
    } catch (err) {
      console.warn('editorjs-undo destroy failed', err);
    }
    opts.onHandleChange(null);
  };
}
```

Note: `PostUndoHandle` in the spec uses data properties, not getters. Tests use `toMatchObject({ canUndo: false })`, which works with getters. `EditUndoRedoButtons` will read `handle.canUndo` the same way. When `onUpdate` fires, pass a **plain object snapshot** so React state updates:

Change `toHandle` to return a snapshot, not getters:

```ts
function toHandle(instance: InstanceType<UndoPluginCtor>): PostUndoHandle {
  return {
    undo: () => {
      try {
        instance.undo();
      } catch (err) {
        console.warn('editor undo failed', err);
      }
    },
    redo: () => {
      try {
        instance.redo();
      } catch (err) {
        console.warn('editor redo failed', err);
      }
    },
    canUndo: flag(instance.canUndo),
    canRedo: flag(instance.canRedo),
  };
}
```

Use this snapshot version (not getters) so `setState` in the edit page re-renders buttons.

Then wire `index.tsx`:

1. Import `PostUndoHandle` from `./postCanvas/types`, `usePostCanvasShortcuts`, `attachEditorUndo`.
2. Extend props:

```ts
export type EditorProps = {
  blogId: string;
  data: OutputData;
  onChange: (data: OutputData) => void;
  onUndoHandleChange?: (handle: PostUndoHandle | null) => void;
};
```

3. Inside the memo component, add `canvasRef` and local undo controller for the hook:

```ts
const canvasRef = useRef<HTMLDivElement>(null);
const undoControllerRef = useRef<PostUndoController | null>(null);
usePostCanvasShortcuts({
  canvasRef,
  undo: undoControllerRef.current,
});
```

`undo: undoControllerRef.current` is stale on first render (always null) because the hook copies into `undoRef` each render — **pass a stable wrapper instead:**

```ts
const undoForHook = useRef<PostUndoController>({
  undo: () => undoControllerRef.current?.undo(),
  redo: () => undoControllerRef.current?.redo(),
});
usePostCanvasShortcuts({
  canvasRef,
  undo: undoForHook.current,
});
```

Wait: `hasUndo` is `Boolean(undoRef.current)`, which would always be true if we pass a wrapper. The spec: Preview passes no undo so Z/Y are native. Edit must pass a controller only when the plugin attached.

Use:

```ts
const [undoReady, setUndoReady] = useState(false);
const undoControllerRef = useRef<PostUndoController | null>(null);
usePostCanvasShortcuts({
  canvasRef,
  undo: undoReady
    ? {
        undo: () => undoControllerRef.current?.undo(),
        redo: () => undoControllerRef.current?.redo(),
      }
    : null,
});
```

Creating a new object each render is OK because the hook stores it in `undoRef.current` every render.

4. In the existing editor `useEffect`, after `new MonkeysEditor(...)`, call attach (do not change debounce/save):

```ts
let detachUndo: (() => void) | undefined;
const editor = editorInstance.current;
if (editor) {
  void attachEditorUndo({
    editor,
    initialData: data,
    onHandleChange: (handle) => {
      undoControllerRef.current = handle
        ? { undo: handle.undo, redo: handle.redo }
        : null;
      setUndoReady(Boolean(handle));
      onUndoHandleChange?.(handle);
    },
  }).then((detach) => {
    detachUndo = detach;
  });
}
```

Cleanup of that effect already destroys the editor. Also:

```ts
return () => {
  detachUndo?.();
  undoControllerRef.current = null;
  setUndoReady(false);
  onUndoHandleChange?.(null);
  if (debounceTimer.current) clearTimeout(debounceTimer.current);
  if (editorInstance.current && editorInstance.current.destroy) {
    editorInstance.current.destroy();
    editorInstance.current = null;
  }
};
```

`detachUndo` must be let-bound in the effect so cleanup can call it. If attach resolves after unmount, `attachEditorUndo` still returns cleanup — store it on a ref:

```ts
const detachUndoRef = useRef<(() => void) | null>(null);
// in effect:
let cancelled = false;
void attachEditorUndo({...}).then((detach) => {
  if (cancelled) {
    detach();
    return;
  }
  detachUndoRef.current = detach;
});
return () => {
  cancelled = true;
  detachUndoRef.current?.();
  detachUndoRef.current = null;
  // existing destroy...
};
```

5. Holder:

```tsx
return (
  <div
    ref={canvasRef}
    className='w-full px-4 space-y-6 select-text'
    id='monkeys_editor_editor-container'
    data-post-canvas
  ></div>
);
```

Do not rename the id. Do not add undo to `getEditorConfig` tools.

`onUndoHandleChange` should be in the effect dependency array. The edit page will pass `setState`, which is stable.

- [ ] **Step 5: Run tests to verify they pass**

```
pnpm test -- src/components/editor/postCanvas
```

Expected: PASS (including attach tests).

- [ ] **Step 6: Commit**

Skip unless asked.

---

### Task 6: Undo/Redo buttons on the edit bar

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/EditUndoRedoButtons.tsx`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/postCanvas/EditUndoRedoButtons.test.tsx`
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/icon.tsx` — add `'RiArrowGoBack' | 'RiArrowGoForward'` to `IconName`
- Modify: `local/the_monkeys/apps/the_monkeys/src/app/edit/[blogId]/page.tsx`

**Interfaces:**
- Consumes: `PostUndoHandle` from Task 1; `onUndoHandleChange` from Task 5
- Produces: `export function EditUndoRedoButtons(props: { handle: PostUndoHandle | null }): JSX.Element | null` — `null` when `handle` is null; otherwise two `min-h-11 min-w-11` buttons, `aria-label` “Undo” / “Redo”, disabled from `canUndo` / `canRedo`

- [ ] **Step 1: Write the failing test**

`EditUndoRedoButtons.test.tsx`:

```tsx
import { cleanup, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, describe, expect, it, vi } from 'vitest';

import { EditUndoRedoButtons } from './EditUndoRedoButtons';
import type { PostUndoHandle } from './types';

afterEach(cleanup);

describe('EditUndoRedoButtons', () => {
  it('renders nothing when the plugin is missing', () => {
    const { container } = render(<EditUndoRedoButtons handle={null} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows disabled undo/redo at start', () => {
    const handle: PostUndoHandle = {
      undo: vi.fn(),
      redo: vi.fn(),
      canUndo: false,
      canRedo: false,
    };
    render(<EditUndoRedoButtons handle={handle} />);
    expect(screen.getByRole('button', { name: 'Undo' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Redo' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Undo' }).className).toMatch(
      /min-h-11/
    );
  });

  it('invokes undo when enabled', async () => {
    const user = userEvent.setup();
    const handle: PostUndoHandle = {
      undo: vi.fn(),
      redo: vi.fn(),
      canUndo: true,
      canRedo: false,
    };
    render(<EditUndoRedoButtons handle={handle} />);
    await user.click(screen.getByRole('button', { name: 'Undo' }));
    expect(handle.undo).toHaveBeenCalled();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
pnpm test -- src/components/editor/postCanvas/EditUndoRedoButtons.test.tsx
```

Expected: FAIL — module not found.

- [ ] **Step 3: Write minimal implementation**

Add to `IconName` in `icon.tsx` (keep alphabetic-ish, next to `RiArrowLeft`):

```ts
| 'RiArrowGoBack'
| 'RiArrowGoForward'
```

`EditUndoRedoButtons.tsx`:

```tsx
'use client';

import Icon from '@/components/icon';
import { Button } from '@the-monkeys/ui/atoms/button';

import type { PostUndoHandle } from './types';

export function EditUndoRedoButtons({
  handle,
}: {
  handle: PostUndoHandle | null;
}) {
  if (!handle) return null;

  return (
    <div className='flex items-center gap-1'>
      <Button
        type='button'
        variant='ghost'
        aria-label='Undo'
        disabled={!handle.canUndo}
        onClick={() => handle.undo()}
        className='min-h-11 min-w-11 p-0'
      >
        <Icon name='RiArrowGoBack' size={18} />
      </Button>
      <Button
        type='button'
        variant='ghost'
        aria-label='Redo'
        disabled={!handle.canRedo}
        onClick={() => handle.redo()}
        className='min-h-11 min-w-11 p-0'
      >
        <Icon name='RiArrowGoForward' size={18} />
      </Button>
    </div>
  );
}
```

If `Button` has no `variant='ghost'`, use the same variant as the Edit tab / `EditBlogDialog` (`buttonVariant={'ghost'}`). If TypeScript fails on `ghost`, use `variant='outline'` but keep `min-h-11`.

On `page.tsx`:

1. Import `useState` (already imported), `EditUndoRedoButtons`, `PostUndoHandle`.
2. Add `const [undoHandle, setUndoHandle] = useState<PostUndoHandle | null>(null);`
3. When switching to preview, the Editor unmounts and should call `onUndoHandleChange(null)`. Still hide buttons with `!isPreviewMode`.
4. Pass `onUndoHandleChange={setUndoHandle}` into `<Editor ... />`.
5. Change the top bar to allow wrap and insert buttons after Online:

```tsx
<div className='pt-4 pb-3 flex flex-wrap justify-between items-center gap-2'>
  <div className='flex items-center gap-2'>
    <div className={twMerge('px-[10px] py-[1px] flex items-center gap-1 border-1 rounded-full', ...)}>
      {/* existing Online pill */}
    </div>
    {!isPreviewMode ? <EditUndoRedoButtons handle={undoHandle} /> : null}
  </div>
  {/* existing Tabs */}
  {/* existing PublishBlogDrawer */}
</div>
```

Do not show the buttons on Preview. Do not add them to `BlogPageClient`.

- [ ] **Step 4: Run tests to verify they pass**

```
pnpm test -- src/components/editor/postCanvas
```

Expected: all postCanvas tests PASS.

- [ ] **Step 5: Manual check (required before claiming done)**

Dev server: `local/the_monkeys/apps/the_monkeys`, `npm run dev` or `pnpm dev` on `http://localhost:3000`.

1. `/edit/:id` Edit: `Ctrl+A` selects title + blocks, not nav/footer. `Ctrl+Z` undoes adding a paragraph. Redo with `Ctrl+Y` or `Ctrl+Shift+Z`. Search field `Ctrl+A` / `Ctrl+Z` stay native.
2. Preview tab: `Ctrl+A` selects title + body only. No Undo/Redo buttons.
3. Published `/blog/:slug`: same Select All. No Undo/Redo buttons.
4. ~360px width: Undo/Redo visible, 44px, bar may wrap.
5. No Application-error white screen; no new overlay crash from this feature.

- [ ] **Step 6: Commit**

Skip unless asked.

---

## Self-review (plan vs spec)

| Spec | Task |
| --- | --- |
| Docs Select All on edit / Preview / published | 2, 4, 5 |
| Chrome search / drawer native shortcuts | 1, 2, 3 |
| Document undo/redo edit only + `editorjs-undo` | 5 |
| Disable plugin keybinder; our capture listener | 1, 2, 5 (`shortcuts: { undo: '', redo: '' }`) |
| No `execCommand('selectAll')` | 1 `selectPostCanvas` |
| `user-select: text` on canvas, not none on body | 2, 5 (`select-text`) |
| Phone Undo/Redo on existing bar, hidden if plugin missing | 6 |
| No JSON / API change | all |
| Plugin failure does not white-screen | 5 tests + try/catch |
| Vitest helpers + canvas + buttons; no real EditorJS | 1, 2, 5, 6 |
| Manual `/edit` + `/blog` | Task 6 Step 5 |
| Author/date excluded from canvas | Task 4 layout |
