import { useLayoutEffect, useRef } from 'react'
import { HTMLContainer, Rectangle2d, ShapeUtil, useValue, type Editor, type TLShape } from 'tldraw'
import { RefCard } from '../components/RefCard'
import {
	CARD_FACE_CLASS,
	CARD_HALO_INSET,
	CARD_THUMB,
	CardHostContext,
} from '../components/entitycard/EntityCard'

// The whiteboard's own shape: a live app card pinned to the canvas. Its only
// state is an fls: token — the same compact reference chat, the wiki and task
// bodies use — so a card placed here is not a screenshot of a task or a batch
// but the real thing, re-hydrated by RefCard on every render and always current.
//
// That reuse is the whole point: any card scheme the app gains later
// (fls:doc / fls:task / fls:job / fls:batch today) works on the whiteboard for
// free, because RefCard is the single place that maps a token to its renderer.

export const FLS_CARD_TYPE = 'fls-card'

declare module 'tldraw' {
	export interface TLGlobalShapePropsMap {
		[FLS_CARD_TYPE]: { w: number; h: number; token: string }
	}
}

export type FlsCardShape = TLShape<typeof FLS_CARD_TYPE>

// The size a card is CREATED at, and the cap it may grow to. The real card
// (RefCard) sizes to its content — a short name renders a narrow card — so after
// mount the shape measures the rendered card and shrinks its geometry to fit
// (see CardBody). CARD_W is the ceiling; CARD_H the initial guess before the
// first measure. Keeping geometry == paint is what makes bound arrows point at
// the card rather than at the empty right edge of a fixed box.
//
// CARD_W matches EntityCard's own max width, so a name truncates at the same
// place on a board as it does in chat.
export const CARD_W = 420
export const CARD_H = 96

// How far the measured size may sit from the stored one before it is written
// back. Wide enough to absorb the difference between two machines' font
// rendering, narrow enough that a real content change still resizes the card.
const MEASURE_SLOP = 3

// The narrowest a REAL card can measure. EntityCard's front face is a border
// (1px each side), 8px of padding each side, and a CARD_THUMB-wide thumbnail
// that never shrinks (`flexShrink: 0`); only the text column can collapse. So
// anything at or below the thumbnail's own width is not a card that rendered —
// it is a card that was not laid out when we looked.
// It doubles as the "is the STORED size possible?" test used to recover a card
// the old measure bug wrote down to 7x7, so it is exported and pinned by a test:
// the recovery size must itself clear this floor or the reset would re-trigger
// on every render.
export const MIN_MEASURED_W = CARD_THUMB

// shapeSizeForMeasurement turns a raw front-face measurement into the geometry
// to store, or null when the measurement must be IGNORED.
//
// Pure and exported because it is the one piece of this file that can quietly
// corrupt a shared document, and it did: the reject test used to run on the
// inset-inclusive numbers, where a 0x0 measurement had already become 7x7
// (CARD_HALO_INSET * 2) and sailed through a `!w || !h` check.
//
// 0x0 is not an exotic input. tldraw culls off-screen shapes by setting
// `display: none` on the shape container rather than unmounting them, so every
// card that leaves the viewport measures 0x0 and ResizeObserver reports it.
// A board large enough for that to happen would take itself apart as it was
// panned; a board that fits on screen never culls and looked perfectly fine,
// which is what disguised this as one corrupted board.
export function shapeSizeForMeasurement(
	rawW: number,
	rawH: number,
): { w: number; h: number } | null {
	if (!Number.isFinite(rawW) || !Number.isFinite(rawH)) return null
	if (rawW < MIN_MEASURED_W || rawH <= 0) return null
	const inset = CARD_HALO_INSET * 2
	return { w: Math.min(CARD_W, Math.ceil(rawW + inset)), h: Math.ceil(rawH + inset) }
}

// The mounted card DOM, by shape id. CardBody keeps this current; the shape
// util's onClick below is the only reader.
const cardNodes = new Map<string, HTMLElement>()

export class FlsCardShapeUtil extends ShapeUtil<FlsCardShape> {
	static override type = FLS_CARD_TYPE

	override getDefaultProps(): FlsCardShape['props'] {
		return { w: CARD_W, h: CARD_H, token: '' }
	}

	// Cards are content, not geometry: let them move but not distort, so a task
	// card never ends up squashed into an unreadable sliver.
	override canResize() {
		return false
	}

	override getGeometry(shape: FlsCardShape) {
		return new Rectangle2d({
			width: shape.props.w,
			height: shape.props.h,
			isFilled: true,
		})
	}

