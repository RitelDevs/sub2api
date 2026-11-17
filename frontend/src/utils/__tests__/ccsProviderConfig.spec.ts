// @vitest-environment node
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { CcsImportApp } from '@/types/ccswitch'
import type { CcSwitchApplicationProfile } from '../ccswitchImport'
import { parseCcSwitchModels, type CcSwitchImportContext } from '../ccsModelSelection'
import { buildCcSwitchProviderConfig, type CcSwitchProviderConfigInput } from '../ccsProviderConfig'

const apps = ['opencode', 'openclaw', 'hermes', 'pi', 'mcode', 'kimi', 'dsh', 'omp'] as const
const providerId = 'REPLACE_WITH_PROVIDER_ID'
const sol = 'gpt-6.1-sol'
const astra = 'gpt-6-astra'
const context: CcSwitchImportContext = {
  models: [
    { id: sol, contextWindow: 1000000, maxOutputTokens: 32768, reasoningEfforts: ['low', 'high'], inputModalities: ['text', 'image'] },
    { id: astra, contextWindow: 262144, maxOutputTokens: 8192, reasoningEfforts: ['high'], inputModalities: ['text'] },
    { id: 'not-authorized-by-profile', contextWindow: 2000000, reasoningEfforts: ['high'], inputModalities: ['text'] }
  ]
}
function profile(app: CcsImportApp): CcSwitchApplicationProfile {
  return {
    id: `openai-${app}`, app, label: app, platform: 'openai',
    parameters: { model: sol, apiFormat: 'openai_responses' }, modelOptions: [sol, astra]
  }
}
function build(app: CcsImportApp, overrides: Partial<CcSwitchProviderConfigInput> = {}) {
  return buildCcSwitchProviderConfig({ profile: profile(app), baseUrl: 'https://api.example.test', apiKey: 'sk-input-only', context, ...overrides })!
}
// The two YAML editors accept JSON's YAML 1.2 subset, including their required
// single-provider wrapper. Parse it independently of the output implementation.
function node(app: CcsImportApp, content: string): any {
  const parsed = JSON.parse(content)
  if (app === 'mcode') return parsed.custom_provider[providerId]
  if (app === 'omp') return parsed.providers[providerId]
  return parsed
}
function entries(app: CcsImportApp, content: string): any[] {
  const models = node(app, content).models
  return Array.isArray(models) ? models : Object.entries(models).map(([id, model]) => ({ id, ...(model as object) }))
}
function windows(app: CcsImportApp, content: string): number[] {
  if (app === 'kimi') return [...content.matchAll(/^max_context_size = (\d+)$/gm)].map(match => Number(match[1]))
  return entries(app, content).map(model => model.limit?.context ?? model.contextWindow ?? model.context_length).filter(value => value !== undefined)
}

beforeEach(() => vi.stubGlobal('fetch', vi.fn(() => { throw new Error('Network access is forbidden in provider serialization') })))
afterEach(() => vi.unstubAllGlobals())

