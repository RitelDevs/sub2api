import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import type { ApiKey, GroupPlatform, PublicSettings } from '@/types'

const { copyToClipboardMock, saveAsMock } = vi.hoisted(() => ({
  copyToClipboardMock: vi.fn().mockResolvedValue(true), saveAsMock: vi.fn()
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: copyToClipboardMock }) }))
vi.mock('file-saver', () => ({ saveAs: saveAsMock }))
import UseKeyModal from '../UseKeyModal.vue'

enableAutoUnmount(afterEach)
const approved = [
  'gpt-6.1-sol', 'gpt-6-astra', 'gpt-6.1-terra', 'gpt-6-luna',
  'claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5', 'claude-fable-5-2',
  'gemini-4-argon', 'gemini-4-carbon', 'gemini-4-hydrogen', 'gemini-3.8-flash',
  'grok-4.7', 'grok-4.6', 'grok-4.3', 'grok-4.20-multi-agent-0309',
  'deepseek-v4.1-flash', 'deepseek-v4-pro', 'glm-5.3', 'kimi-k3.1-pro', 'minimax-m3.1-flash', 'minimax-m3'
]
function catalog(ids = approved) {
  return { models: ids.map(slug => ({ slug, context_window: 272000, max_context_window: 1050000,
    supported_reasoning_levels: [{ effort: 'low' }, { effort: 'medium' }, { effort: 'high' }],
    default_reasoning_level: 'medium', input_modalities: ['text', 'image'] })) }
}
function response(payload: unknown) {
  return { ok: true, status: 200, text: async () => JSON.stringify(payload) }
}
function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const record = { id: 8, name: 'Test key', user_id: 42, key: 'sk-fixture', group: { platform: 'openai' } } as ApiKey
function render(platform: GroupPlatform = 'openai', extra: Record<string, unknown> = {}) {
  return mount(UseKeyModal, {
    props: { show: true, apiKey: record.key, baseUrl: 'https://example.com/v1/', platform, keyRecord: record, allowMessagesDispatch: true, ...extra },
    global: { stubs: { BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, Icon: true } }
  })
}
type Wrapper = ReturnType<typeof render>
const code = (wrapper: Wrapper) => wrapper.findAll('pre code').map(item => item.text())
const toml = (wrapper: Wrapper) => code(wrapper).find(value => value.includes('model_provider = "custom"'))!
const importButton = (wrapper: Wrapper) => wrapper.get('[data-testid^="connection-ccs-"]')
const chooseApp = (wrapper: Wrapper, app: string) => wrapper.get(`[data-app-tab="${app}"]`).trigger('click')
const select = (wrapper: Wrapper) => wrapper.get('[data-testid="application-model-select"]')
let fetchMock: ReturnType<typeof vi.fn>
beforeEach(() => {
  fetchMock = vi.fn().mockResolvedValue(response(catalog()))
  vi.stubGlobal('fetch', fetchMock)
  copyToClipboardMock.mockClear()
  saveAsMock.mockClear()
})
afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks() })

