// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { ccSwitchContextWindow, ccSwitchReasoningEffort, parseCcSwitchModels, type CcSwitchImportContext } from '../ccsModelSelection'
import { buildCcSwitchImportDeeplink, getCcSwitchApplicationProfiles } from '../ccswitchImport'
import type { CcsImportApp } from '@/types/ccswitch'
import type { GroupPlatform } from '@/types'

const context = (ids: string[]): CcSwitchImportContext => ({ models: ids.map(id => ({ id, contextWindow: 1000000, reasoningEfforts: ['low', 'high'], inputModalities: ['text', 'image'] })) })
const gpt = context(['gpt-6.1-sol', 'gpt-6-astra', 'gpt-6.1-terra', 'gpt-6-luna'])
const gemini = context(['gemini-4-argon', 'gemini-4-carbon', 'gemini-4-hydrogen', 'gemini-3.8-flash'])
const native = context(['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5', 'claude-fable-5-2'])
const base = { baseUrl: 'https://api.example.com', apiKey: 'test-only-key', providerName: 'Ritel.SI API' }
function profile(platform: GroupPlatform, app: CcsImportApp, ctx: CcSwitchImportContext) {
  return getCcSwitchApplicationProfiles(platform, undefined, ctx).find(p => p.app === app)!
}
function link(platform: GroupPlatform, app: CcsImportApp, ctx: CcSwitchImportContext) {
  return new URL(buildCcSwitchImportDeeplink({ ...base, platform, context: ctx, profile: profile(platform, app, ctx) })).searchParams
}
const decode = (encoded: string) => JSON.parse(Buffer.from(encoded, 'base64').toString('utf8'))

