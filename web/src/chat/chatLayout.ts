// How wide the chat pane has to be before a thread can sit beside the message
// list instead of replacing it. Pure, so the arithmetic is covered — the repo
// has no DOM test harness, and these four numbers are the whole decision.
//
// The Fusion palette opens at 420px (FusionProjectChat entry.py). With the old
// fixed 360px thread column that left the message list 60px, which is what
// prompted this: below a certain width there is no split worth having, and the
// thread should become a page with a back arrow instead.

import { APP_RAIL_WIDTH } from '../components/Column'

// The thread's preferred width — what it was fixed at before, kept because it
// is the width at which an attached document card (thumbnail + name +
// location) reads comfortably.
export const THREAD_BASIS = 360

// ...and how far it may be squeezed before that stops being true. Between
// SPLIT_MIN and a wide pane the thread gives these 40px back rather than the
// message list absorbing the entire squeeze.
export const THREAD_MIN = 320

// The same floor for the list beside it: narrower and a message with a card in
// it starts wrapping into a column of fragments.
export const MESSAGES_MIN = 320

// Below this the two cannot both be themselves, so the thread takes the whole
// pane and the message list steps aside.
export const SPLIT_MIN = MESSAGES_MIN + THREAD_MIN

/**
 * splitWidth is the space the message list and the thread actually share.
 *
 * The open channel rail comes off the top: measuring the outer box alone would
 * call a 900px pane wide while a 260px rail leaves only 640 for the split.
 */
export function splitWidth(outerW: number, railOpen: boolean): number {
  return Math.max(0, outerW - (railOpen ? APP_RAIL_WIDTH : 0))
}

/**
 * isCompact reports whether a thread must be a page rather than a column.
 *
 * A zero width means "not measured yet" (first paint, or a hidden tab) and is
 * treated as roomy: the desktop app is the common case, and flashing a page
 * layout for one frame before the observer reports is worse than the reverse.
 */
export function isCompact(outerW: number, railOpen: boolean): boolean {
  if (outerW <= 0) return false
  return splitWidth(outerW, railOpen) < SPLIT_MIN
}