describe('single-provider editor payloads', () => {
  it.each(apps)('keeps every authorized model in %s without adding models from metadata or roles', app => {
    const inputProfile = profile(app)
    inputProfile.modelOptions!.push(sol)
    inputProfile.roleModels = { opus: sol, sonnet: 'role-not-authorized', haiku: sol, fable: astra }
    const result = build(app, { profile: inputProfile })
    expect(result.content).not.toContain('not-authorized')
    if (app === 'kimi') {
      expect(result.content.match(/^\[models\./gm)).toHaveLength(2)
      for (const id of [sol, astra]) {
        expect(result.content).toContain(`[models."${providerId}/${id}"]`)
        expect(result.content).toContain(`model = "${id}"`)
      }
    } else {
      expect(entries(app, result.content).map(model => model.id)).toEqual([sol, astra])
    }
    expect(windows(app, result.content)).toEqual([1000000, 262144])
    expect(fetch).not.toHaveBeenCalled()
  })

  it.each(['claude', 'claude-desktop', 'codex', 'gemini', 'agy', 'grokbuild'] as const)('returns null for %s', app => {
    expect(build(app)).toBeNull()
  })

  it('does not restore an unauthorized default when the authorized list is empty', () => {
    expect(() => build('pi', { profile: { ...profile('pi'), modelOptions: [] } })).toThrow('No authorized models')
  })

  it('puts the explicitly selected authorized model first without changing the input', () => {
    const input = { ...profile('hermes'), parameters: { model: astra, apiFormat: 'openai_responses' } }
    const result = build('hermes', { profile: input })
    expect(entries('hermes', result.content).map(model => model.id)).toEqual([astra, sol])
    expect(input.modelOptions).toEqual([sol, astra])
    expect(context.models).toHaveLength(3)
  })

  it.each(['mcode', 'omp'] as const)('uses exactly one %s provider wrapper and requires its current ID', app => {
    const result = build(app)
    const wrapper = app === 'mcode' ? 'custom_provider' : 'providers'
    const parsed = JSON.parse(result.content)
    expect(Object.keys(parsed)).toEqual([wrapper])
    expect(Object.keys(parsed[wrapper])).toEqual([providerId])
    expect(result.language).toBe('yaml')
    expect(result.requiresProviderId).toBe(true)
    expect(node(app, result.content)).not.toHaveProperty('defaultModel')
    expect(node(app, result.content)).not.toHaveProperty('thinkingLevelMap')
  })

  it('uses Kimi native tables scoped to the current provider, not global defaults', () => {
    const result = build('kimi')
    expect(result.language).toBe('toml')
    expect(result.requiresProviderId).toBe(true)
    expect(result.content.match(/^\[.*\]$/gm)).toEqual([
      `[providers."${providerId}"]`, `[models."${providerId}/${sol}"]`, `[models."${providerId}/${astra}"]`
    ])
    expect(result.content.match(/^provider = "REPLACE_WITH_PROVIDER_ID"$/gm)).toHaveLength(2)
    expect(result.content).toContain('capabilities = ["tool_use", "image_in", "thinking"]')
    expect(result.content).not.toMatch(/^(default_model|default_effort|model_provider) =/m)
  })

  it.each(['opencode', 'openclaw', 'hermes', 'pi', 'dsh'] as const)('emits only a %s provider JSON node', app => {
    const result = build(app)
    expect(result.language).toBe('json')
    expect(result.requiresProviderId).toBe(false)
    const parsed = JSON.parse(result.content)
    expect(parsed).not.toHaveProperty('providers')
    expect(parsed).not.toHaveProperty('agents')
    expect(parsed).not.toHaveProperty('mcpServers')
  })
})

describe('verified limits and capabilities', () => {
  it.each(apps)('honors both the key cap and long-context policy in %s', app => {
    expect(windows(app, build(app, { context: { ...context, contextTokenLimit: 120000, allowLongContext: false } }).content))
      .toEqual([120000, 120000])
    expect(windows(app, build(app, { context: { ...context, allowLongContext: false } }).content))
      .toEqual([200000, 200000])
  })

  it.each(apps)('does not invent a 1M window or output budget in %s', app => {
    const unknown = { models: [sol, astra].map(id => ({ id, reasoningEfforts: [], inputModalities: [] })) }
    const result = build(app, { profile: { ...profile(app), contextWindow: 1000000 }, context: unknown })
    expect(windows(app, result.content)).toEqual([])
    expect(result.content).not.toContain('1000000')
    if (app === 'kimi') {
      expect(result.formatLabel).toContain('max_context_size')
      expect(result.content).toContain('# Required: fill max_context_size')
      expect(result.content).not.toContain('max_output_size =')
    } else {
      for (const model of entries(app, result.content)) {
        expect(model).not.toHaveProperty('maxTokens')
        expect(model).not.toHaveProperty('max_tokens')
        expect(model.limit).toBeUndefined()
        expect(model).not.toHaveProperty('reasoning')
      }
      if (app === 'pi' || app === 'omp') expect(result.formatLabel).toContain('maxTokens')
    }
  })

  it('writes actual output limits separately from context limits', () => {
    expect(entries('opencode', build('opencode').content)[0].limit).toEqual({ context: 1000000, output: 32768 })
    expect(entries('openclaw', build('openclaw').content)[0].maxTokens).toBe(32768)
    expect(entries('hermes', build('hermes').content)[0]).toMatchObject({ context_length: 1000000, max_tokens: 32768, supports_vision: true })
  })

  it.each(['pi', 'omp'] as const)('keeps %s explicitly incomplete when only Terra publishes an output limit', app => {
    const terra = 'gpt-6.1-terra'
    const catalog = { models: parseCcSwitchModels(JSON.stringify({ models: [
      { slug: sol, context_window: 1000000 },
      { slug: terra, context_window: 1000000, max_output_tokens: 128000 }
    ] })) }
    const result = build(app, { profile: { ...profile(app), modelOptions: [sol, terra] }, context: catalog })
    const models = entries(app, result.content)
    expect(models[0]).not.toHaveProperty('maxTokens')
    expect(models[1].maxTokens).toBe(128000)
    expect(result.formatLabel).toContain('必填 maxTokens 缺失')
    expect(result.formatLabel).toContain('补齐后再保存')
    expect(result.formatLabel).not.toContain('contextWindow')
  })

  it('omits malformed numeric metadata rather than emitting null or fictional budgets', () => {
    const invalid = { models: [{ id: sol, contextWindow: Infinity, maxOutputTokens: 0, reasoningEfforts: [], inputModalities: [] }] }
    const first = entries('pi', build('pi', { context: invalid }).content)[0]
    expect(first).not.toHaveProperty('contextWindow')
    expect(first).not.toHaveProperty('maxTokens')
  })

  it('uses the real OpenAI Responses SDK option only with confirmed effort support', () => {
    expect(entries('opencode', build('opencode').content)[0].options).toEqual({ reasoningEffort: 'high' })
    const capped = build('opencode', { context: { ...context, maxReasoningEffort: 'low' } })
    expect(entries('opencode', capped.content)[0].options).toEqual({ reasoningEffort: 'low' })
    expect(entries('opencode', capped.content)[1]).not.toHaveProperty('options')
    const unsupported = { models: [{ ...context.models[0]!, reasoningEfforts: ['medium'] }] }
    expect(entries('opencode', build('opencode', { context: unsupported }).content)[0]).not.toHaveProperty('options')
  })

  it('retains capabilities without inventing an effort option for another SDK dialect', () => {
    const result = build('opencode', { profile: { ...profile('opencode'), parameters: { model: sol, apiFormat: 'openai_chat' } } })
    const first = entries('opencode', result.content)[0]
    expect(first.reasoning).toBe(true)
    expect(first).not.toHaveProperty('options')
  })

  it('uses DSH effort support rather than allowing its writer to invent unsupported levels', () => {
    const second = entries('dsh', build('dsh').content)[1]
    expect(second.reasoningEfforts).toEqual({ high: 'high' })
    expect(second).not.toHaveProperty('options')
    expect(second).not.toHaveProperty('default_effort')
  })

  it.each(['pi', 'omp', 'openclaw'] as const)('only emits supported input modalities in %s', app => {
    const extra = { models: [{ ...context.models[0]!, inputModalities: ['text', 'image', 'audio', 'pdf'] }] }
    const model = entries(app, build(app, { context: extra }).content)[0]
    expect(model.input).toEqual(['text', 'image'])
    expect(model).not.toHaveProperty('thinkingLevelMap')
    expect(model).not.toHaveProperty('reasoning_effort')
  })
})

describe('connection and credential fidelity', () => {
  it.each(apps)('uses only the selected key in %s, with lossless quoted strings', app => {
    const apiKey = 'sk-input-"quoted"\\path\n中文\u007f\u0080'
    const input = profile(app)
    input.parameters.apiKey = 'sk-wrong-parameter'
    input.parameters.config = JSON.stringify({ apiKey: 'sk-wrong-config', models: { unauthorized: {} } })
    const result = build(app, { apiKey, profile: input })
    expect(result.content).not.toContain('sk-wrong')
    expect(result.content).not.toContain('unauthorized')
    if (result.language === 'yaml') expect(result.content).not.toMatch(/[\u007f-\u009f]/)
    if (app === 'kimi') {
      const keys = [...result.content.matchAll(/^api_key = (.+)$/gm)]
      expect(keys).toHaveLength(1)
      expect(JSON.parse(keys[0]![1]!)).toBe(apiKey)
    } else {
      const parsed = node(app, result.content)
      expect(parsed.apiKey ?? parsed.api_key ?? parsed.options?.apiKey).toBe(apiKey)
    }
  })

  it.each([
    ['opencode', 'anthropic', 'npm', '@ai-sdk/anthropic', '/v1'],
    ['opencode', 'openai_chat', 'npm', '@ai-sdk/openai-compatible', '/v1'],
    ['openclaw', 'anthropic', 'api', 'anthropic-messages', ''],
    ['openclaw', 'gemini_native', 'api', 'google-generative-ai', '/v1beta'],
    ['hermes', 'openai_responses', 'api_mode', 'codex_responses', '/v1'],
    ['hermes', 'anthropic', 'api_mode', 'anthropic_messages', ''],
    ['hermes', 'openai_chat', 'api_mode', 'chat_completions', '/v1'],
    ['pi', 'gemini_native', 'api', 'google-generative-ai', '/v1beta'],
    ['mcode', 'openai_chat', 'api', 'openai-completions', '/v1'],
    ['dsh', 'openai_responses', 'api', 'openai-responses', '/v1'],
    ['omp', 'anthropic', 'api', 'anthropic-messages', '']
  ] as const)('matches the %s / %s native connection format', (app, apiFormat, field, expected, suffix) => {
    const result = build(app, { profile: { ...profile(app), parameters: { model: sol, apiFormat } }, baseUrl: 'https://api.example.test/v1beta/' })
    const parsed = node(app, result.content)
    expect(parsed[field]).toBe(expected)
    expect(parsed.baseUrl ?? parsed.base_url ?? parsed.options?.baseURL).toBe(`https://api.example.test${suffix}`)
  })

  it.each(['gemini', 'composite'] as const)('always routes OpenCode Gemini models through Chat /v1 (%s)', platform => {
    const input = { ...profile('opencode'), platform, parameters: { model: 'gemini-4-argon', apiFormat: 'gemini_native', endpoint: '{{baseUrl}}/v1beta/' }, modelOptions: ['gemini-4-argon', 'gemini-4-carbon'] }
    const parsed = node('opencode', build('opencode', { profile: input }).content)
    expect(parsed.npm).toBe('@ai-sdk/openai-compatible')
    expect(parsed.options.baseURL).toBe('https://api.example.test/v1')
    expect(Object.keys(parsed.models)).toEqual(['gemini-4-argon', 'gemini-4-carbon'])
    expect(parsed.models['gemini-4-argon']).not.toHaveProperty('options')
  })

  it('retains the first explicit endpoint without inventing failover or rewriting its path', () => {
    const input = { ...profile('pi'), parameters: { apiFormat: 'openai_responses', endpoint: 'https://first.example.test/gateway/,https://second.example.test/v1' } }
    const parsed = node('pi', build('pi', { profile: input }).content)
    expect(parsed.baseUrl).toBe('https://first.example.test/gateway')
  })

  it('keeps the Antigravity gateway prefix used by the import link', () => {
    const input = { ...profile('opencode'), platform: 'antigravity' as const, parameters: { apiFormat: 'anthropic' } }
    expect(node('opencode', build('opencode', { profile: input }).content).options.baseURL).toBe('https://api.example.test/antigravity/v1')
  })

  it.each(['hermes', 'mcode', 'dsh'] as const)('rejects unsupported native Gemini for %s rather than changing protocols silently', app => {
    expect(() => build(app, { profile: { ...profile(app), parameters: { apiFormat: 'gemini_native' } } })).toThrow('Unsupported API format')
  })
})
