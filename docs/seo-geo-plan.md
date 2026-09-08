# Events, Groups, Studio — SEO + GEO plan

Status: **implemented (frontend).** Helpers + hubs + detail JSON-LD + noindex shells + sitemaps/RSS + `llms.txt`. Verify on production after deploy. Handoff: `docs/seo-geo-context.md`.

Goal: make Events, Groups, and Studio (Image template, X screenshot, Card) discoverable in Google and in AI answers. Blog SEO stays as-is. Production URL: `https://monkeys.com.co`.

Do not put “#1 in everything” in titles. Compete on specific queries with complete pages.

---

## Why current pages will not rank

1. Hub titles are generic (`Events`, `Groups`). Studio routes have **no** `metadata` at all.
2. `/events` and `/groups` are client-only lists — weak crawlable copy (events Discover has no real H1).
3. Sitemap lists `/events` once and **zero** event/group/studio URLs.
4. Groups have no JSON-LD. Event JSON-LD is missing offers, geo, canonical, breadcrumbs.
5. Editor URLs (`/new`, `/edit`, `/manage`) can be indexed.
6. No RSS for events/groups; no `llms.txt` for AI crawlers.

Issue [#677](https://github.com/the-monkeys/the_monkeys/issues/677) is the blog checklist (JSON-LD completeness, `/feed.xml`, robots `max-image-preview`, WebSub). Reuse those ideas here; do not block this work on WebSub or blog feed routes.

---

## Copy targets (already in `lib/seo.ts`)

| URL | Intent |
| --- | --- |
| `/events` | Research events / meetups / RSVP talks & workshops |
| `/groups` | Research groups, writing communities, start a group |
| `/snapshot/new` | Free Instagram/quote/carousel templates |
| `/snapshot/new?view=x` | Free X/Twitter screenshot generator |
| `/cards` | Digital business card + QR vCard |
| `/snapshot` | Snapshot picker (index, thinner than `/snapshot/new`) |

FAQs live next to those constants. Hub JSON-LD should emit `FAQPage` from the same objects.

---

## Slice 1 — Wire hub metadata + JSON-LD (highest leverage)

**Events**

- Update `app/events/layout.tsx` with `pageMetadata(EVENTS_SEO)` (canonical, keywords, robots `max-image-preview:large`, OG/Twitter). Keep layout chrome only — **no FAQ UI** here (layout also wraps `/new` and `/[slug]/edit`).
- Split `app/events/page.tsx`: move current file to `EventsPageClient.tsx`. Server `page.tsx` renders `<JsonLd data={eventsHubGraph(EVENTS_SEO.faqs)} />` and a visually hidden H1 (same trick as `app/feed/layout.tsx`).
- Add `alternates.types` RSS: `/events/feed.xml`.

**Groups** — same split (`GroupsPageClient.tsx` + server page + `groupsHubGraph`).

**Studio**

- `app/snapshot/layout.tsx` — indexable Snapshot/Studio fallback.
- Change `app/snapshot/new/page.tsx` to a **server** page with `generateMetadata({ searchParams })`: `view=x` → `X_SCREENSHOT_SEO`, else `STUDIO_SEO`. Render `studioHubGraph` + `sr-only` H1. Move today’s client code to `SnapshotNewClient.tsx`.
- `app/cards/layout.tsx` (or server `page.tsx`) using `CARDS_SEO` + WebApplication JSON-LD + FAQ. Leave `CardsAuthGuard` for the gallery.

**Root** — extend Organization JSON-LD in `app/layout.tsx`: `knowsAbout` includes events, groups, studio tools; optional `hasOfferCatalog` pointing at `/events`, `/groups`, `/snapshot/new`, `/cards`.

---

## Slice 2 — Detail pages

**`app/events/[slug]/page.tsx`**

- Replace local `buildEventJsonLd` with `eventJsonLd()` from `seoSchema.ts` (offers, free vs paid, Place/VirtualLocation, geo, breadcrumbs via extra `<JsonLd>`).
- Metadata: `canonical`, `indexRobots`, keywords from `event.tags`, Twitter site. Not found / draft → `noIndexRobots`.

**`app/groups/[slug]/page.tsx`**

- Server-render `<JsonLd data={groupJsonLd(group)} />` + breadcrumbs.
- Metadata like events. `visibility !== 'public'` or non-published → `noindex`.

---

## Slice 3 — noindex private shells

Small `layout.tsx` files exporting `noIndexMetadata` from `lib/seo.ts`:

| Route |
| --- |
| `app/events/new/layout.tsx` |
| `app/events/[slug]/edit/layout.tsx` |
| `app/events/[slug]/manage/layout.tsx` |
| `app/groups/new/layout.tsx` |
| `app/groups/[slug]/edit/layout.tsx` |
| `app/groups/[slug]/manage/layout.tsx` |
| `app/groups/[slug]/requests/layout.tsx` |
| `app/groups/invite/[token]/layout.tsx` |
| `app/cards/[cardId]/layout.tsx` |
| `app/snapshot/[blogId]/layout.tsx` optional index (public post-derived) or noindex — **index only if the source blog is public**; default noindex to be safe |

---

## Slice 4 — Sitemaps + RSS + robots

- `app/events/sitemap.ts` — `fetchPublicEvents()` → `/events/{slug}` weekly, priority ~0.7.
- `app/groups/sitemap.ts` — public groups only.
- Update `app/sitemap.ts` static list: `/groups`, `/snapshot/new`, `/snapshot/new?view=x` (query URLs are optional; path `/snapshot/new` is enough), `/cards`. Keep blog fetch as-is.
- `app/events/feed.xml/route.ts` and `app/groups/feed.xml/route.ts` — RSS 2.0, `escapeXml`, `application/rss+xml; charset=utf-8`. Items: title, link, guid, pubDate, description. Use `parseEventTime` for dates.
- `app/robots.ts`:
  - allow `/`
  - disallow `/auth/`, `/settings`, `/notifications`, `/library`, `/edit/`, `/create`, `/events/new`, `/groups/new`, `/*/edit`, `/*/manage`, `/groups/invite/`, `/cards/` (trailing slash keeps `/cards` allowed)
  - sitemaps: existing two + `/events/sitemap.xml` + `/groups/sitemap.xml`
  - extra rule: allow GPTBot, ChatGPT-User, ClaudeBot, PerplexityBot, Google-Extended

Local sitemap may be empty if `API_URL` is unset; that is OK. Production should hit `https://monkeys.com.co/api/v1` via `seoCatalog.ts`.

---

## Slice 5 — GEO

- `app/llms.txt/route.ts` (or `app/llms.txt/route.ts` serving text): short product definition, public URLs, RSS, “research journals + events + groups + studio”. `Content-Type: text/plain; charset=utf-8`.
- Optional `app/llms-full.txt` later — not required for v1.

---

## Out of scope (unless asked)

- Blog `/feed.xml` / `/atom.xml` / WebSub (#677, #685, #686).
- New Go APIs, new DB, changing RSVP/UI.
- Studio visual redesign.
- Buying backlinks / Search Console setup (ops, not code).
- Unique OG images per event (cover URL is enough when present).

---

## Order of work

1. Slice 1 hubs (events, groups, three studio surfaces) + root schema.  
2. Slice 2 detail JSON-LD/metadata.  
3. Slice 3 noindex.  
4. Slice 4 sitemap/RSS/robots.  
5. Slice 5 `llms.txt`.  
6. View-source check on local `localhost:3000` for the six public URLs.

No docker rebuild. Prettier on touched TS/TSX. Commit in `local/the_monkeys` when the user asks.
