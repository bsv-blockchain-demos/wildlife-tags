/**
 * One place for colour and spacing.
 *
 * The palette is dark by default and high contrast on purpose: this is read on
 * a phone in direct sun on open water, where a light theme with subtle greys is
 * unreadable. The accent is used only for the thing the screen wants you to do
 * next.
 */
export const theme = {
  bg: '#0d1418',
  panel: '#141f25',
  panelEdge: '#1f2f38',
  ink: '#e8f0f3',
  inkDim: '#93a8b3',
  accent: '#37b3a4',
  accentInk: '#04231f',
  warn: '#e2a33c',
  bad: '#e2685c',
  good: '#5fc98a',

  radius: 14,
  gap: 12,
  pad: 16,

  // Touch targets. 48dp is Android's accessibility floor, and everything here
  // is operated with wet hands on a moving boat, so nothing goes below it.
  tap: 48
} as const

export type Theme = typeof theme
