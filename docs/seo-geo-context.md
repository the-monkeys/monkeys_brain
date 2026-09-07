# SEO / GEO handoff context

Read this plus `docs/seo-geo-plan.md` if the previous chat ran out of context. Product/events handoff remains `docs/context.md`.

**Job:** aggressive SEO + GEO for **Events, Groups, and Studio** (Image template, X screenshot, Business card). Blog SEO already exists — do not rebuild it. Production: **https://monkeys.com.co**. Local: Next `localhost:3000`, gateway `localhost:8081`. ~77 users; discovery is the constraint.

GitHub north star for *blog* crawler hygiene: [issue #677](https://github.com/the-monkeys/the_monkeys/issues/677) (JSON-LD, RSS/Atom, robots `max-image-preview`, WebSub). Sub-issues #682–#686. That issue is **article-only**. This work copies the *patterns* onto events / groups / studio. Do not implement WebSub or rewrite `generateBlogSchema` unless asked.

---

## Product (how to talk about Monkeys in meta copy)

Research-first platform: people write **research journals / blogs**, plus:

| Surface | Public URL | What it is |
| --- | --- | --- |
| Journals / feed | `/feed`, `/blog/[slug]` | Existing SEO — leave alone |
| Events | `/events`, `/events/[slug]` | Meetup-style talks, workshops, RSVP (free or Razorpay) |
| Groups | `/groups`, `/groups/[slug]` | Public/private/unlisted communities that host events |
| Studio | `/snapshot/new` | Image templates (Instagram/quote/carousel) |
| Studio | `/snapshot/new?view=x` | Clean X/Twitter screenshot from a public post URL |
| Studio | `/cards` | Digital business card + QR vCard (login-gated editor) |

Do **not** write “we are #1 in everything” into titles. Rank by competing for real queries (tweet screenshot generator, digital business card, research meetup, Instagram quote card) with complete structured data, sitemaps, crawlable HTML, and honest FAQs. Keyword stuffing will get ignored or penalized.

---

## Repos

| Repo | Path | Notes |
| --- | --- | --- |
| Engine | `c:\Users\Dave\the_monkeys\the_monkeys_engine` | Go services. These SEO files live in **frontend**. |
| Frontend | `engine/local/the_monkeys` | Own git repo, gitignored by engine. App: `apps/the_monkeys` (Next.js 14 App Router). |

Commit SEO work **inside** `local/the_monkeys`, not the engine repo. Confirm `git branch` first (`events_upgrade` vs snapshot PR branch).

---

## What blog SEO already does (copy these patterns)

| Piece | Where |
| --- | --- |
| Root metadata + Organization JSON-LD | `src/app/layout.tsx` |
| Article `generateMetadata` + BlogPosting JSON-LD | `src/app/blog/[slug]/page.tsx`, `layout.tsx`, `utils.ts` (`generateBlogSchema`) |
| Feed hub metadata + hidden `<h1>` | `src/app/feed/layout.tsx` |
| AboutPage JSON-LD | `src/app/about/page.tsx` |
| Sitemap (home, feed, about, topics, **events hub only**, blogs) | `src/app/sitemap.ts` — blogs from `https://monkeys.support/api/v2/blog/meta-feed` |
| Topics sitemap | `src/app/topics/sitemap.ts` |
| robots.txt | `src/app/robots.ts` — allow `/`, sitemaps: `/sitemap.xml`, `/topics/sitemap.xml` |
| Canonical / OG image | `src/constants/baseUrl.ts` = `https://monkeys.com.co`; OG `opengraph-image.png?b7ef6eff2b7766be` |
| Profile metadata | `src/app/[username]/layout.tsx` |

`LIVE_URL` / `API_URL` come from `src/constants/api.ts` (`NEXT_PUBLIC_LIVE_URL`, `NEXT_PUBLIC_API_URL`). Server fetches use `API_URL`; browser uses `/api/v1`.

---

## What events / groups / studio already have (thin)

| Page | Today |
| --- | --- |
| `/events` layout | Title “Events”, short description, OG. **No** canonical, keywords, FAQ, CollectionPage JSON-LD, RSS. Listing page is `'use client'` — crawlers get almost no H1 on Discover. |
| `/events/[slug]` | `generateMetadata` + basic Event JSON-LD (title, dates, attendance mode, location, organizer). **Missing:** canonical, robots `max-image-preview`, offers/tickets, geo, breadcrumbs, noindex for missing events. |
| `/groups` layout | Same thin pattern as events. Listing is `'use client'`. |
| `/groups/[slug]` | `generateMetadata` only. **No JSON-LD.** Private/unlisted not noindexed in metadata. |
| `/snapshot`, `/snapshot/new`, `/cards` | **No layouts / no metadata.** Client pages. Studio is a SEO dead zone. |
| `/events/new`, `*/edit`, `*/manage`, `/groups/new`, invite | No `noindex`. Risk of indexing auth/editor shells. |
| Sitemap | Static `/events` only. **No** group hub, studio, cards, per-event, per-group URLs. |
| robots | Does not disallow `/auth/`, editors, or point at extra sitemaps. No AI crawler allows. |
| Feeds | No `/events/feed.xml` or `/groups/feed.xml`. Blog `/feed.xml` from #677 also not in this tree. |

Public list APIs (auth optional, drafts hidden):

- `GET /api/v1/events?date=upcoming&limit=`
- `GET /api/v1/groups?limit=&public_only=1`
- Detail: `GET /api/v1/events/:slug`, `GET /api/v1/groups/:slug`

Frontend wrappers: `listEvents` / `getEvent` in `services/events/eventsApi.ts`; `listGroups` / `getGroup` in `services/groups/groupsApi.ts`. Types: `EventItem`, `GroupItem` (`visibility`: public/private/unlisted).

Event detail already SSRs JSON-LD via `loadEvent` + `fetch(`${API_URL}/events/...`)` with `revalidate: 60`. Groups detail does **not** inject JSON-LD.

Studio nav: `StudioTabs` — Image template (`/snapshot/new?view=template`), X screenshot (`?view=x`), Card (`/cards`). Image template + X are public; cards editor is `CardsAuthGuard`.

---

## Files already added this session (not wired yet)

Frontend, under `apps/the_monkeys/src`:

| File | Role |
| --- | --- |
| `lib/seo.ts` | `SITE_URL`, OG, `indexRobots` / `noIndexMetadata`, `pageMetadata()`, `publisherOrg()`, `faqPage()`, `breadcrumb()`, `escapeXml()`, copy+FAQ constants: `EVENTS_SEO`, `GROUPS_SEO`, `STUDIO_SEO`, `X_SCREENSHOT_SEO`, `CARDS_SEO` |
| `lib/seoSchema.ts` | `eventsHubGraph`, `groupsHubGraph`, `studioHubGraph` (WebApplication), `eventJsonLd` (offers/geo), `groupJsonLd` (Organization) |
| `lib/seoCatalog.ts` | `fetchPublicEvents` / `fetchPublicGroups` for sitemaps+RSS. Origin: `API_URL` or `https://monkeys.com.co/api/v1` |
| `components/seo/JsonLd.tsx` | `<script type="application/ld+json">` |

**Not done:** layouts/pages still use old metadata. Helpers are unused until the plan’s wiring steps run.

---

## Implementation rules

- Frontend only unless asked. No new backend endpoints if `/events` and `/groups` list already return public rows.
- Match blog style: `generateMetadata`, JSON-LD `<script>`, `alternates.canonical`, OG + Twitter `summary_large_image`.
- Listing pages that are `'use client'` cannot export metadata. Split: **server** `page.tsx` (JSON-LD + optional `sr-only` h1) + existing client as `*PageClient.tsx`. Shared `layout.tsx` wraps **new/edit/manage** too — do **not** put a marketing FAQ in `app/events/layout.tsx` or it shows on create/edit.
- `noindex` private shells: `/events/new`, `[slug]/edit`, `[slug]/manage`, `/groups/new`, `[slug]/edit|manage|requests`, `/groups/invite/`, `/cards/[cardId]` (personal cards). Index `/cards` and `/cards/new` as the tool.
- Private/unlisted groups and draft events: `robots: noindex` even if the URL loads.
- Ticket prices in `eventJsonLd` are **major units** (₹499 stored as `499`), same as `formatPrice`. Do not divide by 100.
- `events/layout.tsx` already exists; overwrite metadata there for the hub, and **override** on the listing `page.tsx` if needed. Child `generateMetadata` merges with the layout.
- Lint: Prettier. Hidden h1 pattern already used in `feed/layout.tsx`.
- Do not wait on docker rebuild for this — no Go changes.
- Browser-verify titles via View Source / `/events` head, not only screenshots.

---

## GEO (generative engines)

SEO = Google/Bing (titles, canonicals, sitemap, Event/Organization/WebApplication JSON-LD, RSS).

GEO = ChatGPT, Perplexity, Gemini, Google AI Overviews: a machine-readable product definition.

Ship `/llms.txt` (and mention it in robots). Content: what Monkeys is, the six public product URLs, RSS, “research journals + events + groups + studio”. Allow GPTBot / ChatGPT-User / ClaudeBot / PerplexityBot / Google-Extended.

---

## Verify after wiring

- View source on `/events`, `/groups`, `/snapshot/new`, `/snapshot/new?view=x`, `/cards`: title, description, canonical, JSON-LD.
- `/events/[slug]` Event rich-result fields + offers.
- `/groups/[slug]` Organization JSON-LD; private group `noindex`.
- `/sitemap.xml` includes studio + groups; `/events/sitemap.xml` and `/groups/sitemap.xml` list public slugs (empty is OK locally if API_URL misses production).
- `/events/feed.xml`, `/groups/feed.xml` `content-type: application/rss+xml`.
- `/llms.txt` 200.
- `/robots.txt` extra sitemaps + disallows.
- Create-event / edit still `noindex` and **no** hub FAQ in the DOM.

Issue #677 leftover (out of scope unless asked): blog `/feed.xml` + `/atom.xml`, WebSub ping, ImageObject dimensions on `generateBlogSchema`.
