// @vitest-environment node
import { describe, expect, it, vi } from 'vitest'

vi.mock('@/api/admin/accounts', () => ({
  getAntigravityDefaultModelMapping: vi.fn()
}))

import { buildModelMappingObject, getModelsByPlatform, getPresetMappingsByPlatform, reconcileUpstreamModelMapping, splitModelMappingObject } from '../useModelWhitelist'
import { BUILTIN_PLATFORM_CATALOG, resetPlatformCatalog, setPlatformCatalog } from '@/constants/platformCatalog'

describe('useModelWhitelist', () => {
  it('offers exact GPT Image 2.6 names without inventing a bare alias', () => {
    const models = getModelsByPlatform('openai')
    expect(models).toContain('gpt-image-2.6-flare')
    expect(models).toContain('gpt-image-2.6-sunburst')
    expect(models).not.toContain('gpt-image-2.6')
  })

  it('平台清单中没有内置模型列表的多协议供应商不预填白名单', () => {
    setPlatformCatalog({
      ...BUILTIN_PLATFORM_CATALOG,
      platforms: [
        ...BUILTIN_PLATFORM_CATALOG.platforms,
        {
          id: 'acme_router',
          display_name: 'Acme Router',
          gateway: 'openai',
          cn_provider: false,
          multi_protocol: {
            default_mode: 'standard',
            routing: 'by_model',
            modes: [{ mode: 'standard', base_urls: { chat_completions: 'https://api.acme-router.example/v1' } }]
          }
        }
      ]
    })
    try {
      expect(getModelsByPlatform('acme_router')).toEqual([])
      expect(getModelsByPlatform('kimi').length).toBeGreaterThan(0)
      // 非多协议供应商的未知平台维持原有回退。
      expect(getModelsByPlatform('bedrock')).toEqual(getModelsByPlatform('anthropic'))
    } finally {
      resetPlatformCatalog()
    }
  })

  it('openai 模型列表移除旧型号并包含 GPT Image 2.6', () => {
    const models = getModelsByPlatform('openai')

    expect(models).not.toContain('gpt-5.4')
    expect(models).not.toContain('gpt-5.4-mini')
    expect(models).not.toContain('gpt-5.4-2026-03-05')
    expect(models).not.toContain('gpt-5.5')
    expect(models).not.toContain('gpt-5.2')
    expect(models).not.toContain('gpt-5.3-codex-spark')
    expect(models).toContain('gpt-image-1.5')
    expect(models).toContain('gpt-image-2.6-flare')
    expect(models).toContain('gpt-image-2.6-sunburst')
    expect(models).toContain('codex-auto-review')
    expect(models).toContain('gpt-5.6')
    expect(models).toContain('gpt-6')
    expect(models).toContain('gpt-6-astra')
    expect(models).toContain('gpt-6.2-astra')
    expect(models).toContain('gpt-6.2-astra-pro')
    expect(models).toContain('gpt-6.2-sol')
    expect(models).toContain('gpt-6.1-sol')
    expect(models).toContain('gpt-6.1-terra')
    expect(models).toContain('gpt-6-sol')
    expect(models).toContain('gpt-6-luna')
  })

  it('openai 预设映射包含 GPT-6 别名和 Astra', () => {
    expect(getPresetMappingsByPlatform('openai')).toEqual(expect.arrayContaining([
      ...['gpt-6.2-astra', 'gpt-6.2-astra-pro', 'gpt-6.2-sol'].map(model => expect.objectContaining({ from: model, to: model })),
      expect.objectContaining({ label: 'GPT-6.2 Sol', from: 'gpt-6.2-sol', to: 'gpt-6.2-sol' }),
      expect.objectContaining({ label: 'GPT-6.2 Astra', from: 'gpt-6.2-astra', to: 'gpt-6.2-astra' }),
      expect.objectContaining({ label: 'GPT-6.1 Sol', from: 'gpt-6.1-sol', to: 'gpt-6.1-sol' }),
      expect.objectContaining({ label: 'GPT-6.1 Terra', from: 'gpt-6.1-terra', to: 'gpt-6.1-terra' }),
      expect.objectContaining({ label: 'GPT-6', from: 'gpt-6', to: 'gpt-6' }),
      expect.objectContaining({ label: 'GPT-6 Astra', from: 'gpt-6-astra', to: 'gpt-6-astra' })
    ]))
  })

  it('Gemini 4 Carbon exposes the official API model without invented aliases', () => {
    const models = getModelsByPlatform('gemini')
    expect(models).toContain('gemini-4-carbon')
    expect(models).not.toContain('gemini-4-carbon-preview')
    expect(getModelsByPlatform('antigravity')).toContain('gemini-4-carbon')
    expect(getPresetMappingsByPlatform('antigravity')).toEqual(expect.arrayContaining([
      expect.objectContaining({ from: 'gemini-4-carbon', to: 'gemini-4-carbon' })
    ]))
    expect(buildModelMappingObject('whitelist', ['gemini-4-carbon'], [])).toEqual({
      'gemini-4-carbon': 'gemini-4-carbon'
    })
  })
  it('whitelist 模式会忽略通配符条目', () => {
    const mapping = buildModelMappingObject('whitelist', ['claude-*', 'gemini-3.1-flash-image'], [])
    expect(mapping).toEqual({
      'gemini-3.1-flash-image': 'gemini-3.1-flash-image'
    })
  })

  it('combined 模式会同时保留白名单身份映射和模型映射', () => {
    const mapping = buildModelMappingObject(
      'combined',
      ['gpt-5.4', 'claude-*'],
      [
        { from: 'gpt-latest', to: 'gpt-5.4' },
        { from: 'gpt-5.4', to: 'gpt-5.4-mini' }
      ]
    )

    expect(mapping).toEqual({
      'gpt-5.4': 'gpt-5.4-mini',
      'gpt-latest': 'gpt-5.4'
    })
  })

  it('combined mode retains a mapping when a whitelist entry has the same source', () => {
    expect(buildModelMappingObject('combined', ['gpt-latest'], [{ from: 'gpt-latest', to: 'deepseek-chat' }])).toEqual({
      'gpt-latest': 'deepseek-chat'
    })
  })

  it('reconciles only concrete upstream models and preserves live alias targets', () => {
    const existingMappings = [
      { from: 'public-alias', to: 'target' },
      { from: 'retired-alias', to: 'retired-target' },
      { from: 'legacy-*', to: 'target' },
      { from: 'tar*get', to: 'target' },
      { from: 'wildcard-target', to: 'tar*' },
      { from: 'target', to: 'target' },
      { from: ' ', to: 'target' }
    ]
    const models = [' target ', 'target', '', ' ', '*', 'retired-*']
    const before = structuredClone({ models, existingMappings })

    const reconciled = reconcileUpstreamModelMapping(models, existingMappings)

    expect(reconciled.allowedModels).toEqual(['target'])
    expect(reconciled.modelMappings).toEqual([
      { from: 'public-alias', to: 'target' },
      { from: 'legacy-*', to: 'target' }
    ])
    expect({ models, existingMappings }).toEqual(before)
  })

  it('does not replace a live redirect with an upstream identity of the same name', () => {
    const reconciled = reconcileUpstreamModelMapping(
      ['public-model', 'target'],
      [{ from: 'public-model', to: 'target' }]
    )

    expect(reconciled).toEqual({
      allowedModels: ['target'],
      modelMappings: [{ from: 'public-model', to: 'target' }]
    })
    expect(buildModelMappingObject('combined', reconciled.allowedModels, reconciled.modelMappings)).toEqual({
      target: 'target',
      'public-model': 'target'
    })
  })

  it('drops alias chains when the immediate target is absent from the live catalog', () => {
    const reconciled = reconcileUpstreamModelMapping(
      ['target'],
      [{ from: 'alias', to: 'target' }, { from: 'chained-alias', to: 'alias' }]
    )

    expect(reconciled).toEqual({
      allowedModels: ['target'],
      modelMappings: [{ from: 'alias', to: 'target' }]
    })
  })

  it('split mapping keeps only identity entries in the whitelist after reopening', () => {
    expect(splitModelMappingObject({ 'gpt-latest': 'deepseek-chat', 'gpt-5.4': 'gpt-5.4' })).toEqual({
      allowedModels: ['gpt-5.4'],
      modelMappings: [{ from: 'gpt-latest', to: 'deepseek-chat' }]
    })
  })

  it('splitModelMappingObject 会把身份映射还原成白名单，其余保留为映射', () => {
    const parsed = splitModelMappingObject({
      'gpt-5.4': 'gpt-5.4',
      'gpt-latest': 'gpt-5.4',
      ' ': 'gpt-empty',
      broken: 123
    })

    expect(parsed).toEqual({
      allowedModels: ['gpt-5.4'],
      modelMappings: [{ from: 'gpt-latest', to: 'gpt-5.4' }]
    })
  })
})
