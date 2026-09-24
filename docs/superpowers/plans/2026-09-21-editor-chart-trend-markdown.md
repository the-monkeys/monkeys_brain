# Editor Chart, Trend, and Markdown Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Chart and Trend visible and usable on a phone (graphic first, one-line how-to, collapsed data form) without changing saved JSON, and add a Markdown block that accepts paste or a `.md` file.

**Architecture:** Keep EditorJS + `createBlock`. Extract Chart SVG and Trend math into pure modules for tests. Add `MarkdownBlock` with lazy `marked` + existing `isomorphic-dompurify`. Register the tool in edit and read-only configs. No engine changes.

**Tech Stack:** React 18, EditorJS (`@themonkeys/monkeys-editor`), Vitest + Testing Library, Tailwind, `marked`, `isomorphic-dompurify`.

**Spec:** `docs/superpowers/specs/2026-09-21-editor-chart-trend-markdown-design.md`

## Global Constraints

- Frontend only under `local/the_monkeys/apps/the_monkeys`. Engine, proto, ES, gateway: do not touch.
- Backward compatible: do not rename `chart` / `trend` or add required fields to `ChartBlockData` / `TrendBlockData`.
- Mobile-first: default layout is one column, graphic before form, tap targets `min-h-11` (44px), no hover-only help.
- Do not rewrite Chart with D3. Fix comments that claim D3.
- Do not convert Markdown into other EditorJS blocks. Do not use `@mdx-js/*` for writer content.
- Do not change feed card excerpts.
- PowerShell: never chain with `&&`. Use `;` or separate commands.
- Do not git commit unless the user explicitly asks. Skip every commit step until then.
- Tests run from `local/the_monkeys/apps/the_monkeys` with `pnpm test -- <file>`.

### File map

| File | Responsibility |
| --- | --- |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/chartSvg.ts` | Hand-rolled SVG/pie HTML; axis fill `currentColor` |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/ChartComponent.tsx` | Preview-first UI, how-to, collapsed Edit data |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/index.ts` | `showLegend: true` in sanitize |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TrendBlock/trendMath.ts` | `computeTrend` + sparkline SVG |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TrendBlock/TrendComponent.tsx` | Sparkline-first UI, how-to, collapsed Edit data |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/shared/BlockWrapper.tsx` | Shared `BlockHelp` + `EditDataDetails` |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/shared/types.ts` | `MarkdownBlockData` + `MARKDOWN_TOOLBOX` |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/markdownRender.ts` | `marked` + DOMPurify |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/markdownFile.ts` | Size/type validation |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/MarkdownComponent.tsx` | Write/Preview + upload |
| `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/index.ts` | `createBlock` wrapper |
| `local/the_monkeys/apps/the_monkeys/src/config/editor/monkeys_editor.config.ts` | Register `markdown` |
| `local/the_monkeys/apps/the_monkeys/src/config/editor/monkeys_editor_readonly.config.ts` | Register `markdown` |

---

### Task 1: Extract Chart SVG with contrast colors

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/chartSvg.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/chartSvg.test.ts`
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/ChartComponent.tsx` (import `generateChartMarkup`; delete local `generateSVG` / `renderCartesianSVG` / `renderPieSVG` / `getNice` / `fmtAxis`; keep `ChartPreview` wrapper)

**Interfaces:**
- Consumes: `ChartBlockData`, `PALETTES` from `../shared/types`
- Produces: `export function generateChartMarkup(data: ChartBlockData): string`

- [ ] **Step 1: Write the failing test**