	override component(shape: FlsCardShape) {
		return <CardBody editor={this.editor} shape={shape} />
	}

	// A card's own click (navigate to the document, open the task dialog, …) can
	// never reach it through the DOM: tldraw's canvas takes pointer capture on
	// pointer down, so the browser retargets the mouse up and fires `click` at
	// the canvas instead of at the card. The editor still tells a click from a
	// drag — that is what this handler is — so take the click from there and
	// replay it on whatever sits under the cursor. The behaviour stays inside
	// the card component, which is what keeps every scheme RefCard renders
	// (doc / task / job / batch, and whatever comes next) working here for free.
	//
	// Returning nothing lets tldraw carry on with its normal selection handling.
	override onClick(shape: FlsCardShape) {
		// The same gate as the card's pointer events (see CardBody): the first
		// click selects, the second interacts. tldraw applies this click's
		// selection *after* the handler runs, so what we read here is the state
		// the click started from.
		if (this.editor.getOnlySelectedShapeId() !== shape.id) return
		const node = cardNodes.get(shape.id)
		if (!node) return
		const rect = this.editor.getContainer().getBoundingClientRect()
		const p = this.editor.inputs.getCurrentScreenPoint()
		// Aim at the exact element under the cursor so a control inside a card
		// gets the click rather than the card as a whole; fall back to the card
		// root when the hit test lands outside (zoomed-out cards are small).
		const hit = document.elementFromPoint(rect.left + p.x, rect.top + p.y)
		const target = hit && node.contains(hit) ? hit : node.firstElementChild
		if (target instanceof HTMLElement) target.click()
	}

	override getIndicatorPath(shape: FlsCardShape) {
		const path = new Path2D()
		path.rect(0, 0, shape.props.w, shape.props.h)
		return path
	}
}

