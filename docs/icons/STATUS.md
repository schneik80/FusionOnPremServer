# Document icons — status

The app's document icons are being replaced with purpose-drawn artwork, one
wave at a time. This page records what has landed, what is still FontAwesome,
and the rules a new mark has to follow.

## Where icons come from

| Piece | File |
| --- | --- |
| kind → FontAwesome glyph | `web/src/components/icons.ts` (`iconForItem`) |
| the render funnel | `web/src/components/entityIcons.tsx` (`ItemIcon`) |
| hand-drawn brand marks | `entityIcons.tsx` (`HubIcon`, `ProjectIcon`) |
| Fusion design-intent marks | `web/src/components/intentIcons.tsx` |
| which intent drawing to use | `web/src/components/intentArt.ts` (+ its test) |
| card artwork | `components/entitycard/EntityCard.tsx`, prop `iconItem` |

`ItemIcon` is the single funnel: everywhere an item's kind is shown — browse
rows, the details panel's Uses / Where-Used / Drawings rows, the hub browser
tree, the pins dialog, the dashboard pins list, and (via `iconItem`) document
cards and Where-Used graph nodes. Adding a mark for a kind means one line in
`ItemIcon`, not a sweep of call sites.

`iconForItem` is still consulted for every kind without its own artwork, and
is unchanged — `typeTag` (the `· asm` / `· dwg` row suffix) is a separate
concern and is not touched by any of this.

## Wave 1 — Fusion 3D designs (landed)

`kind === 'design'` now draws the Fusion **design-intent** marks: the isometric
part / hybrid / assembly glyphs already shipped by the sibling PowerTools
add-in, so one design reads the same in both products. The source of truth is
`PowerTools/lib/ptAddInUtils/assets/intent_icons/<intent>-<size>-<theme>.svg`
(see `intent_icons.py` there for the vocabulary); `intentIcons.tsx` transcribes
all twelve verbatim, writing `<rect>`s as their equivalent path and changing
nothing else.

Two axes the set varies that an ordinary glyph does not:

- **Theme.** These are three shaded faces, not a silhouette, so they cannot
  paint in `currentColor`. The light drawing carries a `#666` outline the dark
  one omits. `DesignIntentIcon` picks off `useTheme().palette.mode`.
- **Size.** 16 px and 32 px are separate drawings — the 32 px assembly and
  hybrid carry extra geometry. Callers size these the way they size an FA
  glyph, `style={{ fontSize: N }}`, and that same number picks the drawing
  (`INTENT_ART_BREAK = 20`: list rows ask for 11–16, the `EntityCard` thumbnail
  tile for 24). No size prop is threaded anywhere.

Accepted cost: a design icon does not tint to `primary.main` on a selected row
the way a `currentColor` glyph does.

### Why hybrid is drawn but never shown

Fusion's real three-way intent — part (bodies only), hybrid (bodies and
components), assembly (components only) — is a property of a *live document*
(`PartDesignIntentType` &c.), which only an in-Fusion add-in can read. All this
server has is `api/classify.go`, which asks MDM GraphQL for
`occurrences(limit: 1)`: non-empty means the design has sub-components. That is
a binary assembly-or-part with no signal for root-level bodies, so hybrid is
not derivable here.

The hybrid artwork ships anyway so the set is complete the day a signal exists.
`intentForSubtype` never returns it, and `intentArt.test.ts` asserts that — the
assertion is deliberately the thing that has to change first.

### Assembly-vs-part is best-effort, never paid for twice

`Item.subtype` arrives empty from the listing API and is refined by
`useClassify`, one APS call per design row, gated on the row nearing the
viewport. **APS is quota'd, so nothing else may issue that call.** Surfaces
that want the assembly mark but are not browse rows — document cards, pinned
production documents, Where-Used graph nodes, the details panel's nav rows —
use `useCachedSubtype` (`web/src/api/queries.ts`), which reads a result already
in the react-query cache with `enabled: false` and never fetches. On a miss
they draw the part mark, which is what they drew before.

`['classify', …]` keys are excluded from the localStorage persister, so those
free upgrades are session-scoped.

## Still FontAwesome

Every other kind, pending its own wave:

- `configured` (`faGears`) — a Fusion 3D design too, and the obvious next mark.
- `drawing` / `template` (`faPenRuler`), `schematic` / `pcb` / `ecad`
  (`faMicrochip`), `folder` (`faFolder`).
- Uploaded files (`kind === 'unknown'`) all fall through to `faFile`. There is
  no extension → icon mapping at all; the nearest thing is
  `components/viewers/kind.ts` (`viewerKindFor`), which `DocumentCard` and the
  hub browser already reuse to special-case images with `faFileImage`.
- Local records drawn beside documents in the Where-Used graph: task, chat,
  whiteboard, job, batch.
- `components/PermissionsExplorer.tsx` keeps a second, independent kind → icon
  map for its layer rows. It should fold into `icons.ts` when a wave touches
  the kinds it draws.

## Adding a mark

1. Put the drawing in a `.tsx` module beside `entityIcons.tsx`; there is no
   static-asset pipeline (no `web/public/`, no svgr, no `.svg` imports) and
   adding one is not worth it for glyphs.
2. Follow the house style: props are `{ style?, className? }` only,
   `width="1em" height="1em"`, `aria-hidden`, and
   `style={{ ...GLYPH_BASE, ...style }}` — `GLYPH_BASE` lives in `icons.ts`,
   the one icon module with no JSX, and carries the baseline nudge that lines a
   hand-drawn glyph up with an FA one.
3. Paint in `currentColor` unless the artwork is genuinely shaded; if it is,
   say so here and switch on `theme.palette.mode`.
4. Route it from `ItemIcon`, and give a card its item via `iconItem` rather
   than widening `EntityCard`'s `icon` prop.
5. Any *choice* between drawings goes in a plain `.ts` module with a test — the
   web test runner is node-only (no jsdom, no RTL), so component rendering is
   not testable but selection logic is. `intentArt.ts` is the pattern.
6. Icon components carry no text. If one ever needs a `title` or `aria-label`,
   it must come from `useTranslation()`; `src/components/**/*.tsx` is in the
   eslint i18n ratchet.
