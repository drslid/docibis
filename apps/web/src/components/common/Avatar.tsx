import { createElement, type CSSProperties } from 'react'
import type { LucideIcon } from 'lucide-react'
import {
  Sun, Moon, Cloud, Rainbow, Snowflake, Flame, Leaf, Flower2, Sprout, Clover, Trees, Mountain,
  Cat, Dog, Bird, Fish, Rabbit, Turtle, Squirrel, Snail, Bug, PawPrint, Feather, Shell,
  Rocket, Plane, Sailboat, Bike, Car, TrainFront, Compass, Tent,
  Coffee, Pizza, Apple, Cherry, Carrot, Cake, IceCreamCone, Citrus,
  Music, Headphones, Guitar, Drum, Camera, Palette, Book, Lightbulb, Puzzle, Gamepad2, Dices, Trophy, Target, Telescope,
  Star, Heart, Gem, Crown, Sparkles, Atom, Bot, Ghost, Anchor, Umbrella,
} from 'lucide-react'
import type { Role } from '@/api/generated/docibis'
import { useI18n } from '@/i18n/useI18n'

// The pictograms an avatar can carry, by the name the API keeps. Append
// only: an account that never chose shows the pictogram its
// number points at.
export const AVATAR_ICONS: [string, LucideIcon][] = [
  ['sun', Sun], ['moon', Moon], ['cloud', Cloud], ['rainbow', Rainbow], ['snowflake', Snowflake], ['flame', Flame],
  ['leaf', Leaf], ['flower', Flower2], ['sprout', Sprout], ['clover', Clover], ['trees', Trees], ['mountain', Mountain],
  ['cat', Cat], ['dog', Dog], ['bird', Bird], ['fish', Fish], ['rabbit', Rabbit], ['turtle', Turtle],
  ['squirrel', Squirrel], ['snail', Snail], ['bug', Bug], ['paw-print', PawPrint], ['feather', Feather], ['shell', Shell],
  ['rocket', Rocket], ['plane', Plane], ['sailboat', Sailboat], ['bike', Bike], ['car', Car], ['train', TrainFront],
  ['compass', Compass], ['tent', Tent], ['coffee', Coffee], ['pizza', Pizza], ['apple', Apple], ['cherry', Cherry],
  ['carrot', Carrot], ['cake', Cake], ['ice-cream', IceCreamCone], ['citrus', Citrus], ['music', Music], ['headphones', Headphones],
  ['guitar', Guitar], ['drum', Drum], ['camera', Camera], ['palette', Palette], ['book', Book], ['lightbulb', Lightbulb],
  ['puzzle', Puzzle], ['gamepad', Gamepad2], ['dices', Dices], ['trophy', Trophy], ['target', Target], ['telescope', Telescope],
  ['star', Star], ['heart', Heart], ['gem', Gem], ['crown', Crown], ['sparkles', Sparkles], ['atom', Atom],
  ['bot', Bot], ['ghost', Ghost], ['anchor', Anchor], ['umbrella', Umbrella],
]

// Backgrounds white initials stay readable on, in both themes.
export const AVATAR_COLOURS = [
  '#0F766E', '#BE123C', '#A21CAF', '#7E22CE', '#4F46E5', '#1D4ED8',
  '#0369A1', '#0F766E', '#047857', '#B45309', '#C2410C', '#475569',
]

const ICONS = new Map(AVATAR_ICONS)

function hash(id: string): number {
  let h = 0x811c9dc5
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i)
    h = Math.imul(h, 0x01000193) >>> 0
  }
  return h >>> 0
}

export interface AvatarChoice {
  icon: string
  color: string
}

/** The avatar an account chose, else the one drawn from its number. */
export function avatarFor(id: number | string, chosen?: { icon?: string; color?: string } | null): AvatarChoice {
  const h = hash(String(id || '?'))
  const drawn = AVATAR_COLOURS[h % AVATAR_COLOURS.length]
  if (chosen?.icon && ICONS.has(chosen.icon)) {
    return { icon: chosen.icon, color: chosen.color && /^#[0-9a-f]{6}$/i.test(chosen.color) ? chosen.color : drawn }
  }
  return { color: drawn, icon: AVATAR_ICONS[Math.floor(h / AVATAR_COLOURS.length) % AVATAR_ICONS.length][0] }
}

export function initialsOf(name?: string, email?: string): string {
  const words = (name || '').trim().split(/\s+/).filter(Boolean)
  if (words.length >= 2) return (words[0][0] + words[words.length - 1][0]).toUpperCase()
  if (words.length === 1) return words[0][0].toUpperCase()
  return (email || '?')[0].toUpperCase()
}

function mix(hex: string, amount: number): string {
  const n = parseInt(hex.slice(1), 16)
  const target = amount < 0 ? 255 : 0
  const part = (c: number) => Math.round(c + (target - c) * Math.abs(amount))
  return `rgb(${part((n >> 16) & 255)} ${part((n >> 8) & 255)} ${part(n & 255)})`
}

const SIZES = {
  xs: { box: 'w-5 h-5 text-[9px]', icon: false, ring: 1.5, gap: 1 },
  sm: { box: 'w-8 h-8 text-xs', icon: true, ring: 2, gap: 1.5 },
  md: { box: 'w-10 h-10 text-sm', icon: true, ring: 2, gap: 1.5 },
  lg: { box: 'w-14 h-14 text-xl', icon: true, ring: 2.5, gap: 2 },
  xl: { box: 'w-24 h-24 text-[2rem]', icon: true, ring: 3, gap: 3 },
} as const

// Administrators wear a ring of the brand colour, editors an amber one.
const RING: Partial<Record<Role, string>> = { admin: '#0F766E', editor: '#D97706' }

interface Props {
  id: number | string
  name?: string
  email?: string
  avatar?: { icon?: string; color?: string } | null
  size?: keyof typeof SIZES
  role?: Role
  decorative?: boolean
  className?: string
}

/** A person as a circle: initials over a large pictogram, on the colour they chose. */
export function Avatar({ id, name, email, avatar, size = 'sm', role, decorative = false, className = '' }: Props) {
  const { t } = useI18n()
  const { icon, color } = avatarFor(id, avatar)
  const Icon = ICONS.get(icon)
  const s = SIZES[size]
  const ring = role ? RING[role] : undefined
  const text = [name || email || '', role ? t(`role.${role}`) : ''].filter(Boolean).join(' · ')
  const circle: CSSProperties = {
    backgroundColor: color,
    backgroundImage: `linear-gradient(135deg, ${mix(color, -0.14)} 0%, ${color} 48%, ${mix(color, 0.32)} 100%)`,
    ...(ring ? { outline: `${s.ring}px solid ${ring}`, outlineOffset: s.gap } : {}),
  }
  const a11y = decorative ? { 'aria-hidden': true } : { role: 'img', 'aria-label': text, title: text }
  return (
    <span {...a11y} className={`relative inline-flex shrink-0 align-middle rounded-full ${className}`}>
      <span aria-hidden="true" style={circle} className={`${s.box} relative overflow-hidden rounded-full flex items-center justify-center font-bold text-white leading-none select-none`}>
        {s.icon &&
          Icon &&
          createElement(Icon, {
            className: 'absolute text-white pointer-events-none',
            strokeWidth: 2.25,
            style: { width: '68%', height: '68%', right: '-6%', bottom: '-8%', opacity: 0.38, transform: 'rotate(-10deg)' },
          })}
        <span className="relative" style={{ textShadow: '0 1px 2px rgb(0 0 0 / 0.35)' }}>
          {initialsOf(name, email)}
        </span>
      </span>
    </span>
  )
}
