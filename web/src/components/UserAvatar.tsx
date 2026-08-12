import { Avatar, Tooltip } from '@mui/material'
import type { SxProps, Theme } from '@mui/material/styles'
import { useTheme } from '@mui/material/styles'
import { firstGrapheme } from '../fmt/graphemes'
import { userColor } from './userColor'

// The one avatar circle. Every place the app shows a person — chat messages,
// task assignees, project members, hub contributors, the permissions explorer,
// history tracks, the title bar — renders this, so the same person is the same
// colour with the same initials everywhere.
//
// Colour is a hash of the person's key (see userColor.ts, the app's single
// documented non-theme colour); the text colour is solved against it rather
// than hardcoded white, because HSL lightness is not perceptual.
//
// The one avatar circle that is NOT this component is the whiteboard presence
// stack, whose colour comes from tldraw's own peer assignment and has to match
// that peer's live cursor on the board.

// avatarKey is the identity a colour is derived from: a stable id when we have
// one, else the display name. Two people who share a name therefore share a
// colour only when neither has an id — the same fallback the History tracks
// make.
export function avatarKey(id?: string | null, name?: string | null): string {
  return id || name || ''
}

// avatarInitials takes up to two graphemes — never name[0], which splits emoji
// and combining marks. Splits on '@' as well as whitespace so a bare email
// address ("ada@example.com") still yields something better than one letter.
export function avatarInitials(name?: string | null): string {
  const s = (name ?? '').trim()
  if (!s) return '?'
  const out = s
    .split(/[\s@]+/)
    .filter(Boolean)
    .map((w) => firstGrapheme(w))
    .slice(0, 2)
    .join('')
    .toUpperCase()
  return out || '?'
}

export default function UserAvatar({
  id,
  name,
  size = 24,
  text,
  tooltip,
  dimmed,
  sx,
}: {
  /** Stable user id when known — preferred over the name for the colour hash. */
  id?: string | null
  name?: string | null
  size?: number
  /** Overrides the initials, e.g. the "+3" marker on an overflow group. */
  text?: string
  /** When set, wraps the circle in a tooltip. Pass the person's full name. */
  tooltip?: string
  /** Renders muted, for people whose access is denied/inactive. */
  dimmed?: boolean
  sx?: SxProps<Theme>
}) {
  const theme = useTheme()
  const color = userColor(avatarKey(id, name))

  const avatar = (
    <Avatar
      sx={{
        width: size,
        height: size,
        // Initials have to shrink with the circle; below 9px they stop being
        // legible, so small circles simply run tighter against the edge.
        fontSize: Math.max(9, Math.round(size * 0.45)),
        fontWeight: 600,
        bgcolor: color,
        color: theme.palette.getContrastText(color),
        flexShrink: 0,
        opacity: dimmed ? 0.55 : 1,
        ...sx,
      }}
    >
      {text ?? avatarInitials(name)}
    </Avatar>
  )

  return tooltip ? <Tooltip title={tooltip}>{avatar}</Tooltip> : avatar
}
