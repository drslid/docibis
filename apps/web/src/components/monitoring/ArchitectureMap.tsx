import { AppWindow, Bot, Cloud, Cpu, Database, HardDrive, Server, Users, type LucideIcon } from 'lucide-react'

export type NodeId = 'users' | 'assistants' | 'cloudfront' | 'front' | 'api' | 'workers' | 'database' | 's3'
export type Health = 'critical' | 'warning' | 'ok' | 'neutral'

export interface MapNode {
  id: NodeId
  health: Health
  lines: string[]
  firing?: boolean
}

export interface MapEdge {
  from: NodeId
  to: NodeId
  active: boolean
  label?: string
}

// Small tiles far apart, so that the moving links between them stay readable.
const W = 138
const H = 66
const COLS = [10, 237, 464, 691]
const ROWS = [26, 196]

const POSITIONS: Record<NodeId, [number, number]> = {
  users: [COLS[0], ROWS[0]],
  cloudfront: [COLS[1], ROWS[0]],
  api: [COLS[2], ROWS[0]],
  workers: [COLS[3], ROWS[0]],
  assistants: [COLS[0], ROWS[1]],
  front: [COLS[1], ROWS[1]],
  database: [COLS[2], ROWS[1]],
  s3: [COLS[3], ROWS[1]],
}

const ICONS: Record<NodeId, LucideIcon> = {
  users: Users,
  assistants: Bot,
  cloudfront: Cloud,
  front: AppWindow,
  api: Server,
  workers: Cpu,
  database: Database,
  s3: HardDrive,
}

export const HEALTH_COLOURS: Record<Health, string> = { critical: '#dc2626', warning: '#d97706', ok: '#16a34a', neutral: '#cbd5e1' }

// path joins two components along the side they face.
function path(from: NodeId, to: NodeId): string {
  const [fx, fy] = POSITIONS[from]
  const [tx, ty] = POSITIONS[to]
  if (fy === ty) {
    const right = tx > fx
    return `M ${right ? fx + W : fx} ${fy + H / 2} L ${right ? tx : tx + W} ${ty + H / 2}`
  }
  if (fx === tx) {
    const down = ty > fy
    return `M ${fx + W / 2} ${down ? fy + H : fy} L ${tx + W / 2} ${down ? ty : ty + H}`
  }
  const down = ty > fy
  const x1 = tx > fx ? fx + W - 20 : fx + 20
  const x2 = tx > fx ? tx + 20 : tx + W - 20
  return `M ${x1} ${down ? fy + H : fy} L ${x2} ${down ? ty : ty + H}`
}

function midpoint(from: NodeId, to: NodeId): [number, number] {
  const [fx, fy] = POSITIONS[from]
  const [tx, ty] = POSITIONS[to]
  if (fy === ty) return [(Math.max(fx, tx) + Math.min(fx, tx) + W) / 2, fy + H / 2 - 7]
  return [(fx + tx + W) / 2 + 14, (Math.max(fy, ty) + Math.min(fy, ty) + H) / 2]
}

interface Props {
  nodes: MapNode[]
  edges: MapEdge[]
  titles: Record<NodeId, string>
  selected: NodeId | null
  onSelect: (id: NodeId | null) => void
}

/** The components of DocIbis and the flows between them, coloured by health,. */
export function ArchitectureMap({ nodes, edges, titles, selected, onSelect }: Props) {
  return (
    <svg viewBox="0 0 840 290" className="w-full h-auto select-none" role="img" aria-label="Architecture">
      <style>{'@keyframes dib-flow { to { stroke-dashoffset: -16; } } @keyframes dib-pulse { 50% { opacity: .35; } }'}</style>
      <defs>
        <marker id="dib-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="#94a3b8" />
        </marker>
        <marker id="dib-arrow-active" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse">
          <path d="M 0 0 L 10 5 L 0 10 z" fill="#2563eb" />
        </marker>
      </defs>

      {edges.map((edge) => (
        <path
          key={`${edge.from}-${edge.to}`}
          d={path(edge.from, edge.to)}
          fill="none"
          stroke={edge.active ? '#2563eb' : '#cbd5e1'}
          strokeWidth={edge.active ? 2.6 : 1.6}
          strokeDasharray={edge.active ? '8 8' : undefined}
          style={edge.active ? { animation: 'dib-flow 0.9s linear infinite' } : undefined}
          markerEnd={`url(#${edge.active ? 'dib-arrow-active' : 'dib-arrow'})`}
        />
      ))}
      {edges
        .filter((edge) => edge.label)
        .map((edge) => {
          const [x, y] = midpoint(edge.from, edge.to)
          return (
            <g key={`label-${edge.from}-${edge.to}`}>
              <rect x={x - 32} y={y - 10} width={64} height={16} rx={8} className="fill-blue-50 dark:fill-blue-950" stroke="#93c5fd" strokeWidth={0.8} />
              <text x={x} y={y + 2} textAnchor="middle" className="fill-blue-700 dark:fill-blue-300" style={{ fontSize: 10, fontWeight: 600 }}>
                {edge.label}
              </text>
            </g>
          )
        })}

      {nodes.map((node) => {
        const [x, y] = POSITIONS[node.id]
        const Icon = ICONS[node.id]
        const colour = HEALTH_COLOURS[node.health]
        const isSelected = selected === node.id
        return (
          <g
            key={node.id}
            transform={`translate(${x},${y})`}
            className="cursor-pointer"
            role="button"
            tabIndex={0}
            aria-label={titles[node.id]}
            onClick={() => onSelect(isSelected ? null : node.id)}
            onKeyDown={(e) => (e.key === 'Enter' || e.key === ' ') && onSelect(isSelected ? null : node.id)}
          >
            <title>{titles[node.id]}</title>
            <rect width={W} height={H} rx={10} className="fill-white dark:fill-gray-800" stroke={isSelected ? '#2563eb' : colour} strokeWidth={isSelected ? 3 : node.health === 'neutral' ? 1.2 : 2} />
            <rect width={5} height={H} rx={2.5} fill={colour} opacity={node.health === 'neutral' ? 0 : 1} />
            <Icon x={11} y={8} width={14} height={14} className="text-gray-500 dark:text-gray-400" />
            <text x={30} y={19} className="fill-gray-900 dark:fill-gray-100" style={{ fontSize: 11.5, fontWeight: 600 }}>
              {titles[node.id]}
            </text>
            {node.health !== 'neutral' && (
              <circle cx={W - 11} cy={15} r={4} fill={colour} style={node.firing || node.health === 'critical' ? { animation: 'dib-pulse 1.2s ease-in-out infinite' } : undefined} />
            )}
            {node.lines.slice(0, 3).map((line, i) => (
              <text key={i} x={11} y={35 + i * 12} className="fill-gray-500 dark:fill-gray-400" style={{ fontSize: 9.5 }}>
                {line.length > 26 ? `${line.slice(0, 25)}…` : line}
              </text>
            ))}
          </g>
        )
      })}
    </svg>
  )
}