```ts
import { describe, expect, it } from 'vitest';

import type { ChartBlockData } from '../shared/types';
import { generateChartMarkup } from './chartSvg';

const sample: ChartBlockData = {
  type: 'line',
  title: '',
  xLabel: '',
  yLabel: '',
  showLegend: true,
  palette: 'ocean',
  labels: ['Jan', 'Feb', 'Mar'],
  series: [{ name: 'Series A', values: [12, 24, 18] }],
  source: 'manual',
};

describe('generateChartMarkup', () => {
  it('emits a full-width SVG and does not use the old faint axis color', () => {
    const html = generateChartMarkup(sample);
    expect(html).toContain('<svg');
    expect(html).toContain('viewBox="0 0 600 300"');
    expect(html).toContain('width:100%');
    expect(html).toContain('currentColor');
    expect(html).not.toContain('rgba(100,116,139,0.7)');
  });

  it('keeps a bar chart working from old JSON', () => {
    const html = generateChartMarkup({
      ...sample,
      type: 'bar',
      labels: ['A'],
      series: [{ name: 'S', values: [1] }],
    });
    expect(html).toContain('<rect');
  });

  it('renders pie as a conic-gradient div, not an empty string', () => {
    const html = generateChartMarkup({
      ...sample,
      type: 'pie',
      labels: ['A', 'B'],
      series: [{ name: 'S', values: [1, 2] }],
    });
    expect(html).toContain('conic-gradient');
    expect(html).not.toContain('<svg');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run from `local/the_monkeys/apps/the_monkeys`:

```
pnpm test -- src/components/editor/customBlocks/ChartBlock/chartSvg.test.ts
```

Expected: FAIL — `chartSvg.ts` is not defined.

- [ ] **Step 3: Write minimal implementation**

Move the existing `generateSVG`, `renderCartesianSVG`, `renderPieSVG`, `getNice`, and `fmtAxis` from `ChartComponent.tsx` into `chartSvg.ts`. Export `generateChartMarkup` as the old `generateSVG`.

In `renderCartesianSVG`, change axis tick `<text>` `fill="rgba(100,116,139,0.7)"` to `fill="currentColor"`. Keep grid/axis strokes as `rgba(148,163,184,0.25)` and `rgba(148,163,184,0.5)` (those already read on dark). Keep `style="display:block;width:100%;height:auto;"` on the SVG.

In `ChartComponent.tsx`:
- `import { generateChartMarkup } from './chartSvg';`
- `ChartPreview` uses `generateChartMarkup(data)`
- Wrap the `dangerouslySetInnerHTML` container with `className` that includes `text-slate-600 dark:text-slate-300` so `currentColor` follows the theme
- Change the file comment “Uses D3” / `ChartPreview` comment “Uses D3 library” to “Hand-rolled SVG, not D3”

Do not change `ChartBlockData`. Do not import `d3`.

- [ ] **Step 4: Run the tests and make sure they pass**

```
pnpm test -- src/components/editor/customBlocks/ChartBlock/chartSvg.test.ts
```

Expected: PASS (3 tests).

- [ ] **Step 5: Commit** (skip unless the user asks)

```
git add local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/chartSvg.ts local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/chartSvg.test.ts local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/ChartComponent.tsx
git commit -m "fix: make chart SVG readable in dark mode without D3"
```

Note: `local/the_monkeys` is gitignored from the engine repo. Commits happen in `local/the_monkeys` on `codex/group-scoped-blogs`.

---

### Task 2: Shared help + Edit data chrome

**Files:**
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/shared/BlockWrapper.tsx`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/shared/BlockWrapper.test.tsx`

**Interfaces:**
- Consumes: existing `cn`, ReactNode
- Produces:
  - `export function BlockHelp({ children }: { children: React.ReactNode }): JSX.Element`
  - `export function EditDataDetails({ children }: { children: React.ReactNode }): JSX.Element`

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import { BlockHelp, EditDataDetails } from './BlockWrapper';

describe('BlockHelp', () => {
  it('renders help as a sentence, not a tooltip', () => {
    render(<BlockHelp>Paste CSV (first row = headers)</BlockHelp>);
    const help = screen.getByText('Paste CSV (first row = headers)');
    expect(help.tagName).toBe('P');
    expect(help.className).toContain('text-sm');
  });
});

describe('EditDataDetails', () => {
  it('starts collapsed with a 44px tap target', () => {
    render(
      <EditDataDetails>
        <input aria-label='hidden-field' />
      </EditDataDetails>
    );
    const details = screen.getByText('Edit data').closest('details');
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute('open');
    const summary = details!.querySelector('summary');
    expect(summary?.className).toMatch(/min-h-11/);
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
pnpm test -- src/components/editor/customBlocks/shared/BlockWrapper.test.tsx
```

Expected: FAIL — `BlockHelp` / `EditDataDetails` are not exported.

- [ ] **Step 3: Write minimal implementation**

Append to `BlockWrapper.tsx`:

```tsx
export function BlockHelp({ children }: { children: ReactNode }) {
  return (
    <p className='mb-3 text-sm leading-5 text-slate-600 dark:text-slate-300'>
      {children}
    </p>
  );
}

export function EditDataDetails({ children }: { children: ReactNode }) {
  return (
    <details className='mt-3'>
      <summary className='flex min-h-11 cursor-pointer list-none items-center text-sm font-medium text-slate-700 dark:text-slate-200'>
        Edit data
      </summary>
      <div className='mt-3 grid grid-cols-1 gap-3 sm:grid-cols-2'>{children}</div>
    </details>
  );
}
```

- [ ] **Step 4: Run the tests and make sure they pass**

