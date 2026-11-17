// @vitest-environment node
import { describe, expect, it } from 'vitest'
import { buildCodexImportConfig } from '@/utils/codexImportConfig'

const input = {
  apiKey: 'test-only-key',
  endpoint: 'https://api.example.test/v1/',
  model: 'gpt-6.1-sol'
}

function catalog(efforts: string[], defaultEffort = 'medium'): string {
  return JSON.stringify({
    models: [{
      slug: input.model,
      default_reasoning_level: defaultEffort,
      supported_reasoning_levels: efforts.map((effort) => ({ effort }))
    }]
  })
}

describe('buildCodexImportConfig', () => {
  it('keeps the custom history identity and OpenAI provider name separate from the key', () => {
    const result = buildCodexImportConfig(input)
    expect(result.auth).toEqual({ OPENAI_API_KEY: input.apiKey })
    expect(result.config).toContain('model_provider = "custom"\n')
    expect(result.config).toContain('[model_providers.custom]\nname = "OpenAI"')
    expect(result.config).toContain('base_url = "https://api.example.test/v1"')
    expect(result.config).toContain('wire_api = "responses"')
    expect(result.config).toContain('requires_openai_auth = true')
    expect(result.config).not.toContain(input.apiKey)
    expect(result.config).not.toContain('disable_response_storage')
  })

  it('explicitly selects standard routing without disabling the manual Fast switch', () => {
    const result = buildCodexImportConfig(input)
    expect(result.config).toContain('service_tier = "default"\n')
    expect(result.config).not.toContain('service_tier = "fast"')
    expect(result.config).not.toContain('service_tier = "priority"')
    expect(result.config).not.toContain('fast_mode')
  })

  it('prefers high over a lower catalog default when supported', () => {
    const result = buildCodexImportConfig({
      ...input,
      modelCatalogContent: catalog(['low', 'medium', 'high'])
    })
    expect(result.config).toContain('model_reasoning_effort = "high"')
  })

  it('uses the supported catalog default if high is unavailable', () => {
    const result = buildCodexImportConfig({
      ...input,
      reasoningEffort: 'high',
      modelCatalogContent: catalog(['low', 'medium'])
    })
    expect(result.config).toContain('model_reasoning_effort = "medium"')
  })

  it('accepts caller-verified effort when no catalog is supplied', () => {
    expect(buildCodexImportConfig({ ...input, reasoningEffort: 'high' }).config)
      .toContain('model_reasoning_effort = "high"')
  })

  it('keeps a supported explicit selection instead of overriding it with high', () => {
    expect(buildCodexImportConfig({
      ...input,
      reasoningEffort: 'low',
      modelCatalogContent: catalog(['low', 'medium', 'high'])
    }).config).toContain('model_reasoning_effort = "low"')
  })

  it.each([
    {},
    { modelCatalogContent: catalog(['none'], 'none') },
    { modelCatalogContent: '{"models":[]}' },
    { modelCatalogContent: catalog([]) },
    { reasoningEffort: null, modelCatalogContent: catalog(['high']) },
    { reasoningEffort: 'none' }
  ])('omits unsupported, unknown or explicitly disabled reasoning (%j)', (options) => {
    expect(buildCodexImportConfig({ ...input, ...options }).config)
      .not.toContain('model_reasoning_effort')
  })

  it.each([131072, 1000000, 1048576])('uses the verified window %i without inventing 1M', (contextWindow) => {
    const config = buildCodexImportConfig({ ...input, contextWindow }).config
    expect(config).toContain(`model_context_window = ${contextWindow}\n`)
    expect(config).not.toContain('model_auto_compact_token_limit')
  })

  it('leaves an unknown context window to the client/catalog', () => {
    expect(buildCodexImportConfig(input).config).not.toContain('model_context_window')
  })

  it.each([0, -1, 1.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])('rejects an invalid window %s', (contextWindow) => {
    expect(() => buildCodexImportConfig({ ...input, contextWindow })).toThrow('context window')
  })

  it('preserves the complete same-origin remote catalog URL and opts into discovery', () => {
    const modelCatalogUrl = 'https://api.example.test/v1/codex/models?client=codex'
    const config = buildCodexImportConfig({ ...input, modelCatalogUrl }).config
    expect(config).toContain(`model_catalog_url = "${modelCatalogUrl}"`)
    expect(config).toContain('[features]\napi_key_model_discovery = true')
  })

  it.each([
    'https://different.example.test/v1/codex/models',
    'http://api.example.test/v1/codex/models',
    'https://api.example.test:8443/v1/codex/models'
  ])('rejects a catalog origin different from the inference endpoint (%s)', (modelCatalogUrl) => {
    expect(() => buildCodexImportConfig({ ...input, modelCatalogUrl })).toThrow('same origin')
  })

  it('does not append /v1 or rewrite a caller-resolved compatible endpoint', () => {
    expect(buildCodexImportConfig({ ...input, endpoint: 'https://api.example.test/gateway/' }).config)
      .toContain('base_url = "https://api.example.test/gateway"')
  })

  it('escapes TOML control characters rather than allowing extra configuration lines', () => {
    const config = buildCodexImportConfig({
      ...input,
      model: 'model"\\name\nservice_tier = "priority"\u007f'
    }).config
    expect(config).toContain('model = "model\\"\\\\name\\u000aservice_tier = \\"priority\\"\\u007f"')
    expect(config.split('\n').filter((line) => line.startsWith('service_tier ='))).toEqual([
      'service_tier = "default"'
    ])
  })
})