describe('key-scoped model choices agreed with the owner', () => {
  it('offers 6.2 only when the selected key exposes it and retains older manual choices', () => {
    const models = context(['gpt-6.2-sol', 'gpt-6.2-astra', ...gpt.models.map(model => model.id)])
    const selected = profile('openai', 'codex', models)
    expect(selected.parameters.model).toBe('gpt-6.2-sol')
    expect(selected.modelOptions).toContain('gpt-6.2-astra')
    expect(profile('openai', 'claude', models).roleModels).toEqual({ opus: 'gpt-6.2-sol', fable: 'gpt-6.2-astra', sonnet: 'gpt-6.1-terra', haiku: 'gpt-6-luna' })
    expect(profile('openai', 'codex', { ...models, selectedModel: 'gpt-6.1-sol' }).parameters.model).toBe('gpt-6.1-sol')
    expect(profile('openai', 'codex', gpt).modelOptions).not.toContain('gpt-6.2-sol')
    expect(profile('openai', 'codex', gpt).parameters.model).toBe('gpt-6.1-sol')
  })

  it('does not use the OpenAI routing label as a GPT model entitlement', () => {
    const p = profile('openai', 'codex', gemini)
    expect(p.parameters.model).toBe('gemini-4-argon')
    expect(p.parameters.apiFormat).toBe('openai_chat')
    expect(p.modelOptions).not.toContain('gpt-6.1-sol')
  })
  it('preserves separate GPT roles and encodes Fable in Claude config', () => {
    const p = link('openai', 'claude', gpt)
    expect(p.get('model')).toBe('gpt-6.1-sol[1M]')
    expect(p.get('sonnetModel')).toBe('gpt-6.1-terra[1M]')
    expect(p.get('haikuModel')).toBe('gpt-6-luna[1M]')
    const env = decode(p.get('config')!).env
    expect(env.ANTHROPIC_DEFAULT_FABLE_MODEL).toBe('gpt-6-astra[1M]')
    expect(env).not.toHaveProperty('CLAUDE_CODE_SUBAGENT_MODEL')
  })
  it('keeps an unlimited GPT group inside its actual model set', () => {
    const p = profile('openai', 'claude', context(['gpt-6.1-sol', 'gpt-6-astra']))
    expect(p.roleModels).toEqual({ opus: 'gpt-6.1-sol', fable: 'gpt-6-astra', sonnet: 'gpt-6.1-sol', haiku: 'gpt-6.1-sol' })
  })
  it('uses Carbon for Fable and Hydrogen for Sonnet', () => {
    expect(profile('gemini', 'claude', gemini).roleModels).toEqual({ opus: 'gemini-4-argon', fable: 'gemini-4-carbon', sonnet: 'gemini-4-hydrogen', haiku: 'gemini-3.8-flash' })
  })
  it.each([
    ['openai', gpt, 'gpt-6-astra', 'gpt-6.1-sol', 'gpt-6.1-terra', 'gpt-6-luna'],
    ['gemini', gemini, 'gemini-4-carbon', 'gemini-4-argon', 'gemini-4-hydrogen', 'gemini-3.8-flash'],
    ['anthropic', native, 'claude-fable-5-2', 'claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5']
  ] as const)('carries all four Desktop roles through its existing import contract (%s)', (platform, ctx, fable, opus, sonnet, haiku) => {
    const p = link(platform, 'claude-desktop', ctx)
    expect(p.get('model')).toBe(fable)
    expect(p.get('opusModel')).toBe(opus)
    expect(p.get('sonnetModel')).toBe(sonnet)
    expect(p.get('haikuModel')).toBe(haiku)
    expect(p.toString()).not.toContain('%5B1M%5D')
  })
  it('removes all model overrides for native Claude even from saved old templates', () => {
    const settings = { profiles: [{ id: 'native', label: 'Claude', app: 'claude' as const, platform: 'anthropic' as const, parameters: { model: 'claude-opus-5-5', sonnetModel: 'claude-opus-5-5', config: JSON.stringify({ env: { ANTHROPIC_MODEL: 'old', ANTHROPIC_DEFAULT_FABLE_MODEL: 'old', CLAUDE_CODE_SUBAGENT_MODEL: 'old', API_TIMEOUT_MS: '600000' } }) } }] }
    const p = getCcSwitchApplicationProfiles('anthropic', settings, native).find(p => p.app === 'claude')!
    const params = new URL(buildCcSwitchImportDeeplink({ ...base, platform: 'anthropic', profile: p, context: native })).searchParams
    for (const key of ['model', 'sonnetModel', 'haikuModel', 'opusModel']) expect(params.has(key)).toBe(false)
    const env = decode(params.get('config')!).env
    expect(Object.keys(env).some(key => /MODEL/.test(key))).toBe(false)
    expect(env.API_TIMEOUT_MS).toBe('600000')
  })
  it('uses Chat and /v1 for Gemini in OpenCode', () => {
    const p = link('gemini', 'opencode', gemini)
    expect(p.get('apiFormat')).toBe('openai_chat')
    expect(p.get('endpoint')).toBe('https://api.example.com/v1')
    expect(p.get('model')).toBe('gemini-4-argon')
  })
  it('selects app-specific domestic defaults only if this key supplies them', () => {
    const unlimited = context(['deepseek-v4.1-flash', 'minimax-m3', 'kimi-k3.1-pro'])
    const standard = context([...unlimited.models.map(m => m.id), 'minimax-m3.1-flash'])
    expect(profile('deepseek', 'mcode', standard).parameters.model).toBe('minimax-m3.1-flash')
    expect(profile('deepseek', 'mcode', unlimited).parameters.model).toBe('minimax-m3')
    expect(profile('deepseek', 'kimi', unlimited).parameters.model).toBe('kimi-k3.1-pro')
    expect(profile('deepseek', 'dsh', unlimited).parameters.model).toBe('deepseek-v4.1-flash')
  })
  it('uses explicit manual choices without inventing unavailable models', () => {
    expect(profile('openai', 'codex', { ...gpt, selectedModel: 'gpt-6-astra' }).parameters.model).toBe('gpt-6-astra')
    expect(profile('openai', 'codex', { ...gpt, selectedModel: 'gpt-does-not-exist' }).parameters.model).toBe('gpt-6.1-sol')
  })
  it('fails closed for empty or non-text catalogs and unsupported native clients', () => {
    expect(() => link('openai', 'codex', context([]))).toThrow('No compatible model')
    expect(() => link('openai', 'gemini', gpt)).toThrow('No compatible model')
    expect(() => link('openai', 'codex', context(['gpt-image-2.6-flare']))).toThrow('No compatible model')
  })
  it('honors a key context cap and reasoning ceiling', () => {
    const capped = { ...gpt, allowLongContext: false, contextTokenLimit: 100000, maxReasoningEffort: 'low' }
    expect(ccSwitchContextWindow('gpt-6.1-sol', capped)).toBe(100000)
    expect(ccSwitchReasoningEffort('gpt-6.1-sol', capped)).toBe('low')
    expect(link('openai', 'claude', capped).get('model')).toBe('gpt-6.1-sol')
  })
  it('parses real catalog limits without guessing missing metadata', () => {
    const models = parseCcSwitchModels(JSON.stringify({ models: [
      { slug: 'gpt-6.1-sol', context_window: 272000, max_context_window: 1000000, max_output_tokens: 128000, supported_reasoning_levels: [{ effort: 'high' }] },
      { slug: 'gpt-6.1-sol' }, { slug: 'deepseek-*' }, { slug: 'gemini-4-argon' }
    ] }))
    expect(models).toHaveLength(2)
    expect(models[0]?.contextWindow).toBe(1000000)
    expect(models[0]?.maxOutputTokens).toBe(128000)
    expect(models[1]?.maxOutputTokens).toBeUndefined()
    expect(models[1]?.contextWindow).toBeUndefined()
  })
})


