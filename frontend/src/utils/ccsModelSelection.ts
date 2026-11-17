import type { GroupPlatform } from '@/types'
import type { CcsImportApp } from '@/types/ccswitch'

export interface CcsModelInfo {
  id: string
  contextWindow?: number
  maxOutputTokens?: number
  reasoningEfforts: string[]
  inputModalities: string[]
}

export interface CcSwitchImportContext {
  models: CcsModelInfo[]
  selectedModel?: string
  allowLongContext?: boolean
  contextTokenLimit?: number
  maxReasoningEffort?: string
}

export type CcsModelFamily = 'openai' | 'anthropic' | 'gemini' | 'grok' | 'domestic'
export interface CcsRoleModels { sonnet: string; opus: string; haiku: string; fable: string }

const preferred: Record<CcsModelFamily, string[]> = {
  openai: ['gpt-6.2-sol', 'gpt-6.2-astra', 'gpt-6.1-sol', 'gpt-6-astra', 'gpt-6.1-terra', 'gpt-6-luna'],
  anthropic: ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5', 'claude-fable-5-2'],
  gemini: ['gemini-4-argon', 'gemini-4-carbon', 'gemini-4-hydrogen', 'gemini-3.8-flash'],
  grok: ['grok-4.7', 'grok-4.6', 'grok-4.5', 'grok-4.3'],
  domestic: ['deepseek-v4.1-flash', 'deepseek-v4-pro', 'glm-5.3', 'kimi-k3.1-pro', 'minimax-m3.1-flash', 'minimax-m3']
}

/** Parse only model discovery data obtained with the selected user's key. */
export function parseCcSwitchModels(content: string): CcsModelInfo[] {
  if (!content) return []
  const value: unknown = JSON.parse(content)
  if (!value || typeof value !== 'object') throw new Error('Invalid model catalog')
  const data = value as { models?: unknown; data?: unknown }
  const list = data.models ?? data.data
  if (!Array.isArray(list)) throw new Error('Invalid model catalog')
  const seen = new Set<string>()
  return list.flatMap((entry: unknown) => {
    if (!entry || typeof entry !== 'object') return []
    const model = entry as Record<string, unknown>
    const id = typeof model.slug === 'string' ? model.slug : typeof model.id === 'string' ? model.id : ''
    if (!id || id.includes('*') || seen.has(id)) return []
    seen.add(id)
    const windows = [model.context_window, model.max_context_window].filter((n): n is number => typeof n === 'number' && Number.isFinite(n) && n > 0)
    const levels = Array.isArray(model.supported_reasoning_levels) ? model.supported_reasoning_levels : []
    return [{
      id,
      contextWindow: windows.length ? Math.max(...windows) : undefined,
      maxOutputTokens: typeof model.max_output_tokens === 'number' && Number.isSafeInteger(model.max_output_tokens) && model.max_output_tokens > 0 ? model.max_output_tokens : undefined,
      reasoningEfforts: levels.flatMap(level => typeof level?.effort === 'string' ? [level.effort] : []),
      inputModalities: Array.isArray(model.input_modalities) ? model.input_modalities.filter((v): v is string => typeof v === 'string') : []
    }]
  })
}

export function ccSwitchModelFamily(model: string): CcsModelFamily | undefined {
  if (model.startsWith('gpt-')) return 'openai'
  if (model.startsWith('claude-')) return 'anthropic'
  if (model.startsWith('gemini-')) return 'gemini'
  if (model.startsWith('grok-')) return 'grok'
  if (/^(?:deepseek-|DeepSeek-|glm-|kimi-|minimax-)/.test(model)) return 'domestic'
  return undefined
}

function platformFamily(platform: GroupPlatform): CcsModelFamily {
  if (platform === 'anthropic' || platform === 'antigravity') return 'anthropic'
  if (platform === 'openai' || platform === 'composite') return 'openai'
  if (platform === 'gemini') return 'gemini'
  if (platform === 'grok') return 'grok'
  return 'domestic'
}

export function ccSwitchModelOptions(platform: GroupPlatform, context?: CcSwitchImportContext): string[] {
  const family = platformFamily(platform)
  if (!context) return [...preferred[family]]
  const available = new Set(context.models.map(model => model.id))
  // Imported keys can expose a different family from their routing platform
  // (for example Gemini through an OpenAI Chat group).
  const order = [family, ...Object.keys(preferred).filter(key => key !== family) as CcsModelFamily[]]
  const options = order.flatMap(key => preferred[key].filter(id => available.has(id)))
  const grokVariants = context.models.map(model => model.id).filter(id => /^grok-4\.\d+(?:-|$)/.test(id) && !options.includes(id))
  return [...options, ...grokVariants]
}

