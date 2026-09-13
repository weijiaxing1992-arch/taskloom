import type { SVGProps } from 'react'

type IconName = 'work' | 'bell' | 'desktop' | 'refresh' | 'chevron' | 'filter' | 'check' | 'clock' | 'inbox' | 'arrow' | 'list' | 'iteration' | 'back' | 'search'

export function MobileIcon({ name, size = 22, ...props }: { name: IconName; size?: number } & SVGProps<SVGSVGElement>) {
  const common = { width: size, height: size, viewBox: '0 0 24 24', fill: 'none', stroke: 'currentColor', strokeWidth: 1.9, strokeLinecap: 'round' as const, strokeLinejoin: 'round' as const, ...props }
  if (name === 'list') return <svg {...common}><rect x="4" y="3" width="16" height="18" rx="3"/><path d="M8 8h8M8 12h8M8 16h5"/></svg>
  if (name === 'iteration') return <svg {...common}><path d="M20 8a8 8 0 0 0-14-3L3 8m0-5v5h5M4 16a8 8 0 0 0 14 3l3-3m0 5v-5h-5"/></svg>
  if (name === 'back') return <svg {...common}><path d="m14 5-7 7 7 7"/></svg>
  if (name === 'search') return <svg {...common}><circle cx="10.5" cy="10.5" r="6.5"/><path d="m16 16 5 5"/></svg>
  if (name === 'work') return <svg {...common}><rect x="4" y="5" width="16" height="15" rx="3" /><path d="M9 5V3.7h6V5M4 10h16M9 14h6" /></svg>
  if (name === 'bell') return <svg {...common}><path d="M18 9a6 6 0 0 0-12 0c0 7-3 7-3 9h18c0-2-3-2-3-9" /><path d="M10 21h4" /></svg>
  if (name === 'desktop') return <svg {...common}><rect x="3" y="4" width="18" height="12" rx="2" /><path d="M8 21h8m-4-5v5" /></svg>
  if (name === 'refresh') return <svg {...common}><path d="M20 11a8 8 0 1 0 1.4 4.5" /><path d="M20 4v7h-7" /></svg>
  if (name === 'chevron') return <svg {...common}><path d="m9 18 6-6-6-6" /></svg>
  if (name === 'filter') return <svg {...common}><path d="M4 5h16l-6.2 7.1V18l-3.6 1.8v-7.7Z" /></svg>
  if (name === 'check') return <svg {...common}><path d="m5 12 4 4L19 6" /></svg>
  if (name === 'clock') return <svg {...common}><circle cx="12" cy="12" r="8" /><path d="M12 8v4l3 2" /></svg>
  if (name === 'inbox') return <svg {...common}><path d="M4 5h16v14H4z" /><path d="M4 14h4l2 3h4l2-3h4" /></svg>
  return <svg {...common}><path d="M5 12h14m-6-6 6 6-6 6" /></svg>
}