describe('UseKeyModal authorized model discovery', () => {
  it('loads with this key on opening, disables import until ready, and shares one request across apps', async () => {
    const pending = deferred<ReturnType<typeof response>>()
    fetchMock.mockReturnValueOnce(pending.promise)
    const wrapper = render('anthropic')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(fetchMock.mock.calls[0][0]).toBe('https://example.com/v1/models?client_version=0.158.0')
    expect(fetchMock.mock.calls[0][1]).toMatchObject({ headers: { Authorization: 'Bearer sk-fixture' }, cache: 'no-store' })
    expect(importButton(wrapper).attributes('disabled')).toBeDefined()
    expect(code(wrapper)).toEqual([])
    await chooseApp(wrapper, 'pi')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    pending.resolve(response(catalog()))
    await flushPromises()
    expect(importButton(wrapper).attributes('disabled')).toBeUndefined()
    await chooseApp(wrapper, 'claude')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('shows a retry after failure and never enables an unverified fallback', async () => {
    fetchMock.mockRejectedValueOnce(new Error('network unavailable'))
    const wrapper = render()
    await flushPromises()
    expect(wrapper.get('[data-testid="model-selection-status"]').attributes('role')).toBe('alert')
    expect(importButton(wrapper).attributes('disabled')).toBeDefined()
    expect(code(wrapper)).toEqual([])
    await wrapper.get('[data-testid="models-retry"]').trigger('click')
    await flushPromises()
    expect(importButton(wrapper).attributes('disabled')).toBeUndefined()
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it.each([[], ['gpt-5.4'], ['unknown-model']].map(ids => ({ ids })))('disables import and manual configuration without an approved model: $ids', async ({ ids }) => {
    fetchMock.mockResolvedValue(response(catalog(ids)))
    const wrapper = render()
    await flushPromises()
    expect(importButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="model-selection-status"]').text()).toContain('no approved model')
    expect(wrapper.find('[data-testid="application-model-select"]').exists()).toBe(false)
    expect(code(wrapper)).toEqual([])
  })

  it('aborts on a key change, rejects stale responses and forgets choices from the old key', async () => {
    const wrapper = render()
    await flushPromises()
    await select(wrapper).setValue('gpt-6-astra')
    const oldRequest = deferred<ReturnType<typeof response>>()
    const newRequest = deferred<ReturnType<typeof response>>()
    fetchMock.mockReturnValueOnce(oldRequest.promise).mockReturnValueOnce(newRequest.promise)
    await wrapper.setProps({ apiKey: 'sk-second', keyRecord: { ...record, id: 9 } })
    const oldSignal = fetchMock.mock.calls[1][1].signal as AbortSignal
    expect(importButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.setProps({ apiKey: 'sk-third', keyRecord: { ...record, id: 10 } })
    expect(oldSignal.aborted).toBe(true)
    newRequest.resolve(response(catalog(['gpt-6.1-sol', 'gpt-6-luna'])))
    await flushPromises()
    expect((select(wrapper).element as HTMLSelectElement).value).toBe('gpt-6.1-sol')
    oldRequest.resolve(response(catalog(['gpt-6-astra'])))
    await flushPromises()
    expect(select(wrapper).findAll('option').map(option => option.text())).toEqual(['gpt-6.1-sol', 'gpt-6-luna'])
  })

  it('aborts on close and unmount and ignores a late rejection', async () => {
    const pending = deferred<ReturnType<typeof response>>()
    fetchMock.mockReturnValueOnce(pending.promise)
    const wrapper = render()
    const signal = fetchMock.mock.calls[0][1].signal as AbortSignal
    await wrapper.setProps({ show: false })
    expect(signal.aborted).toBe(true)
    pending.reject(new Error('late failure'))
    await flushPromises()
    expect(wrapper.find('[data-testid="models-retry"]').exists()).toBe(false)
    const next = deferred<ReturnType<typeof response>>()
    fetchMock.mockReturnValueOnce(next.promise)
    await wrapper.setProps({ show: true })
    expect(fetchMock).toHaveBeenCalledTimes(2)
    const nextSignal = fetchMock.mock.calls[1][1].signal as AbortSignal
    wrapper.unmount()
    expect(nextSignal.aborted).toBe(true)
    next.resolve(response(catalog()))
    await flushPromises()
  })

  it('remembers selections per application within the same key and does not take the first catalog item', async () => {
    fetchMock.mockResolvedValue(response(catalog(['gpt-6-luna', 'gpt-6-astra', 'gpt-6.1-sol'])))
    const wrapper = render()
    await flushPromises()
    expect((select(wrapper).element as HTMLSelectElement).value).toBe('gpt-6.1-sol')
    await select(wrapper).setValue('gpt-6-astra')
    expect(toml(wrapper)).toContain('model = "gpt-6-astra"')
    await chooseApp(wrapper, 'opencode')
    expect((select(wrapper).element as HTMLSelectElement).value).toBe('gpt-6.1-sol')
    await select(wrapper).setValue('gpt-6-luna')
    await chooseApp(wrapper, 'codex')
    expect((select(wrapper).element as HTMLSelectElement).value).toBe('gpt-6-astra')
    await chooseApp(wrapper, 'opencode')
    expect(JSON.parse(code(wrapper)[0]).model).toBe('ritel/gpt-6-luna')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })
})

describe('UseKeyModal model roles and manual output', () => {
  it('leaves native Claude models, all tiers and Subagent unset across every shell and JSON', async () => {
    fetchMock.mockResolvedValue(response(catalog(approved.filter(id => id.startsWith('claude-')))))
    const wrapper = render('anthropic')
    await flushPromises()
    expect(wrapper.get('[data-testid="native-claude-model"]').text()).toContain('Follow Claude Code')
    expect(wrapper.find('[data-testid="application-model-select"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="model-role-table"]').text()).toContain('Inherit the session')
    for (const shell of ['macOS / Linux', 'Windows CMD', 'PowerShell']) {
      await wrapper.findAll('button').find(button => button.text().trim() === shell)!.trigger('click')
      const all = code(wrapper).join('\n')
      expect(all).toContain('ANTHROPIC_BASE_URL')
      expect(all).toContain('CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC')
      expect(all).not.toContain('ANTHROPIC_MODEL')
      expect(all).not.toContain('ANTHROPIC_DEFAULT_')
      expect(all).not.toContain('CLAUDE_CODE_SUBAGENT_MODEL')
      expect(all).not.toContain('CLAUDE_CODE_ATTRIBUTION_HEADER')
      expect(all).not.toContain('[1M]')
      const settings = JSON.parse(code(wrapper).find(value => value.startsWith('{'))!)
      expect(settings.env.CLAUDE_CODE_EFFORT_LEVEL).toBe('high')
    }
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    await importButton(wrapper).trigger('click')
    expect(new URL(String(open.mock.calls[0][0])).searchParams.has('model')).toBe(false)
  })

  it.each([
    ['openai', 'gpt-6-astra', 'gpt-6.1-terra', 'gpt-6-luna'],
    ['gemini', 'gemini-4-carbon', 'gemini-4-hydrogen', 'gemini-3.8-flash'],
    ['grok', 'grok-4.7', 'grok-4.7', 'grok-4.7'],
    ['deepseek', 'deepseek-v4.1-flash', 'deepseek-v4.1-flash', 'deepseek-v4.1-flash']
  ])('shows and copies the confirmed %s role mappings including Fable', async (platform, fable, sonnet, haiku) => {
    const wrapper = render(platform)
    await flushPromises()
    await chooseApp(wrapper, 'claude')
    const roles = wrapper.get('[data-testid="model-role-table"]').text()
    for (const model of [fable, sonnet, haiku]) expect(roles).toContain(model)
    if (wrapper.find('[data-testid="advanced-configuration"]').exists()) {
      const settings = JSON.parse(code(wrapper).find(value => value.startsWith('{'))!)
      expect(settings.env.ANTHROPIC_DEFAULT_FABLE_MODEL).toBe(`${fable}[1M]`)
      expect(settings.env.ANTHROPIC_DEFAULT_SONNET_MODEL).toBe(`${sonnet}[1M]`)
      expect(settings.env.ANTHROPIC_DEFAULT_HAIKU_MODEL).toBe(`${haiku}[1M]`)
      expect(settings.env).not.toHaveProperty('CLAUDE_CODE_SUBAGENT_MODEL')
    }
  })

  it('marks each Claude role and default independently from verified windows in every manual shell', async () => {
    fetchMock.mockResolvedValue(response({ models: [
      { slug: 'gpt-6.1-sol', context_window: 1050000, supported_reasoning_levels: [{ effort: 'high' }] },
      { slug: 'gpt-6-astra', context_window: 1000000 },
      { slug: 'gpt-6.1-terra', context_window: 999999 },
      { slug: 'gpt-6-luna' }
    ] }))
    const wrapper = render()
    await flushPromises()
    await chooseApp(wrapper, 'claude')
    for (const shell of ['macOS / Linux', 'Windows CMD', 'PowerShell']) {
      await wrapper.findAll('button').find(button => button.text().trim() === shell)!.trigger('click')
      const blocks = code(wrapper)
      const env = JSON.parse(blocks.find(value => value.startsWith('{'))!).env
      expect(env).toMatchObject({
        ANTHROPIC_MODEL: 'gpt-6.1-sol[1M]',
        ANTHROPIC_DEFAULT_OPUS_MODEL: 'gpt-6.1-sol[1M]',
        ANTHROPIC_DEFAULT_FABLE_MODEL: 'gpt-6-astra[1M]',
        ANTHROPIC_DEFAULT_SONNET_MODEL: 'gpt-6.1-terra',
        ANTHROPIC_DEFAULT_HAIKU_MODEL: 'gpt-6-luna',
        CLAUDE_CODE_EFFORT_LEVEL: 'high'
      })
      expect(blocks[0]).toContain('gpt-6.1-sol[1M]')
      expect(blocks[0]).toContain('gpt-6-astra[1M]')
      expect(blocks[0]).not.toContain('gpt-6.1-terra[1M]')
      expect(blocks[0]).not.toContain('gpt-6-luna[1M]')
      expect(blocks[0]).toContain('CLAUDE_CODE_EFFORT_LEVEL')
    }
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    await importButton(wrapper).trigger('click')
    const params = new URL(String(open.mock.calls[0][0])).searchParams
    const env = JSON.parse(code(wrapper).find(value => value.startsWith('{'))!).env
    expect(params.get('model')).toBe(env.ANTHROPIC_MODEL)
    expect(params.get('opusModel')).toBe(env.ANTHROPIC_DEFAULT_OPUS_MODEL)
    expect(params.get('sonnetModel')).toBe(env.ANTHROPIC_DEFAULT_SONNET_MODEL)
    expect(params.get('haikuModel')).toBe(env.ANTHROPIC_DEFAULT_HAIKU_MODEL)

    await select(wrapper).setValue('gpt-6-luna')
    const changed = JSON.parse(code(wrapper).find(value => value.startsWith('{'))!).env
    expect(changed.ANTHROPIC_MODEL).toBe('gpt-6-luna')
    expect(changed).not.toHaveProperty('CLAUDE_CODE_EFFORT_LEVEL')
  })

  it.each([{ allow_long_context: false }, { context_token_limit: 999999 }])('respects the Key long-context cap for Claude manual mappings: %j', async limits => {
    const wrapper = render('openai', { keyRecord: { ...record, ...limits } })
    await flushPromises()
    await chooseApp(wrapper, 'claude')
    expect(code(wrapper).join('\n')).not.toContain('[1M]')
    expect(JSON.parse(code(wrapper).find(value => value.startsWith('{'))!).env.CLAUDE_CODE_EFFORT_LEVEL).toBe('high')
  })

  it('omits unsupported native Claude effort while keeping every model unset', async () => {
    fetchMock.mockResolvedValue(response({ models: [{ slug: 'claude-opus-5-5', context_window: 1000000, supported_reasoning_levels: [{ effort: 'none' }] }] }))
    const wrapper = render('anthropic')
    await flushPromises()
    const env = JSON.parse(code(wrapper).find(value => value.startsWith('{'))!).env
    expect(env).not.toHaveProperty('ANTHROPIC_MODEL')
    expect(env).not.toHaveProperty('CLAUDE_CODE_EFFORT_LEVEL')
    expect(code(wrapper).join('\n')).not.toContain('[1M]')
  })

  it('keeps the Desktop selected target distinct from the Fable wire carrier and explains client settings', async () => {
    const wrapper = render()
    await flushPromises()
    await chooseApp(wrapper, 'claude-desktop')
    expect((select(wrapper).element as HTMLSelectElement).value).toBe('gpt-6.1-sol')
    expect(wrapper.get('[data-testid="model-role-table"]').text()).toContain('gpt-6-astra')
    expect(wrapper.get('[data-testid="desktop-model-limits"]').text()).toContain('CC Switch provider')
    expect(wrapper.get('[data-testid="desktop-model-limits"]').text()).toContain('default model in Claude Desktop')
    expect(wrapper.get('[data-testid="model-role-table"]').text()).not.toContain('Subagent')
  })

  it.each(['legacy', 'api-key'])('uses custom/OpenAI, Fast off, verified high and 1M in all Codex transports/OS (%s auth)', async mode => {
    const wrapper = render()
    await flushPromises()
    await wrapper.get(`[data-testid="codex-auth-mode-${mode}"]`).trigger('click')
    for (const transport of ['HTTPS', 'keys.useKeyModal.cliTabs.codexCliWs']) {
      await wrapper.findAll('button').find(button => button.text().trim() === transport)!.trigger('click')
      for (const os of ['macOS / Linux', 'Windows']) {
        await wrapper.findAll('button').find(button => button.text().trim() === os)!.trigger('click')
        const config = toml(wrapper)
        expect(config).toContain('[model_providers.custom]\nname = "OpenAI"')
        expect(config).not.toContain('model_providers.ritel')
        expect(config).toContain('service_tier = "default"')
        expect(config).not.toContain('service_tier = "fast"')
        expect(config).toContain('model_reasoning_effort = "high"')
        expect(config).toContain('model_context_window = 1050000')
        expect(config).toContain('model_catalog_url = "https://example.com/v1/models"')
        expect(config).toContain('api_key_model_discovery = true')
        expect(config).toContain(`supports_websockets = ${transport !== 'HTTPS'}`)
        expect(config).toContain(`requires_openai_auth = ${mode === 'legacy'}`)
        if (mode === 'api-key') {
          expect(config).toContain('env_key = "RITEL_API_KEY"')
          expect(config).toContain('x-openai-actor-authorization')
          expect(config).not.toContain(record.key)
        } else expect(code(wrapper)).toContain(JSON.stringify({ OPENAI_API_KEY: record.key }, null, 2))
      }
    }
    expect(wrapper.get('[data-testid="codex-fast-notice"]').text()).toContain('cannot clear')
  })

  it.each(['anthropic', 'gemini', 'antigravity', 'grok', 'deepseek', 'minimax', 'composite'])('retains env auth and custom/OpenAI for %s Codex manual configuration', async platform => {
    const wrapper = render(platform)
    await flushPromises()
    await chooseApp(wrapper, 'codex')
    expect(toml(wrapper)).toContain('[model_providers.custom]\nname = "OpenAI"')
    expect(toml(wrapper)).toContain('env_key = "RITEL_API_KEY"')
    expect(toml(wrapper)).toContain('requires_openai_auth = false')
    expect(toml(wrapper)).toContain('service_tier = "default"')
  })

  it('honors key context and group reasoning ceilings and does not infer unknown capabilities', async () => {
    const wrapper = render('openai', { keyRecord: { ...record, context_token_limit: 128000, group: { ...record.group, max_reasoning_effort: 'medium' } } })
    await flushPromises()
    expect(toml(wrapper)).toContain('model_context_window = 128000')
    expect(toml(wrapper)).toContain('model_reasoning_effort = "medium"')
    fetchMock.mockResolvedValue(response({ models: [{ slug: 'gpt-6.1-sol', context_window: 272000, supported_reasoning_levels: [{ effort: 'none' }], input_modalities: ['text'] }] }))
    await wrapper.setProps({ apiKey: 'sk-limited', keyRecord: { ...record, id: 12 } })
    await flushPromises()
    expect(toml(wrapper)).toContain('model_context_window = 272000')
    expect(toml(wrapper)).not.toContain('model_reasoning_effort')
    await chooseApp(wrapper, 'opencode')
    expect(JSON.parse(code(wrapper)[0]).provider.ritel.models['gpt-6.1-sol'].modalities.input).toEqual(['text'])
  })

  it('uses only authorized approved OpenCode models and Chat /v1 for Gemini', async () => {
    fetchMock.mockResolvedValue(response(catalog(['gemini-4-argon', 'gemini-4-carbon', 'gemini-2.5-pro'])))
    const wrapper = render('gemini')
    await flushPromises()
    await chooseApp(wrapper, 'opencode')
    const config = JSON.parse(code(wrapper)[0])
    expect(config.provider.ritel.npm).toBe('@ai-sdk/openai-compatible')
    expect(config.provider.ritel.options.baseURL).toBe('https://example.com/v1')
    expect(config.provider.ritel.options.apiKey).toBe('{env:RITEL_API_KEY}')
    expect(Object.keys(config.provider.ritel.models)).toEqual(['gemini-4-argon', 'gemini-4-carbon'])
    expect(code(wrapper).join('\n')).not.toContain('/v1beta')
    expect(wrapper.get('[data-testid="connection-url"]').text()).toBe('https://example.com/v1')
  })

  it('filters Grok manual entries to actual approved 4.x models and keeps the selected default', async () => {
    fetchMock.mockResolvedValue(response(catalog(['grok-4.7', 'grok-4.3', 'grok-build-0.1'])))
    const wrapper = render('grok')
    await flushPromises()
    await select(wrapper).setValue('grok-4.3')
    const config = code(wrapper).join('\n')
    expect(config).toContain('[model."grok-4.7"]')
    expect(config).toContain('[model."grok-4.3"]')
    expect(config).toContain('default = "grok-4.3"')
    expect(config).not.toContain('grok-build-0.1')
    expect(config).not.toContain('image_gen = true')
    expect(config).toContain('env_key = "XAI_API_KEY"')
    expect(config).toContain('api_backend = "chat_completions"')
    expect(config).toContain('[models]\ndefault = "grok-4.3"\ndefault_reasoning_effort = "high"')
    expect(config).not.toMatch(/^reasoning_effort =/m)
    expect(config).not.toContain('api_backend = "responses"')
  })

  it('switches the Grok manual backend with the selected profile protocol', async () => {
    fetchMock.mockResolvedValue(response(catalog(['grok-4.7', 'gpt-6.1-sol'])))
    const wrapper = render('grok')
    await flushPromises()
    expect(code(wrapper).join('\n')).toContain('api_backend = "chat_completions"')
    await select(wrapper).setValue('gpt-6.1-sol')
    expect(code(wrapper).join('\n')).toContain('api_backend = "responses"')
    expect(code(wrapper).join('\n')).not.toContain('api_backend = "chat_completions"')
  })

  it('supports local catalog downloads without changing the selected model or leaking a key into URLs', async () => {
    const wrapper = render()
    await flushPromises()
    await select(wrapper).setValue('gpt-6-astra')
    await wrapper.get('[data-testid="codex-model-catalog-mode"]').setValue('file')
    expect(toml(wrapper)).toContain('model_catalog_json = "~/.codex/codex-models.json"')
    expect(toml(wrapper)).not.toContain('model_catalog_url')
    expect(toml(wrapper)).toContain('model = "gpt-6-astra"')
    await wrapper.findAll('button').find(button => button.text().includes('keys.useKeyModal.codexModelCatalog.download'))!.trigger('click')
    expect(saveAsMock).toHaveBeenCalledWith(expect.any(Blob), 'codex-models.json')
    expect(wrapper.find(`a[href*="${record.key}"]`).exists()).toBe(false)
  })

  it.each([1024 * 1024, 1024 * 1024 + 1])('forces local catalog only above the byte limit (%i)', async bytes => {
    const manifest = { models: [{ slug: 'gpt-6.1-sol', description: '' }] }
    manifest.models[0].description = 'x'.repeat(bytes - JSON.stringify(manifest).length)
    fetchMock.mockResolvedValue(response(manifest))
    const wrapper = render()
    await flushPromises()
    const mode = wrapper.get('[data-testid="codex-model-catalog-mode"]')
    expect((mode.element as HTMLSelectElement).value).toBe(bytes > 1024 * 1024 ? 'file' : 'remote')
    expect((mode.get('option[value="remote"]').element as HTMLOptionElement).disabled).toBe(bytes > 1024 * 1024)
  })
})

describe('UseKeyModal connection interactions', () => {
  it('keeps explicit import, clipboard, keyboard tabs and advanced collapsing', async () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const wrapper = render()
    await flushPromises()
    await wrapper.get('[data-testid="copy-connection-key"]').trigger('click')
    expect(copyToClipboardMock).toHaveBeenLastCalledWith(record.key)
    await wrapper.get('[data-testid="copy-connection-url"]').trigger('click')
    expect(copyToClipboardMock).toHaveBeenLastCalledWith('https://example.com/v1')
    const advanced = wrapper.get('[data-testid="advanced-configuration"]')
    ;(advanced.element as HTMLDetailsElement).open = true
    await advanced.trigger('toggle')
    await wrapper.get('[data-app-tab="codex"]').trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.get('[data-app-tab="claude"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-testid="advanced-configuration"]').attributes('open')).toBeUndefined()
    expect(open).not.toHaveBeenCalled()
    await importButton(wrapper).trigger('click')
    const params = new URL(String(open.mock.calls[0][0])).searchParams
    expect(params.get('apiKey')).toBe(record.key)
    expect(params.get('app')).toBe('claude')
    expect(params.get('apiFormat')).toBe('openai_responses')
    expect(open.mock.calls[0][1]).toBe('_self')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it.each(['openai', 'anthropic', 'gemini', 'grok', 'deepseek'])('offers all 14 apps and leaves incompatible protocol disabled for %s', async platform => {
    const wrapper = render(platform)
    await flushPromises()
    expect(wrapper.findAll('[data-app-tab]')).toHaveLength(14)
    await chooseApp(wrapper, 'gemini')
    expect(importButton(wrapper).attributes('disabled') !== undefined).toBe(platform !== 'gemini')
    if (platform !== 'gemini') expect(wrapper.get('[data-testid="model-selection-status"]').text()).toContain('Gemini-compatible')
  })

  it('explains an Antigravity OpenCode protocol mismatch without the Gemini CLI-only guidance', async () => {
    fetchMock.mockResolvedValue(response(catalog(['gemini-4-argon'])))
    const wrapper = render('antigravity')
    await flushPromises()
    await chooseApp(wrapper, 'opencode')
    const message = 'This group does not support the protocol required by this app.'
    expect(wrapper.get('[data-testid="model-selection-status"]').text()).toBe(message)
    expect(importButton(wrapper).attributes('disabled')).toBeDefined()
    expect(importButton(wrapper).attributes('title')).toBe(message)
    expect(wrapper.text()).not.toContain('Gemini-compatible key and API')
    expect(wrapper.find('[data-testid="copy-full-provider-config"]').exists()).toBe(false)
    expect(code(wrapper)).toEqual([])
  })

  it.each(['opencode', 'openclaw', 'hermes', 'pi', 'mcode', 'kimi', 'dsh', 'omp'])('copies complete provider configuration separately from the %s single-model import', async app => {
    fetchMock.mockResolvedValue(response(catalog(['gpt-6.1-sol', 'gpt-6-astra'])))
    const wrapper = render()
    await flushPromises()
    await chooseApp(wrapper, app)
    await wrapper.get('[data-testid="copy-full-provider-config"]').trigger('click')
    const copied = copyToClipboardMock.mock.calls.at(-1)![0] as string
    expect(copied).toContain('gpt-6.1-sol')
    expect(copied).toContain('gpt-6-astra')
    expect(wrapper.get('[data-testid="full-provider-config"]').text()).toContain('provider’s editor in CC Switch')
    expect(wrapper.get('[data-testid="application-model-selection"]').text()).toContain('does not mean CC Switch will import a complete')
  })

  it('retains manual setup when imports are hidden or profiles empty', async () => {
    const wrapper = render('openai', { publicSettings: { hide_ccs_import_button: true } as PublicSettings })
    await flushPromises()
    expect(wrapper.find('[data-testid^="connection-ccs-"]').exists()).toBe(false)
    expect(toml(wrapper)).toContain('model_provider = "custom"')
    await wrapper.setProps({ publicSettings: { ccs_import_config: { profiles: [] } } as unknown as PublicSettings })
    expect(wrapper.find('[data-testid^="connection-ccs-"]').exists()).toBe(false)
    expect(toml(wrapper)).toContain('model_provider = "custom"')
  })

  it('keeps a recoverable import error and clears it when switching keys', async () => {
    vi.spyOn(window, 'open').mockImplementation(() => { throw new Error('No handler') })
    const wrapper = render()
    await flushPromises()
    await importButton(wrapper).trigger('click')
    expect(wrapper.text()).toContain('keys.ccSwitchNotInstalled')
    await wrapper.setProps({ keyRecord: { ...record, id: 9 } })
    await flushPromises()
    expect(wrapper.text()).not.toContain('keys.ccSwitchNotInstalled')
  })

  it('retains SystemOne without fetching models or offering CCS apps', () => {
    const wrapper = render('typesafe')
    expect(fetchMock).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid^="connection-ccs-"]').exists()).toBe(false)
    expect(code(wrapper)[0]).toContain('/v1/systemone')
  })
})


describe('UseKeyModal Magpie import', () => {
  it('shares model discovery, waits for readiness, and opens only the fragment bridge on click', async () => {
    const pending = deferred<ReturnType<typeof response>>()
    fetchMock.mockReturnValueOnce(pending.promise)
    const open = vi.spyOn(window, 'open').mockReturnValue(null)
    const wrapper = render()
    expect(wrapper.get('[data-testid="connection-magpie"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-testid="connection-magpie"]').trigger('click')
    expect(open).not.toHaveBeenCalled()
    pending.resolve(response(catalog(['gpt-6.1-sol', 'gpt-6-astra'])))
    await flushPromises()
    await select(wrapper).setValue('gpt-6-astra')
    await wrapper.get('[data-testid="connection-magpie"]').trigger('click')
    expect(fetchMock).toHaveBeenCalledTimes(1)
    expect(open).toHaveBeenCalledTimes(1)
    const [target, disposition, features] = open.mock.calls[0]!
    const url = new URL(String(target)), q = new URLSearchParams(url.hash.slice(1))
    expect(url.origin + url.pathname).toBe('https://usemagpie.ai/import')
    expect(url.search).toBe('')
    expect(q.get('key')).toBe(record.key)
    expect(q.get('models')).toBe('gpt-6-astra,gpt-6.1-sol')
    expect(disposition).toBe('_blank')
    expect(features).toBe('noopener,noreferrer')
  })
  it('remains available independently of the CC Switch visibility setting', async () => {
    const wrapper = render('openai', { publicSettings: { hide_ccs_import_button: true } })
    await flushPromises()
    expect(wrapper.find('[data-testid^="connection-ccs-"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="connection-magpie"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-testid="magpie-import-guide"]').text()).toContain('Magpie')
  })
  it('disables import after discovery failure and while a replacement key is loading', async () => {
    const wrapper = render()
    await flushPromises()
    fetchMock.mockRejectedValueOnce(new Error('offline'))
    await wrapper.setProps({ apiKey: 'sk-other', keyRecord: { ...record, id: 99 } })
    expect(wrapper.get('[data-testid="connection-magpie"]').attributes('disabled')).toBeDefined()
    await flushPromises()
    expect(wrapper.get('[data-testid="connection-magpie"]').attributes('disabled')).toBeDefined()
  })
  it('shows a safe error without putting credential-bearing URLs into the error message', async () => {
    const wrapper = render('openai', { baseUrl: 'http://public.example' })
    await flushPromises()
    await wrapper.get('[data-testid="connection-magpie"]').trigger('click')
    expect(wrapper.get('[data-testid="magpie-import-error"]').text()).not.toContain(record.key)
  })
  it('omits Magpie for SystemOne and previews without a saved key identity', async () => {
    expect(render('typesafe').find('[data-testid="connection-magpie"]').exists()).toBe(false)
    const wrapper = render('openai', { keyRecord: null })
    await flushPromises()
    expect(wrapper.find('[data-testid="connection-magpie"]').exists()).toBe(false)
  })
})
