import { describe, expect, it } from 'vitest'
import { CARD_HALO_INSET, CARD_THUMB } from '../components/entitycard/EntityCard'
import { CARD_H, CARD_W, MIN_MEASURED_W, shapeSizeForMeasurement } from './cardshape'

// This function writes to a SHARED document, so a bad measurement is not a
// glitch that a re-render fixes — it is persisted, synced to everyone on the
// board, and moves the shape. These cases are the ones that actually corrupted
// a board in the field.

const INSET = CARD_HALO_INSET * 2

describe('shapeSizeForMeasurement', () => {
  it('adds the halo inset to a real measurement', () => {
    expect(shapeSizeForMeasurement(313, 73)).toEqual({ w: 313 + INSET, h: 73 + INSET })
  })

  it('rejects a culled card', () => {
    // tldraw hides off-screen shapes with `display: none` instead of
    // unmounting them, so ResizeObserver reports 0x0 for every card that
    // leaves the viewport. This used to produce {w: 7, h: 7} — the inset
    // alone — because the old guard tested the inset-inclusive value and 7 is
    // truthy. Boards big enough to cull took themselves apart as they panned.
    expect(shapeSizeForMeasurement(0, 0)).toBeNull()
  })

  it('rejects a face that has chrome but no laid-out content', () => {
    // Border + padding with a collapsed content row measured ~18px wide and a
    // correct height, which is how cards ended up 24x80.
    expect(shapeSizeForMeasurement(17, 73)).toBeNull()
    expect(shapeSizeForMeasurement(CARD_THUMB - 1, 73)).toBeNull()
  })

  it('accepts the narrowest card that can genuinely render', () => {
    // The thumbnail never shrinks, so the floor has to admit it.
    expect(shapeSizeForMeasurement(CARD_THUMB, 73)).not.toBeNull()
  })

  it('rejects a zero or negative height', () => {
    expect(shapeSizeForMeasurement(313, 0)).toBeNull()
    expect(shapeSizeForMeasurement(313, -1)).toBeNull()
  })

  it('rejects non-finite input', () => {
    expect(shapeSizeForMeasurement(NaN, 73)).toBeNull()
    expect(shapeSizeForMeasurement(313, Infinity)).toBeNull()
  })

  it('can recover to the creation size without re-triggering recovery', () => {
    // A card whose stored width is below the floor is reset to CARD_W x CARD_H
    // so it becomes measurable again. If the recovery size did not itself clear
    // the floor, that reset would fire on every render forever — and every one
    // of them is a write to the shared document.
    expect(CARD_W).toBeGreaterThanOrEqual(MIN_MEASURED_W)
    expect(CARD_H).toBeGreaterThan(0)
  })

  it('caps width at CARD_W but never caps height', () => {
    // A long name truncates at the same width it does in chat; a card is free
    // to be as tall as its content.
    expect(shapeSizeForMeasurement(9999, 73)?.w).toBe(CARD_W)
    expect(shapeSizeForMeasurement(313, 9999)?.h).toBe(9999 + INSET)
  })
})
