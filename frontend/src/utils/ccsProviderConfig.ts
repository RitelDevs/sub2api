import type { CcsImportProfile } from '@/types/ccswitch'
import type { CcSwitchImportContext } from './ccsModelSelection'
import { ccSwitchContextWindow, ccSwitchReasoningEffort } from './ccsModelSelection'
import { getCcSwitchImportParameterDefaults, type CcSwitchApplicationProfile } from './ccswitchImport'

export interface CcSwitchProviderConfigInput {
  profile: CcSwitchApplicationProfile
  baseUrl: string
  apiKey: string
  context?: CcSwitchImportContext
}

export interface CcSwitchProviderConfig {
  content: string
  language: 'json' | 'toml' | 'yaml'
  requiresProviderId: boolean
  formatLabel: string
}

const PROVIDER_ID = 'REPLACE_WITH_PROVIDER_ID'
const ADDITIVE_APPS = ['opencode', 'openclaw', 'hermes', 'pi', 'mcode', 'kimi', 'dsh', 'omp']
const PROTOCOLS: Record<string, string> = {
  anthropic: 'anthropic-messages',
  openai_chat: 'openai-completions',
  openai_responses: 'openai-responses',
  gemini_native: 'google-generative-ai'
}
const OPENCODE_PACKAGES: Record<string, string> = {
  anthropic: '@ai-sdk/anthropic', openai_chat: '@ai-sdk/openai-compatible', openai_responses: '@ai-sdk/openai'
}
const KIMI_TYPES: Record<string, string> = {
  anthropic: 'anthropic', openai_chat: 'openai', openai_responses: 'openai_responses', gemini_native: 'google-genai'
}
const HERMES_MODES: Record<string, string> = {
  anthropic: 'anthropic_messages', openai_chat: 'chat_completions', openai_responses: 'codex_responses'
}

function tokenCount(value: number | undefined): number | undefined {
  return value !== undefined && Number.isSafeInteger(value) && value > 0 ? value : undefined
}

function tomlString(value: string): string {
  return JSON.stringify(value).replace(/\u007f/g, '\\u007f')
}

/**
 * A single provider's editor payload, not a database backup or a deeplink config.
 * CCS 4.0.28/4.0.33 rebuild additive deeplinks as one model; these editor formats
 * preserve the full authorized list. Never infer authorization from metadata.
 */
