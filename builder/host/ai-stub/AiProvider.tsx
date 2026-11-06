/** Inert stand-in for upstream src/builder/ai/AiProvider.tsx (no-AI build). */
import type { ReactNode } from 'react'

type SettingsTab = 'providers' | 'usage' | 'behavior'

export interface AiMessage {
  id: string
  role: 'user' | 'assistant'
  text: string
  status: 'thinking' | 'pending' | 'running' | 'done' | 'stopped'
}

export interface AiSettings {
  active: string
  models: Record<string, string>
  keys: Record<string, string>
  autoApply: boolean
  speed: 'slow' | 'normal' | 'fast'
  showCursor: boolean
  voice: string
  budget: number
}

export interface UsageRecord {
  at: number
  provider: string
  model: string
  input: number
  output: number
  cost: number
}

export interface LogEntry {
  id: string
  label: string
  at: number
}

export interface ModelDef {
  id: string
  name: string
  in: number
  out: number
  unknown?: boolean
}

export interface AiApi {
  /** Always false in this build; the seam for a later AI-capable host. */
  enabled: false
  messages: AiMessage[]
  running: boolean
  send: (text: string, attachments: unknown[]) => void
  stop: () => void
  approve: (id: string) => void
  reject: (id: string) => void
  undoRun: (id: string) => void
  clear: () => void
  focus: null
  settings: AiSettings
  setSettings: (s: AiSettings) => void
  usage: UsageRecord[]
  resetUsage: () => void
  totals: { input: number; output: number; cost: number; requests: number }
  log: LogEntry[]
  settingsOpen: SettingsTab | null
  setSettingsOpen: (t: SettingsTab | null) => void
  engineLabel: string
  live: boolean
  modelsFor: (providerId: string) => ModelDef[]
  modelInfo: (providerId: string, modelId?: string) => ModelDef
  selectModel: (providerId: string, modelId: string) => void
  refreshModels: (providerId: string) => Promise<void>
  discovery: Record<string, { busy?: boolean; error?: string; count?: number; fetchedAt?: number }>
}

const noop = () => {}
const NO_MODEL: ModelDef = { id: '', name: '', in: 0, out: 0, unknown: true }

const INERT: AiApi = Object.freeze({
  enabled: false as const,
  messages: [],
  running: false,
  send: noop,
  stop: noop,
  approve: noop,
  reject: noop,
  undoRun: noop,
  clear: noop,
  focus: null,
  settings: Object.freeze({ active: '', models: {}, keys: {}, autoApply: false, speed: 'normal' as const, showCursor: false, voice: '', budget: 0 }),
  setSettings: noop,
  usage: [],
  resetUsage: noop,
  totals: Object.freeze({ input: 0, output: 0, cost: 0, requests: 0 }),
  log: [],
  settingsOpen: null,
  setSettingsOpen: noop,
  engineLabel: '',
  live: false,
  modelsFor: () => [],
  modelInfo: () => NO_MODEL,
  selectModel: noop,
  refreshModels: () => Promise.resolve(),
  discovery: {},
})

/** Inert AI state; usable outside any provider. */
export const useAi = (): AiApi => INERT

/** Passes its children through; there is no AI context in this build. */
export function AiProvider({ children }: { children: ReactNode }) {
  return <>{children}</>
}

export const PROVIDERS: never[] = []