```
pnpm test -- src/components/editor/customBlocks/shared/BlockWrapper.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 3: Chart preview-first layout + persist legend

**Files:**
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/ChartComponent.tsx`
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/index.ts` (`showLegend: true`)
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/ChartBlock/ChartComponent.test.tsx`

**Interfaces:**
- Consumes: `BlockHelp`, `EditDataDetails` from Task 2; `generateChartMarkup` from Task 1
- Produces: same `ChartComponent({ data, readOnly, onChange })` props; saved data still `ChartBlockData`

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { ChartBlockData } from '../shared/types';
import ChartComponent from './ChartComponent';

const sample: ChartBlockData = {
  type: 'line',
  title: 'Revenue',
  xLabel: '',
  yLabel: '',
  showLegend: true,
  palette: 'ocean',
  labels: ['Jan', 'Feb', 'Mar'],
  series: [{ name: 'Series A', values: [12, 24, 18] }],
  source: 'manual',
};

describe('ChartComponent', () => {
  it('shows how-to and a preview in edit, with Edit data collapsed', () => {
    render(
      <ChartComponent data={sample} readOnly={false} onChange={() => {}} />
    );
    expect(
      screen.getByText(
        'Paste CSV (first row = headers) or one series per line: Revenue:10,20,30'
      )
    ).toBeInTheDocument();
    expect(screen.getByText('Edit data').closest('details')).not.toHaveAttribute(
      'open'
    );
    expect(document.querySelector('svg')).not.toBeNull();
  });

  it('hides how-to and Edit data in readOnly', () => {
    render(
      <ChartComponent data={sample} readOnly={true} onChange={() => {}} />
    );
    expect(
      screen.queryByText(
        'Paste CSV (first row = headers) or one series per line: Revenue:10,20,30'
      )
    ).not.toBeInTheDocument();
    expect(screen.queryByText('Edit data')).not.toBeInTheDocument();
    expect(document.querySelector('svg')).not.toBeNull();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
pnpm test -- src/components/editor/customBlocks/ChartBlock/ChartComponent.test.tsx
```

Expected: FAIL — how-to text is missing; form is not inside collapsed `details`.

- [ ] **Step 3: Write minimal implementation**

In `ChartComponent.tsx` render order inside `BlockWrapper`:

1. Title row (keep existing title + type badge)
2. If `!readOnly`, `<BlockHelp>Paste CSV (first row = headers) or one series per line: Revenue:10,20,30</BlockHelp>`
3. Chart preview (existing `ChartPreview` / EmptyState) + axis labels + legend (unchanged)
4. If `!readOnly`, wrap the **existing** form fields (type, palette, title, legend switch, axes, labels, series, CSV) in `<EditDataDetails>`. Keep `sm:col-span-2` on labels/series/CSV fields. Change Parse CSV button class from `w-fit` to `w-full sm:w-auto`.

In `ChartBlock/index.ts` sanitize, change `showLegend: false` to `showLegend: true`. Do not change `DEFAULT_DATA` or `normalizeData`.

- [ ] **Step 4: Run the tests and make sure they pass**

```
pnpm test -- src/components/editor/customBlocks/ChartBlock/ChartComponent.test.tsx src/components/editor/customBlocks/ChartBlock/chartSvg.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 4: Extract Trend math and full-width sparkline

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TrendBlock/trendMath.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TrendBlock/trendMath.test.ts`
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TrendBlock/index.ts` (import `computeTrend` from `trendMath`; delete local copy)
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TrendBlock/TrendComponent.tsx` (import `computeTrend` and `buildSparklineSvg`; delete local `computeTrend` / round helpers / inline sparkline builder)

**Interfaces:**
- Consumes: `TrendBlockData`
- Produces:
  - `export function computeTrend(periodLabels: string[], values: number[]): TrendBlockData`
  - `export function buildSparklineSvg(values: number[], direction: TrendBlockData['direction']): string`

- [ ] **Step 1: Write the failing test**

```ts
import { describe, expect, it } from 'vitest';

import { buildSparklineSvg, computeTrend } from './trendMath';

describe('computeTrend', () => {
  it('matches the stored default sample', () => {
    const t = computeTrend(['Jan', 'Feb', 'Mar'], [100, 118, 121]);
    expect(t.direction).toBe('up');
    expect(t.percentChange).toBe(21);
    expect(t.delta).toBe(21);
  });

  it('returns flat with empty values', () => {
    const t = computeTrend(['Jan'], []);
    expect(t.direction).toBe('flat');
    expect(t.values).toEqual([]);
    expect(t.percentChange).toBeNull();
  });
});

describe('buildSparklineSvg', () => {
  it('is full width and does not cap at 200px', () => {
    const svg = buildSparklineSvg([100, 118, 121], 'up');
    expect(svg).toContain('<svg');
    expect(svg).toContain('width:100%');
    expect(svg).not.toContain('max-width:200px');
  });

  it('returns empty string for a single point', () => {
    expect(buildSparklineSvg([10], 'flat')).toBe('');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
pnpm test -- src/components/editor/customBlocks/TrendBlock/trendMath.test.ts
```

Expected: FAIL — module missing.

- [ ] **Step 3: Write minimal implementation**

`trendMath.ts`: move `FLAT_THRESHOLD_PCT`, `round1`, and `computeTrend` from `TrendBlock/index.ts` and `TrendComponent.tsx` (keep the existing summary strings exactly). Add:

```ts
export function buildSparklineSvg(
  values: number[],
  direction: TrendBlockData['direction']
): string {
  if (!values || values.length < 2) return '';
  const w = 200;
  const h = 50;
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const count = values.length;
  const pts = values
    .map((v, i) => {
      const x = (i / (count - 1)) * w;
      const y = h - ((v - min) / range) * h;
      return `${x},${y}`;
    })
    .join(' ');
  const color =
    direction === 'up' ? '#22c55e' : direction === 'down' ? '#ef4444' : '#a3a3a3';
  return `<svg viewBox="0 0 ${w} ${h}" xmlns="http://www.w3.org/2000/svg" style="width:100%;height:50px;"><polyline points="${pts}" fill="none" stroke="${color}" stroke-width="2" stroke-linejoin="round"/></svg>`;
}
```

`index.ts` `normalizeData` still calls `computeTrend(periodLabels, values)`. Do not change `DEFAULT_DATA`.

- [ ] **Step 4: Run the tests and make sure they pass**

```
pnpm test -- src/components/editor/customBlocks/TrendBlock/trendMath.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 5: Trend preview-first layout and plain badges

**Files:**
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TrendBlock/TrendComponent.tsx`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/TrendBlock/TrendComponent.test.tsx`

**Interfaces:**
- Consumes: `computeTrend`, `buildSparklineSvg` from Task 4; `BlockHelp`, `EditDataDetails` from Task 2
- Produces: same `TrendComponent` props; saved data still `TrendBlockData`

- [ ] **Step 1: Write the failing test**

```tsx
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';

import type { TrendBlockData } from '../shared/types';
import TrendComponent from './TrendComponent';

const sample: TrendBlockData = {
  periodLabels: ['Jan', 'Feb', 'Mar'],
  values: [100, 118, 121],
  direction: 'up',
  percentChange: 21,
  delta: 21,
  summary: 'Trend is up by 21.0% over this period.',
};

describe('TrendComponent', () => {
  it('shows how-to, plain badges, sparkline, and collapsed Edit data', () => {
    render(
      <TrendComponent data={sample} readOnly={false} onChange={() => {}} />
    );
    expect(
      screen.getByText(
        'Comma-separated values. Optional labels. Percent and direction are calculated for you.'
      )
    ).toBeInTheDocument();
    expect(screen.getByText('Up')).toBeInTheDocument();
    expect(screen.getByText('+21%')).toBeInTheDocument();
    expect(screen.getByText('+21')).toBeInTheDocument();
    expect(screen.queryByText(/Direction:/)).not.toBeInTheDocument();
    expect(screen.getByText('Edit data').closest('details')).not.toHaveAttribute(
      'open'
    );
    const svg = document.querySelector('svg');
    expect(svg?.getAttribute('style') ?? svg?.outerHTML).toContain('width:100%');
    expect(svg?.outerHTML).not.toContain('max-width:200px');
  });

  it('hides how-to in readOnly', () => {
    render(
      <TrendComponent data={sample} readOnly={true} onChange={() => {}} />
    );
    expect(screen.queryByText('Edit data')).not.toBeInTheDocument();
    expect(
      screen.queryByText(
        'Comma-separated values. Optional labels. Percent and direction are calculated for you.'
      )
    ).not.toBeInTheDocument();
    expect(screen.getByText('Up')).toBeInTheDocument();
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
pnpm test -- src/components/editor/customBlocks/TrendBlock/TrendComponent.test.tsx
```

Expected: FAIL — how-to missing; badges still say `Direction: up`.

- [ ] **Step 3: Write minimal implementation**

Render order:

1. Title `Trend` (keep “Trend Insight” **or** change visible heading to `Trend` — use **Trend** to match the spec title row)
2. If `!readOnly`, `<BlockHelp>Comma-separated values. Optional labels. Percent and direction are calculated for you.</BlockHelp>`
3. Sparkline card: `buildSparklineSvg(internal.values, internal.direction)` via `dangerouslySetInnerHTML`; badges:

```tsx
const directionLabel =
  internal.direction === 'up'
    ? 'Up'
    : internal.direction === 'down'
      ? 'Down'
      : 'Flat';
const pctLabel =
  internal.percentChange === null
    ? 'N/A'
    : `${internal.percentChange > 0 ? '+' : ''}${internal.percentChange.toFixed(0)}%`;
const deltaLabel = `${internal.delta > 0 ? '+' : ''}${internal.delta.toFixed(0)}`;
```

For the default sample, `percentChange` is `21` and `delta` is `21`, so labels are `+21%` and `+21`. Use `toFixed(0)` so the test matches. Keep `internal.summary` as the sentence under the badges.

4. If `!readOnly`, wrap the two existing inputs in `<EditDataDetails>`. Put `className='sm:col-span-2'` on both `FormField`s.

- [ ] **Step 4: Run the tests and make sure they pass**

```
pnpm test -- src/components/editor/customBlocks/TrendBlock/TrendComponent.test.tsx src/components/editor/customBlocks/TrendBlock/trendMath.test.ts
```

Expected: PASS.

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 6: Markdown render + file validation

**Files:**
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/markdownRender.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/markdownRender.test.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/markdownFile.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/markdownFile.test.ts`
- Modify: `local/the_monkeys/apps/the_monkeys/package.json` (add `marked` via pnpm)

**Interfaces:**
- Consumes: `marked`, `isomorphic-dompurify`
- Produces:
  - `export const MARKDOWN_MAX_BYTES = 256 * 1024`
  - `export function validateMarkdownFile(file: File): string | null` — `null` means OK; otherwise the exact error string
  - `export async function renderMarkdownHtml(markdown: string): Promise<string>`

- [ ] **Step 1: Install marked**

From `local/the_monkeys/apps/the_monkeys`:

```
pnpm add marked
```

Do not add `react-markdown`. Do not use `@mdx-js/*`.

- [ ] **Step 2: Write the failing tests**

`markdownFile.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { MARKDOWN_MAX_BYTES, validateMarkdownFile } from './markdownFile';

describe('validateMarkdownFile', () => {
  it('accepts a small .md file', () => {
    const file = new File(['# Hi'], 'notes.md', { type: 'text/markdown' });
    expect(validateMarkdownFile(file)).toBeNull();
  });

  it('rejects files over 256 KiB', () => {
    const file = new File([new Uint8Array(MARKDOWN_MAX_BYTES + 1)], 'big.md', {
      type: 'text/markdown',
    });
    expect(validateMarkdownFile(file)).toBe('File is too large (max 256 KB).');
  });

  it('rejects images', () => {
    const file = new File(['x'], 'pic.png', { type: 'image/png' });
    expect(validateMarkdownFile(file)).toBe('Use a .md or text file.');
  });
});
```

`markdownRender.test.ts`:

```ts
import { describe, expect, it } from 'vitest';

import { renderMarkdownHtml } from './markdownRender';

describe('renderMarkdownHtml', () => {
  it('renders a heading', async () => {
    const html = await renderMarkdownHtml('# Hello');
    expect(html).toContain('<h1>');
    expect(html).toContain('Hello');
  });

  it('strips script tags and javascript URLs', async () => {
    const html = await renderMarkdownHtml(
      '<script>alert(1)</script>\n[x](javascript:alert(1))'
    );
    expect(html.toLowerCase()).not.toContain('<script');
    expect(html.toLowerCase()).not.toContain('javascript:');
  });

  it('does not emit img for markdown images', async () => {
    const html = await renderMarkdownHtml('![x](https://example.com/a.png)');
    expect(html.toLowerCase()).not.toContain('<img');
  });

  it('renders h4 as h3', async () => {
    const html = await renderMarkdownHtml('#### Deep');
    expect(html).toContain('<h3>');
    expect(html).not.toContain('<h4>');
  });
});
```

- [ ] **Step 3: Run tests to verify they fail**

```
pnpm test -- src/components/editor/customBlocks/MarkdownBlock/markdownFile.test.ts src/components/editor/customBlocks/MarkdownBlock/markdownRender.test.ts
```

Expected: FAIL — modules missing.

- [ ] **Step 4: Write minimal implementation**

`markdownFile.ts`:

```ts
export const MARKDOWN_MAX_BYTES = 256 * 1024;

export function validateMarkdownFile(file: File): string | null {
  if (file.size > MARKDOWN_MAX_BYTES) {
    return 'File is too large (max 256 KB).';
  }

  const name = file.name.toLowerCase();
  const okExt =
    name.endsWith('.md') ||
    name.endsWith('.markdown') ||
    name.endsWith('.txt');
  if (!okExt) {
    return 'Use a .md or text file.';
  }

  return null;
}
```

Size is checked first so an oversized `.png` still gets the size error; a small `.png` gets the type error.

`markdownRender.ts`:

```ts
import DOMPurify from 'isomorphic-dompurify';

const ALLOWED_TAGS = [
  'h1',
  'h2',
  'h3',
  'p',
  'br',
  'ul',
  'ol',
  'li',
  'a',
  'code',
  'pre',
  'strong',
  'em',
  'blockquote',
  'span',
];

export async function renderMarkdownHtml(markdown: string): Promise<string> {
  const { marked } = await import('marked');
  const renderer = new marked.Renderer();
  renderer.heading = ({ text, depth }) => {
    const level = Math.min(Math.max(depth, 1), 3);
    return `<h${level}>${text}</h${level}>\n`;
  };
  renderer.image = () => '';
  const raw = await marked.parse(markdown, {
    async: true,
    gfm: true,
    breaks: true,
    renderer,
  });
  return DOMPurify.sanitize(raw, {
    ALLOWED_TAGS,
    ALLOWED_ATTR: ['href', 'rel', 'target', 'class'],
    ALLOWED_URI_REGEXP: /^(?:(?:https?|mailto):)/i,
    ADD_ATTR: ['target', 'rel'],
  });
}
```

If `marked.Renderer` heading signature in the installed version is the legacy `(text, level)` function, use that instead:

```ts
renderer.heading = (text: string, level: number) => {
  const depth = Math.min(level, 3);
  return `<h${depth}>${text}</h${depth}>\n`;
};
```

After sanitize, if you need `rel`/`target` on links, walk is optional; DOMPurify `ALLOWED_URI_REGEXP` already blocks `javascript:`. Spec also wants `rel="noopener noreferrer"` and `target="_blank"` on http(s). Add a small post-pass:

```ts
const clean = DOMPurify.sanitize(raw, { ALLOWED_TAGS, ALLOWED_ATTR: ['href'] });
return clean.replace(
  /<a href="(https?:[^"]+)"/g,
  '<a href="$1" target="_blank" rel="noopener noreferrer"'
);
```

Keep `mailto:` without `target="_blank"` if easier: only rewrite `http`/`https`.

- [ ] **Step 5: Run the tests and make sure they pass**

```
pnpm test -- src/components/editor/customBlocks/MarkdownBlock/markdownFile.test.ts src/components/editor/customBlocks/MarkdownBlock/markdownRender.test.ts
```

Expected: PASS.

If `marked` heading API fails TypeScript, adjust to the installed types; do not change the tests.

- [ ] **Step 6: Commit** (skip unless the user asks)

---

### Task 7: Markdown block UI + createBlock

**Files:**
- Modify: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/shared/types.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/MarkdownComponent.tsx`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/MarkdownComponent.test.tsx`
- Create: `local/the_monkeys/apps/the_monkeys/src/components/editor/customBlocks/MarkdownBlock/index.ts`

**Interfaces:**
- Consumes: `createBlock`, `MarkdownBlockData`, `renderMarkdownHtml`, `validateMarkdownFile`
- Produces:
  - `export interface MarkdownBlockData { markdown: string; sourceFileName?: string }`
  - `export const MARKDOWN_TOOLBOX: ToolboxConfig`
  - default save `{ markdown: '' }`
  - EditorJS tool registered later in Task 8

- [ ] **Step 1: Add types**

In `types.ts`, after `DatasetBlockData`:

```ts
export interface MarkdownBlockData {
  markdown: string;
  sourceFileName?: string;
}

export const MARKDOWN_TOOLBOX: ToolboxConfig = {
  title: 'Markdown',
  icon: '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 5h18v14H3z"/><path d="M7 15V9l2 2 2-2v6"/><path d="M15 12h2v3"/></svg>',
};
```

Do not change Chart/Trend types.

- [ ] **Step 2: Write the failing component tests**

```tsx
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';

import MarkdownComponent from './MarkdownComponent';

describe('MarkdownComponent', () => {
  it('shows empty copy in edit', () => {
    render(
      <MarkdownComponent
        data={{ markdown: '' }}
        readOnly={false}
        onChange={() => {}}
      />
    );
    expect(
      screen.getByText('Write or paste Markdown, or upload a .md file.')
    ).toBeInTheDocument();
    expect(screen.getByRole('textbox')).toBeInTheDocument();
  });

  it('renders an h1 in Preview', async () => {
    const user = userEvent.setup();
    render(
      <MarkdownComponent
        data={{ markdown: '# Hello' }}
        readOnly={false}
        onChange={() => {}}
      />
    );
    await user.click(screen.getByRole('button', { name: 'Preview' }));
    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: 'Hello' })).toBeInTheDocument();
    });
  });

  it('renders nothing in empty readOnly', () => {
    const { container } = render(
      <MarkdownComponent
        data={{ markdown: '' }}
        readOnly={true}
        onChange={() => {}}
      />
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('shows rendered HTML and no file input in non-empty readOnly', async () => {
    render(
      <MarkdownComponent
        data={{ markdown: '# Hello' }}
        readOnly={true}
        onChange={() => {}}
      />
    );
    await waitFor(() => {
      expect(screen.getByRole('heading', { level: 1, name: 'Hello' })).toBeInTheDocument();
    });
    expect(screen.queryByLabelText(/upload/i)).not.toBeInTheDocument();
  });

  it('rejects an oversized file and keeps markdown', async () => {
    const user = userEvent.setup();
    const onChange = vi.fn();
    render(
      <MarkdownComponent
        data={{ markdown: '# Keep' }}
        readOnly={false}
        onChange={onChange}
      />
    );
    const input = screen.getByLabelText('Upload .md');
    const big = new File([new Uint8Array(256 * 1024 + 1)], 'big.md', {
      type: 'text/markdown',
    });
    await user.upload(input, big);
    expect(screen.getByText('File is too large (max 256 KB).')).toBeInTheDocument();
    expect(onChange).not.toHaveBeenCalled();
  });
});
```

- [ ] **Step 3: Run test to verify it fails**

```
pnpm test -- src/components/editor/customBlocks/MarkdownBlock/MarkdownComponent.test.tsx
```

Expected: FAIL — component missing.

- [ ] **Step 4: Write MarkdownComponent + createBlock**

`MarkdownComponent.tsx`:

- Props: `{ data: MarkdownBlockData; readOnly: boolean; onChange: (data: MarkdownBlockData) => void }`
- Same internal state + ref pattern as Chart
- If `readOnly && !internal.markdown.trim()` return `null`
- If `readOnly` with content: `BlockWrapper` + rendered HTML (`dangerouslySetInnerHTML` after `renderMarkdownHtml`). Catch errors → `Could not render Markdown.`
- Edit: `BlockWrapper`; heading `Markdown`; empty-state sentence; two full-width buttons `Write` / `Preview` (`min-h-11 w-1/2`); default tab `write`; textarea `min-h-[160px] w-full`; file input `id="markdown-upload"` with `aria-label="Upload .md"`, `accept=".md,.markdown,.txt,text/markdown,text/plain"`, sibling label/button `Upload .md` with `className='min-h-11 w-full sm:w-auto'`
- On file: `validateMarkdownFile`; on error set `fileError` and return; if `internal.markdown.trim()` call `window.confirm('Replace current Markdown with this file?')`; on cancel return; `const text = await file.text()`; `onChange({ markdown: text, sourceFileName: file.name })`
- Show `sourceFileName` as a small line when set

`index.ts`:

```ts
import { createBlock } from '../shared/createBlock';
import { MARKDOWN_TOOLBOX } from '../shared/types';
import type { MarkdownBlockData } from '../shared/types';
import MarkdownComponent from './MarkdownComponent';

const DEFAULT_DATA: MarkdownBlockData = { markdown: '' };

function normalizeData(data?: Partial<MarkdownBlockData>): MarkdownBlockData {
  const markdown = typeof data?.markdown === 'string' ? data.markdown : '';
  const sourceFileName =
    typeof data?.sourceFileName === 'string' && data.sourceFileName
      ? data.sourceFileName
      : undefined;
  return sourceFileName ? { markdown, sourceFileName } : { markdown };
}

export default createBlock<MarkdownBlockData>({
  toolbox: MARKDOWN_TOOLBOX,
  defaultData: DEFAULT_DATA,
  sanitize: {
    markdown: true,
    sourceFileName: true,
  },
  Component: MarkdownComponent,
  normalizeData,
});
```

`normalizeData` must omit `sourceFileName` when missing so default save is `{ markdown: '' }` with no extra key.

- [ ] **Step 5: Run the tests and make sure they pass**

```
pnpm test -- src/components/editor/customBlocks/MarkdownBlock/MarkdownComponent.test.tsx src/components/editor/customBlocks/MarkdownBlock/markdownRender.test.ts src/components/editor/customBlocks/MarkdownBlock/markdownFile.test.ts
```

Expected: PASS.

- [ ] **Step 6: Commit** (skip unless the user asks)

---

### Task 8: Register Markdown in edit and read-only configs

**Files:**
- Modify: `local/the_monkeys/apps/the_monkeys/src/config/editor/monkeys_editor.config.ts`
- Modify: `local/the_monkeys/apps/the_monkeys/src/config/editor/monkeys_editor_readonly.config.ts`
- Create: `local/the_monkeys/apps/the_monkeys/src/config/editor/editorTools.test.ts`

**Interfaces:**
- Consumes: `MarkdownBlock` default export from Task 7
- Produces: `tools.markdown` on both configs; `chart` and `trend` unchanged

- [ ] **Step 1: Write the failing test**

```ts
import { describe, expect, it } from 'vitest';

import { getEditorConfig } from './monkeys_editor.config';
import { editorConfig } from './monkeys_editor_readonly.config';

describe('editor tools', () => {
  it('registers markdown, chart, and trend in the edit config', () => {
    const tools = getEditorConfig('blog-id').tools;
    expect(tools).toHaveProperty('markdown');
    expect(tools).toHaveProperty('chart');
    expect(tools).toHaveProperty('trend');
  });

  it('registers markdown, chart, and trend in the read-only config', () => {
    expect(editorConfig.tools).toHaveProperty('markdown');
    expect(editorConfig.tools).toHaveProperty('chart');
    expect(editorConfig.tools).toHaveProperty('trend');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

```
pnpm test -- src/config/editor/editorTools.test.ts
```

Expected: FAIL — `markdown` missing.

- [ ] **Step 3: Register the tool**

In both configs:

```ts
import MarkdownBlock from '@/components/editor/customBlocks/MarkdownBlock';
```

Add next to `dataset`:

```ts
    markdown: {
      class: MarkdownBlock,
    },
```

Do not remove any existing tools. Do not change `holder` or `defaultBlock`.

- [ ] **Step 4: Run the tests and make sure they pass**

```
pnpm test -- src/config/editor/editorTools.test.ts src/components/editor/customBlocks/ChartBlock src/components/editor/customBlocks/TrendBlock src/components/editor/customBlocks/MarkdownBlock src/components/editor/customBlocks/shared/BlockWrapper.test.tsx
```

Expected: PASS.

- [ ] **Step 5: Commit** (skip unless the user asks)

---

### Task 9: Browser check (create + published)

**Files:** none (manual / browser tools). Do this before claiming done.

- [ ] **Step 1: Open create/edit on a ~375px viewport**

Insert Chart from the plus menu. Confirm the sample line chart is visible without opening Edit data. Confirm the how-to sentence is visible. Open Edit data, paste CSV, parse, confirm the graphic updates.

- [ ] **Step 2: Insert Trend**

Confirm sparkline + `Up` / `+21%` / `+21` without opening Edit data.

- [ ] **Step 3: Insert Markdown**

Paste `# Hello`, switch to Preview, confirm heading. Upload a small `.md` file; confirm confirm-dialog if text already exists; confirm published Preview tab and the article page render the Markdown. Confirm Chart/Trend still render on the published page.

- [ ] **Step 4: Desktop (~1280px)**

Same three blocks: Edit data may be two columns; graphic still on top.

If any step fails, fix in the matching task and re-run that task’s tests before continuing.

---

## Self-review

**Spec coverage**

| Spec section | Task |
| --- | --- |
| Chart/Trend graphic first, how-to, collapsed Edit data | 2, 3, 5 |
| Dark-mode axis contrast / no D3 | 1 |
| Trend sparkline `width:100%`, plain badges | 4, 5 |
| `showLegend` sanitizer | 3 |
| Markdown data shape, toolbox, upload, sanitization | 6, 7 |
| Register edit + read-only | 8 |
| Empty readOnly markdown renders nothing | 7 |
| Mobile tap targets / one column / full-width buttons | 2, 3, 7 |
| Browser 375px + desktop | 9 |
| No engine / no excerpt / no whole-post import | Global constraints |

**Placeholders:** none. Commit steps are present but skipped until the user asks.

**Types:** `MarkdownBlockData.markdown: string`, optional `sourceFileName`. `generateChartMarkup`, `computeTrend`, `buildSparklineSvg`, `validateMarkdownFile`, `renderMarkdownHtml` names are consistent across tasks.
