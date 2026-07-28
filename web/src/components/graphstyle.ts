// Shared stroke/size tokens for the app's hand-drawn lane graphs — the design
// History graph (HistoryGraph.tsx) and the production Batch timeline
// (production/BatchTimeline.tsx). Both draw the same thing: dots on horizontal
// lane rails with angled date tags underneath, so they must read as one visual
// language. History is the reference implementation these values came from;
// they live here so the two can't drift apart again.
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