export function selectCcSwitchModel(platform: GroupPlatform, app: CcsImportApp, context?: CcSwitchImportContext): { model: string; options: string[]; family: CcsModelFamily } {
  let options = ccSwitchModelOptions(platform, context)
  if (app === 'gemini' || app === 'agy') options = options.filter(id => id.startsWith('gemini-'))
  const available = new Set(options)
  const family = ccSwitchModelFamily(options[0] ?? '') ?? platformFamily(platform)
  let candidates = preferred[family]
  if (family === 'domestic' && app === 'mcode') candidates = ['minimax-m3.1-flash', 'minimax-m3', ...candidates]
  if (family === 'domestic' && app === 'kimi') candidates = ['kimi-k3.1-pro', ...candidates]
  const model = context?.selectedModel && available.has(context.selectedModel)
    ? context.selectedModel : candidates.find(id => available.has(id)) ?? options[0] ?? ''
  return { model, options, family: ccSwitchModelFamily(model) ?? family }
}

export function ccSwitchRoleModels(family: CcsModelFamily, model: string, options: string[]): CcsRoleModels {
  const available = new Set(options)
  const pick = (id: string) => available.has(id) ? id : model
  switch (family) {
    case 'openai': return {
      opus: pick(available.has('gpt-6.2-sol') ? 'gpt-6.2-sol' : 'gpt-6.1-sol'),
      fable: pick(available.has('gpt-6.2-astra') ? 'gpt-6.2-astra' : 'gpt-6-astra'),
      sonnet: pick('gpt-6.1-terra'), haiku: pick('gpt-6-luna')
    }
    case 'gemini': return { opus: pick('gemini-4-argon'), fable: pick('gemini-4-carbon'), sonnet: pick('gemini-4-hydrogen'), haiku: pick('gemini-3.8-flash') }
    case 'anthropic': return { opus: pick('claude-opus-5-5'), fable: pick('claude-fable-5-2'), sonnet: pick('claude-sonnet-5-5'), haiku: pick('claude-haiku-5-5') }
    case 'grok': { const target = pick('grok-4.7'); return { opus: target, fable: target, sonnet: target, haiku: target } }
    case 'domestic': { const target = pick('deepseek-v4.1-flash'); return { opus: target, fable: target, sonnet: target, haiku: target } }
  }
}

export function ccSwitchContextWindow(model: string, context?: CcSwitchImportContext): number | undefined {
  const size = context?.models.find(item => item.id === model)?.contextWindow
  if (!size) return undefined
  const keyLimit = context?.contextTokenLimit && context.contextTokenLimit > 0 ? context.contextTokenLimit : Infinity
  return Math.min(size, keyLimit, context?.allowLongContext === false ? 200000 : Infinity)
}

export function ccSwitchReasoningEffort(model: string, context?: CcSwitchImportContext): string | undefined {
  const levels = context?.models.find(item => item.id === model)?.reasoningEfforts ?? []
  if (!levels.includes('high')) return undefined
  const ceiling = context?.maxReasoningEffort
  if (ceiling && ['none', 'minimal', 'low', 'medium'].includes(ceiling)) return levels.includes(ceiling) ? ceiling : undefined
  return 'high'
}

export function ccSwitchApiFormat(platform: GroupPlatform, app: CcsImportApp, family: CcsModelFamily): string {
  if (app === 'gemini' || app === 'agy') return 'gemini_native'
  // OpenCode's custom Gemini endpoint must use Chat, regardless of the key's
  // platform label. The caller separately checks the platform's route support.
  if (app === 'opencode' && family === 'gemini') return 'openai_chat'
  if (platform === 'antigravity') return family === 'gemini' ? 'gemini_native' : 'anthropic'
  if (platform === 'anthropic') return 'anthropic'
  if (family === 'gemini') {
    // The explicitly Chat-only Gemini group is routed as OpenAI. OpenCode's
    // custom Gemini endpoints also use Chat, as required by the owner.
    if (platform === 'openai' || app === 'opencode') return 'openai_chat'
    return ['codex', 'grokbuild', 'hermes', 'mcode', 'dsh'].includes(app) ? 'openai_responses' : 'gemini_native'
  }
  if (family === 'anthropic' && platform === 'composite') return 'anthropic'
  return family === 'openai' ? 'openai_responses' : 'openai_chat'
}