// CardBody renders the card and keeps the SHAPE's geometry equal to the card's
// actual painted size. RefCard sizes to content, so a fixed 320x96 shape left a
// short card floating in an oversized box — the selection border and (worse) a
// bound arrow's anchor sat at the box's centre, out in empty space. Measuring
// the rendered card and syncing w/h back onto the shape makes the box hug the
// card, so arrows land on it.
function CardBody({ editor, shape }: { editor: Editor; shape: FlsCardShape }) {
	const ref = useRef<HTMLDivElement>(null)
	// Pointer events are off until the shape is the only selection. Without that,
	// the card's own click targets would swallow the drag that moves it; with it,
	// one click selects and the next interacts (opening a dialog / navigating).
	//
	// This MUST subscribe to the selection signal rather than read it during
	// render. tldraw tracks the signals a shape reads inside ShapeUtil.component
	// (see useStateTracking in its Shape component), but component() only
	// *returns* <CardBody/> — the read below happens in CardBody's own render,
	// outside that scope. Read plainly it therefore never re-runs on selection:
	// CardBody re-renders only when the shape's props change (its memo compares
	// shape content), so a card painted while unselected kept pointerEvents
	// 'none' for good — no hover, no jump icon, no click through to the card. It
	// looked fine while a board was being built, because placing a card churns
	// its props (the measure/sync below) and that re-render happened to pick the
	// new value up; on a reloaded board, where the sizes already match, nothing
	// re-rendered and every card was inert.
	const interactive = useValue(
		'card is only selection',
		() => editor.getOnlySelectedShapeId() === shape.id,
		[editor, shape.id],
	)

	// Publish the card's DOM for the util's onClick (see FlsCardShapeUtil).
	useLayoutEffect(() => {
		const el = ref.current
		if (!el) return
		cardNodes.set(shape.id, el)
		return () => {
			// Only drop the entry if it is still ours — a remount registers the
			// new node before the old one's cleanup runs.
			if (cardNodes.get(shape.id) === el) cardNodes.delete(shape.id)
		}
	}, [shape.id])

	useLayoutEffect(() => {
		const el = ref.current
		if (!el) return
		const sync = () => {
			// A read-only viewer must not write to a shared document at all, even
			// locally: the measurement is this browser's opinion, and persisting it
			// is a write they are not entitled to make.
			if (editor.getInstanceState().isReadonly) return
			// Measure the card's FRONT FACE plus the halo inset, never the whole
			// wrapper. Selecting a card adds an action bar and flipping it shows a
			// taller back — both are this viewer's transient state, and the wrapper
			// grows with them. Writing that would push one person's selection into
			// the shared document, and two people selecting different cards would
			// each keep overwriting the other's size.
			const faceEl = el.querySelector(`.${CARD_FACE_CLASS}`)
			const face = faceEl instanceof HTMLElement ? faceEl : el

			// Shrink/grow from the CENTRE, not the top-left, so a card stays put over
			// its intended spot — otherwise a whole placed tree drifts up-left as its
			// cards measure smaller than the 420x96 they were created at. It is also
			// what makes recovery land in the right place: x + w/2 is invariant
			// across every write, so a card's centre survives any amount of bad
			// resizing and restoring the size restores the position.
			//
			// history:'ignore' — an auto-measure is not a user edit, so it must not
			// land on the undo stack. It still persists via autosave.
			const resize = (w: number, h: number) =>
				editor.run(
					() =>
						editor.updateShape<FlsCardShape>({
							id: shape.id,
							type: FLS_CARD_TYPE,
							x: shape.x - (w - shape.props.w) / 2,
							y: shape.y - (h - shape.props.h) / 2,
							props: { w, h },
						}),
					{ history: 'ignore' },
				)

			// A measurement taken while the card is not laid out (culled, mid-mount)
			// must never be written — see shapeSizeForMeasurement.
			const size = shapeSizeForMeasurement(face.offsetWidth, face.offsetHeight)
			if (!size) {
				// We could not measure, and the size already stored is one no real
				// card can have — this shape is a casualty of the measure bug that
				// wrote culled cards down to 7x7. Reset it to the creation size so it
				// is visible and measurable again; the next measure refines it.
				// Done here rather than only on a successful measure because a card
				// that is culled at load never gets a successful measure until it is
				// panned into view, and a board should not need a guided tour to
				// repair itself.
				if (shape.props.w < MIN_MEASURED_W) resize(CARD_W, CARD_H)
				return
			}
			const { w, h } = size
			// Close enough is a match — don't churn the store on rounding.
			//
			// The tolerance is MEASURE_SLOP rather than a pixel because the measured
			// size depends on font rendering, which differs by platform and device
			// pixel ratio: two people opening the same board on different displays
			// measure e.g. 318 and 320, and with a one-pixel guard each would write
			// its own value, see the other's, and re-measure — a ping-pong between
			// clients that no single client can detect on its own.
			if (
				Math.abs(w - shape.props.w) <= MEASURE_SLOP &&
				Math.abs(h - shape.props.h) <= MEASURE_SLOP
			)
				return
			resize(w, h)
		}
		sync()
		// The measured node is `max-content`, so its size tracks the card's
		// content, not the shape box — resizing the shape can't feed back into it.
		const ro = new ResizeObserver(sync)
		ro.observe(el)
		return () => ro.disconnect()
	}, [editor, shape.id, shape.props.w, shape.props.h, shape.props.token])

	return (
		<HTMLContainer
			style={{
				width: shape.props.w,
				height: shape.props.h,
				pointerEvents: interactive ? 'all' : 'none',
				display: 'flex',
				// TOP-aligned, not centred. The shape box is measured from the card's
				// front face alone, so a selected card (action bar) and a flipped one
				// (taller back) are both taller than their box. Centring split that
				// overflow above and below, which lifted the card and left the action
				// bar straddling the bottom edge — right where tldraw strokes the
				// selection indicator. Aligned to the top, everything transient hangs
				// clear below the box.
				alignItems: 'flex-start',
				overflow: 'visible',
			}}
		>
			{/* `flex: none` is load-bearing, not tidying. This div is what sync()
			    measures, and it is a flex item in a container whose width IS
			    shape.props.w — so with the default `flex-shrink: 1` it shrank to the
			    stored box, and EntityCard's `minWidth: 0` chain let the face collapse
			    with it (the thumbnail just overflowed). The measurement was therefore
			    a function of the size it was supposed to be measuring.
			    That made a bad size PERMANENT: a card written down to 7px measured
			    ~0 forever and could never grow back. Pinned to `max-content` and
			    refusing to shrink, the measurement reflects the card's CONTENT
			    whatever the shape currently claims, so a wrong stored size corrects
			    itself on the next render — and since the write preserves the card's
			    centre (x + w/2 is invariant), it corrects back to the right place. */}
			<div
				ref={ref}
				style={{
					width: 'max-content',
					maxWidth: CARD_W,
					flex: 'none',
					display: 'flex',
					alignItems: 'flex-start',
				}}
			>
				{/* A card on the canvas takes its selection from the SHAPE, so the
				    click that selects the shape also opens the card's action bar —
				    two clicks to reach an action, the same as a card in chat. */}
				<CardHostContext.Provider value={{ selected: interactive }}>
					<RefCard token={shape.props.token} />
				</CardHostContext.Provider>
			</div>
		</HTMLContainer>
	)
}