export function buildCcSwitchProviderConfig({ profile, baseUrl, apiKey, context }: CcSwitchProviderConfigInput): CcSwitchProviderConfig | null {
  const { app, platform } = profile
  if (!ADDITIVE_APPS.includes(app)) return null
  if (platform === 'typesafe') throw new Error('This group does not provide agent models')

  const selected = typeof profile.parameters.model === 'string' ? profile.parameters.model : ''
  const ids = [...new Set(profile.modelOptions ?? (selected ? [selected] : []))]
  if (!ids.length) throw new Error('No authorized models are available for this provider')
  // Hermes uses the first model when explicitly selected as the current provider.
  if (ids.includes(selected)) ids.splice(0, ids.length, selected, ...ids.filter(id => id !== selected))

  const geminiInOpenCode = app === 'opencode' && (
    platform === 'gemini' || selected.startsWith('gemini-') || profile.parameters.apiFormat === 'gemini_native'
  )
  const parameters: CcSwitchApplicationProfile['parameters'] = { ...profile.parameters, ...(geminiInOpenCode ? { apiFormat: 'openai_chat' } : {}) }
  // The shared defaults resolver predates ApplicationProfile's open-ended
  // GroupPlatform type; its runtime logic already handles those same profiles.
  const defaults = getCcSwitchImportParameterDefaults({ ...profile, parameters } as CcsImportProfile, baseUrl)
  const format = String(parameters.apiFormat || defaults.apiFormat || '')
  const protocol = PROTOCOLS[format]
  if (!protocol || (format === 'gemini_native' && ['hermes', 'mcode', 'dsh'].includes(app))) {
    throw new Error(`Unsupported API format for ${app}: ${format}`)
  }
  const endpointTemplate = String(parameters.endpoint || defaults.endpoint)
  let endpoint = endpointTemplate.replace(/\{\{(baseUrl|apiKey)\}\}/g, (_, key: string) => key === 'baseUrl' ? baseUrl : apiKey)
    .split(',')[0]!.trim().replace(/\/+$/, '')
  if (geminiInOpenCode) endpoint = endpoint.replace(/\/v1(?:beta)?$/, '') + '/v1'

  const models = ids.map(id => {
    const info = context?.models.find(model => model.id === id)
    return {
      id,
      contextWindow: tokenCount(ccSwitchContextWindow(id, context)),
      maxOutputTokens: tokenCount(info?.maxOutputTokens),
      input: [...new Set(info?.inputModalities ?? [])],
      efforts: info?.reasoningEfforts ?? [],
      reasoning: (info?.reasoningEfforts ?? []).some(effort => !['none', 'off', 'disabled'].includes(effort))
    }
  })

  let language: CcSwitchProviderConfig['language'] = 'json'
  let formatLabel = `${profile.label} 供应商 JSON`
  let payload: unknown

  if (app === 'kimi') {
    const type = KIMI_TYPES[format]!
    const lines = [
      `# Replace ${PROVIDER_ID} with the ID shown in this provider's editor.`,
      `[providers.${tomlString(PROVIDER_ID)}]`,
      `type = ${tomlString(type)}`,
      `base_url = ${tomlString(endpoint)}`,
      `api_key = ${tomlString(apiKey)}`
    ]
    for (const model of models) {
      const capabilities = ['tool_use', ...(model.input.includes('image') ? ['image_in'] : []), ...(model.reasoning ? ['thinking'] : [])]
      lines.push('', `[models.${tomlString(`${PROVIDER_ID}/${model.id}`)}]`,
        `provider = ${tomlString(PROVIDER_ID)}`, `model = ${tomlString(model.id)}`)
      lines.push(model.contextWindow !== undefined
        ? `max_context_size = ${model.contextWindow}`
        : '# Required: fill max_context_size with this model/key\'s verified limit before saving.')
      lines.push(`capabilities = [${capabilities.map(tomlString).join(', ')}]`)
      if (format === 'anthropic' && model.maxOutputTokens !== undefined) lines.push(`max_output_size = ${model.maxOutputTokens}`)
    }
    return {
      content: lines.join('\n') + '\n', language: 'toml', requiresProviderId: true,
      formatLabel: 'Kimi Code 单供应商 TOML' + (models.some(model => !model.contextWindow) ? '（必填 max_context_size 缺失，核实补齐后再保存）' : '')
    }
  }

  if (app === 'opencode' || app === 'mcode' || app === 'dsh') {
    const entries = Object.fromEntries(models.map(model => {
      const limit = {
        ...(model.contextWindow !== undefined ? { context: model.contextWindow } : {}),
        ...(model.maxOutputTokens !== undefined ? { output: model.maxOutputTokens } : {})
      }
      // Verified OpenCode @ai-sdk/openai Responses option. Other dialects get
      // capability metadata only, not a made-up universal effort default.
      const effort = app === 'opencode' && format === 'openai_responses' && model.id.startsWith('gpt-')
        ? ccSwitchReasoningEffort(model.id, context) : undefined
      const input = model.input.filter(value => ['text', 'image', 'audio', 'video', 'pdf'].includes(value))
      return [model.id, {
        name: model.id,
        ...(Object.keys(limit).length ? { limit } : {}),
        ...(model.reasoning ? { reasoning: true } : {}),
        // DSH otherwise fills low/medium/high on its own, even if the catalog
        // only confirmed one level. This map declares support, not a default.
        ...(app === 'dsh' && model.reasoning ? { reasoningEfforts: Object.fromEntries(model.efforts.map(effort => [effort === 'none' ? 'off' : effort, effort])) } : {}),
        ...(input.length ? { modalities: { input } } : {}),
        ...(effort ? { options: { reasoningEffort: effort } } : {})
      }]
    }))
    const provider = {
      ...(app === 'opencode'
        ? { npm: OPENCODE_PACKAGES[format]! }
        : { kind: 'custom', enabled: true, api: protocol }),
      options: { baseURL: endpoint, apiKey }, models: entries
    }
    payload = app === 'mcode' ? { custom_provider: { [PROVIDER_ID]: provider } } : provider
    if (app === 'mcode') {
      language = 'yaml'
      formatLabel = 'MiniMax Code config.yaml 单供应商片段'
    }
  } else if (app === 'hermes') {
    payload = {
      base_url: endpoint, api_key: apiKey,
      api_mode: HERMES_MODES[format]!,
      // The CCS editor uses an array; its writer converts this to Hermes' map.
      models: models.map(model => ({
        id: model.id, name: model.id,
        ...(model.contextWindow !== undefined ? { context_length: model.contextWindow } : {}),
        ...(model.maxOutputTokens !== undefined ? { max_tokens: model.maxOutputTokens } : {}),
        ...(model.input.includes('image') ? { supports_vision: true } : {})
      }))
    }
  } else {
    const provider = {
      baseUrl: endpoint, apiKey, api: protocol,
      models: models.map(model => {
        const input = model.input.filter(value => value === 'text' || value === 'image')
        return {
          id: model.id, name: model.id,
          ...(model.contextWindow !== undefined ? { contextWindow: model.contextWindow } : {}),
          ...(model.maxOutputTokens !== undefined ? { maxTokens: model.maxOutputTokens } : {}),
          ...(model.reasoning ? { reasoning: true } : {}),
          ...(input.length ? { input } : {})
        }
      })
    }
    payload = app === 'omp' ? { providers: { [PROVIDER_ID]: provider } } : provider
    if (app === 'omp') {
      language = 'yaml'
      formatLabel = 'Oh My Pi models.yml 单供应商片段'
    }
    if (app === 'pi' || app === 'omp') {
      const missing = [
        ...(models.some(model => !model.contextWindow) ? ['contextWindow'] : []),
        ...(models.some(model => !model.maxOutputTokens) ? ['maxTokens'] : [])
      ]
      if (missing.length) formatLabel += `（必填 ${missing.join('/')} 缺失，核实补齐后再保存）`
    }
  }

  // CCS' yamlNodeFromText accepts JSON syntax. YAML additionally requires these
  // non-printable characters to be escaped; preserve their exact string value.
  const serialized = JSON.stringify(payload, null, 2)
  const content = language === 'yaml'
    ? serialized.replace(/[\u007f-\u009f\ufffe\uffff]/g, value => '\\u' + value.charCodeAt(0).toString(16).padStart(4, '0'))
    : serialized
  return { content: content + '\n', language, requiresProviderId: app === 'mcode' || app === 'omp', formatLabel }
}