describe('review regressions: model family, isolated routes and stale Codex templates', () => {
  it.each(['composite', 'openai'] as const)('leaves native Claude model fields empty on a %s routing platform', (platform) => {
    const params = link(platform, 'claude', native)
    for (const name of ['model', 'sonnetModel', 'opusModel', 'haikuModel']) {
      expect(params.has(name)).toBe(false)
    }
    const env = decode(params.get('config')!).env
    expect(Object.keys(env).filter(key => key.includes('MODEL'))).toEqual([])
  })

  it('maps an actually selected Gemini model on Antigravity instead of treating it as native Claude', () => {
    const mixed = { ...context([...native.models.map(model => model.id), ...gemini.models.map(model => model.id)]), selectedModel: 'gemini-4-argon' }
    const p = profile('antigravity', 'claude', mixed)
    expect(p.nativeClaude).toBe(false)
    const params = link('antigravity', 'claude', mixed)
    expect(params.get('model')).toBe('gemini-4-argon[1M]')
    expect(params.get('opusModel')).toBe('gemini-4-argon[1M]')
    expect(params.get('sonnetModel')).toBe('gemini-4-hydrogen[1M]')
    expect(params.get('haikuModel')).toBe('gemini-3.8-flash[1M]')
    expect(decode(params.get('config')!).env.ANTHROPIC_DEFAULT_FABLE_MODEL).toBe('gemini-4-carbon[1M]')
    expect(params.get('endpoint')).toBe('https://api.example.com/antigravity')
  })

  it('blocks Antigravity Gemini in OpenCode without removing the isolated route prefix', () => {
    const p = profile('antigravity', 'opencode', gemini)
    expect(p.parameters.apiFormat).toBe('openai_chat')
    expect(p.unavailableReason).toBe('protocol')
    expect(() => link('antigravity', 'opencode', gemini)).toThrow('No compatible model')
    // A caller-supplied profile cannot turn this unavailable route into a link.
    expect(() => buildCcSwitchImportDeeplink({
      ...base, platform: 'antigravity', profile: { ...p, unavailableReason: undefined }
    })).toThrow('Antigravity route does not support Chat Completions')
    const claude = link('antigravity', 'opencode', native)
    expect(claude.get('apiFormat')).toBe('anthropic')
    expect(claude.get('endpoint')).toBe('https://api.example.com/antigravity/v1')
  })

  it.each(['gemini', 'composite', 'openai'] as const)('uses OpenAI Chat /v1 for authorized Gemini models on %s', (platform) => {
    const params = link(platform, 'opencode', gemini)
    expect(params.get('apiFormat')).toBe('openai_chat')
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
    expect(params.get('model')).toBe('gemini-4-argon')
  })

  it('keeps the OpenCode Gemini exception for a caller profile without apiFormat', () => {
    const params = new URL(buildCcSwitchImportDeeplink({
      ...base, platform: 'composite', context: gemini,
      profile: { id: 'explicit', label: 'OpenCode', platform: 'composite', app: 'opencode', parameters: { model: 'gemini-4-argon' } }
    })).searchParams
    expect(params.get('apiFormat')).toBe('openai_chat')
    expect(params.get('endpoint')).toBe('https://api.example.com/v1')
  })

  const oldToml = 'model_provider = "ritel"\nmodel = "gpt-6.1-sol"\nservice_tier = "priority"\n[model_providers.ritel]\nname = "Ritel.SI API"\nbase_url = "https://api.example.com/v1"\nwire_api = "responses"\nrequires_openai_auth = true\n'
  it.each([
    ['json', JSON.stringify({ auth: { OPENAI_API_KEY: 'old-test-key', extra: 'keep' }, config: oldToml + '\n[custom_section]\nsetting = true\n' })],
    ['toml', oldToml],
    ['json', '{invalid-json']
  ])('clearly rejects an inline %s override instead of importing stale Codex settings or losing fields', (configFormat, config) => {
    const settings = { profiles: [{
      id: 'old-codex', label: 'Codex', platform: 'openai' as const, app: 'codex' as const,
      parameters: { configFormat, config }
    }] }
    const original = JSON.stringify(settings)
    const p = getCcSwitchApplicationProfiles('openai', settings, gpt).find(item => item.app === 'codex')!
    expect(() => buildCcSwitchImportDeeplink({
      ...base, platform: 'openai', context: gpt, profile: p, settings
    })).toThrow('Clear the old template config')
    expect(JSON.stringify(settings)).toBe(original)
  })

  it.each(['', ' \r\n\t'])('generates the agreed Codex configuration once the old inline override is cleared (%j)', (config) => {
    const settings = { profiles: [{
      id: 'codex-cleared', label: 'Codex', platform: 'openai' as const, app: 'codex' as const,
      parameters: { configFormat: 'json', config }
    }] }
    const p = getCcSwitchApplicationProfiles('openai', settings, gpt).find(item => item.app === 'codex')!
    const params = new URL(buildCcSwitchImportDeeplink({
      ...base, platform: 'openai', context: gpt, profile: p, settings
    })).searchParams
    const result = decode(params.get('config')!)
    expect(result.auth).toEqual({ OPENAI_API_KEY: base.apiKey })
    expect(result.config).toContain('model_provider = "custom"')
    expect(result.config).toContain('[model_providers.custom]\nname = "OpenAI"')
    expect(result.config).toContain('service_tier = "default"')
    expect(result.config).toContain('model_reasoning_effort = "high"')
    expect(result.config).toContain('model_context_window = 1000000')
    expect(result.config).toContain('model_catalog_url = "https://api.example.com/v1/models"')
  })
})
