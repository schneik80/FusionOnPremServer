// Shared stroke/size tokens for the app's hand-drawn dot-on-rail graphs — the
// design History timeline (history/) and the production Batch timeline
// (production/BatchTimeline.tsx). Both draw the same thing: dots on horizontal
// rails, so they must read as one visual language. The values originated in the
// original History lane graph and live here so the two can't drift apart again.
//
// TAG_ANGLE/TAG_OFFSET are the angled date tags under a lane; the Batch
// timeline still uses them. History no longer does — it moved to day rows with
// a horizontal clock axis — but its dot, ring and rail weights are unchanged,
// which is what keeps the two charts recognisably the same family.
//
// Layout spacing (column gap, lane gap, padding) stays local to each graph —
// it depends on how dense that graph's data is. Only the *drawing* weights,
// which are what make the two look like the same chart, are shared.

// Commit/run dot radius, and the width of the paper-coloured ring that lifts a
// dot off the rail it sits on.
export const NODE_R = 7
export const RING_W = 2

// Lane rails: a thick, round-capped line at half opacity behind the dots.
export const RAIL_W = 3
export const RAIL_ALPHA = 0.5

// Connectors between dots (History's merge/share risers, the Batch timeline's
// chronological thread) — thinner than a rail, but near-opaque.
export const CONNECTOR_W = 2
export const CONNECTOR_OPACITY = 0.8

// Angled date tags below the bottom lane. TAG_OFFSET is measured from the edge
// of the dot, so a tag sits the same distance from its dot in either graph.
export const TAG_FONT_SIZE = 9
export const TAG_ANGLE = -35
export const TAG_OFFSET = 12
