import {
  findCodexCatalogModel,
  formatCodexReasoningEffortTomlLine,
  selectCodexConfigReasoningEffort
} from '@/utils/codexCatalogConfig'

export interface CodexImportConfigInput {
  apiKey: string
  /** Already resolved Responses base URL; this builder does not append /v1. */
  endpoint: string
  model: string
  /** Verified limit shared by this model, group and client. Omit when unknown. */
  contextWindow?: number
  /** Full Codex-native catalog URL, on the same origin as endpoint. */
  modelCatalogUrl?: string
  /**
   * A caller-verified effort; null explicitly omits the override.
   * With catalog content, prefer high when supported and otherwise use the
   * catalog's supported default. Without either source, do not guess support.
   */
  reasoningEffort?: string | null
  modelCatalogContent?: string | null
}

export interface CodexImportConfig {
  auth: { OPENAI_API_KEY: string }
  config: string
}

function escapeTomlBasicString(value: string): string {
  return value
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    // eslint-disable-next-line no-control-regex
    .replace(/[\x00-\x1f\x7f]/g, (character) =>
      `\\u${character.charCodeAt(0).toString(16).padStart(4, '0')}`)
}

function selectReasoningEffort(input: CodexImportConfigInput, model: string): string | null {
  if (input.reasoningEffort === null) return null

  const requested = input.reasoningEffort?.trim() || 'high'
  const descriptor = findCodexCatalogModel(input.modelCatalogContent, model)
  if (descriptor) {
    const supported = descriptor.supported_reasoning_levels ?? []
    if (supported.some((level) =>
      typeof level?.effort === 'string' && level.effort.trim() === requested)) {
      return requested === 'none' ? null : requested
    }
    return selectCodexConfigReasoningEffort(descriptor)
  }

  // An explicit effort is the caller's capability decision, not a model-name
  // heuristic. This also supports callers that have a descriptor already.
  const explicit = input.reasoningEffort?.trim()
  return explicit && explicit !== 'none' ? explicit : null
}

/**
 * Shared auth.json / config.toml output for manual setup and CCS JSON payloads.
 *
 * CCS 4.0.28 retains custom, provider.name, model_catalog_url and the window
 * in the imported provider row; routing/aggregation can re-project them.
 * Its codex_link whitelist DROPS service_tier and [features]. Its live
 * projection also leaves the user's existing service tier alone. Consequently
 * this output expresses Fast off for direct setup; a deeplink alone cannot
 * turn off a pre-existing common/live priority setting on that receiver.
 */
export function buildCodexImportConfig(input: CodexImportConfigInput): CodexImportConfig {
  const endpoint = input.endpoint.trim().replace(/\/+$/, '')
  const model = input.model.trim()
  if (!model) throw new Error('A Codex model is required')

  const endpointUrl = new URL(endpoint)
  if (endpointUrl.protocol !== 'https:' && endpointUrl.protocol !== 'http:') {
    throw new Error('The Codex endpoint must use HTTP or HTTPS')
  }

  const catalogUrl = input.modelCatalogUrl?.trim()
  if (catalogUrl && new URL(catalogUrl).origin !== endpointUrl.origin) {
    throw new Error('The Codex model catalog must use the same origin as the endpoint')
  }
  if (input.contextWindow !== undefined &&
    (!Number.isSafeInteger(input.contextWindow) || input.contextWindow <= 0)) {
    throw new Error('The Codex context window must be a positive safe integer')
  }

  const effort = selectReasoningEffort(input, model)
  const lines = [
    'model_provider = "custom"',
    `model = "${escapeTomlBasicString(model)}"`,
    // Codex 0.154 accepts "default" as explicit standard routing. Unlike
    // omission, it suppresses catalog tier defaults while leaving Fast usable.
    'service_tier = "default"'
  ]
  if (effort) {
    lines.push(formatCodexReasoningEffortTomlLine(escapeTomlBasicString(effort)).trimEnd())
  }
  if (input.contextWindow !== undefined) {
    lines.push(`model_context_window = ${input.contextWindow}`)
  }
  lines.push(
    '',
    '[model_providers.custom]',
    'name = "OpenAI"',
    `base_url = "${escapeTomlBasicString(endpoint)}"`,
    'wire_api = "responses"',
    'requires_openai_auth = true'
  )
  if (catalogUrl) {
    lines.push(
      `model_catalog_url = "${escapeTomlBasicString(catalogUrl)}"`,
      '',
      // Remote discovery requires a Codex version supporting this feature.
      // CCS 4.0.28 preserves the URL but does not import this feature flag.
      '[features]',
      'api_key_model_discovery = true'
    )
  }

  return {
    auth: { OPENAI_API_KEY: input.apiKey },
    config: lines.join('\n') + '\n'
  }
}
