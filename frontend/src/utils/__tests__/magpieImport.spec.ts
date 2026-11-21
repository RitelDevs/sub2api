import { describe, expect, it } from 'vitest'
import type { Group, GroupPlatform } from '@/types'
import { buildMagpieImportLink } from '../magpieImport'

const endpointNames = ['chat', 'responses', 'anthropic', 'gemini']
const dashboardOrigin = 'https://dashboard.example'

function keyFor(platform: GroupPlatform, allowMessages = false) {
  return {
    id: 21,
    name: '开发密钥',
    key: 'sk-test-a+b&c=d%#',
    group_id: 9,
    group_ids: [9],
    group: { id: 9, platform, allow_messages_dispatch: allowMessages } as Group
  }
}

function parse(input: Parameters<typeof buildMagpieImportLink>[0]) {
  const result = buildMagpieImportLink(input)
  if (!('url' in result)) throw new Error(`No import link: ${result.error}`)
  const url = new URL(result.url)
  expect(url.origin + url.pathname).toBe('https://usemagpie.ai/import')
  expect(url.search).toBe('')
  return new URLSearchParams(url.hash.slice(1))
}

const root = 'https://api.example/relay'
const openai = { chat: `${root}/v1`, responses: `${root}/v1` }
const compatible = { ...openai, anthropic: root }
const cases: Array<[GroupPlatform, boolean, Record<string, string>]> = [
  ['anthropic', false, { anthropic: root }],
  ['openai', false, openai],
  ['openai', true, compatible],
  ['gemini', false, { gemini: `${root}/v1beta` }],
  ['antigravity', false, { anthropic: `${root}/antigravity`, gemini: `${root}/antigravity/v1beta` }],
  ['grok', false, compatible],
  ['deepseek', false, compatible],
  ['opencode_go', false, compatible],
  ['command_code', false, compatible]
]

describe('Magpie import link', () => {
  it.each(cases)('sets endpoints for %s (Messages dispatch: %s)', (platform, allowMessages, expected) => {
    const row = keyFor(platform, allowMessages)
    const query = parse({ dashboardOrigin, baseUrl: `${root}/v1/`, siteName: '中转站', row })
    expect(Object.fromEntries([...query].filter(([k]) => endpointNames.includes(k)))).toEqual(expected)
    expect(query.get('name')).toBe(platform === 'openai' ? '中转站' : '开发密钥 · 中转站')
    expect(query.get('catalog')).toBe(platform === 'openai' ? 'openai' : null)
    expect(query.has('models')).toBe(false)
    expect(query.get('key')).toBe(row.key)
    expect(query.get('id')).toMatch(/^sub2api-[a-f0-9]{16}-21$/)
  })

  it('normalizes a dedicated Antigravity URL without adding its path twice', () => {
    const query = parse({ dashboardOrigin, baseUrl: `${root}/antigravity/v1beta/`, row: keyFor('antigravity') })
    expect(query.get('anthropic')).toBe(`${root}/antigravity`)
    expect(query.get('gemini')).toBe(`${root}/antigravity/v1beta`)
  })

  it('keeps provider IDs distinct by instance and row and stable across rename or rotation', () => {
    const row = keyFor('openai')
    const input = { dashboardOrigin, baseUrl: root, row }
    const id = parse(input).get('id')
    expect(parse({ ...input, row: { ...row, name: '新名字', key: 'sk-rotated' } }).get('id')).toBe(id)
    expect(parse({ ...input, row: { ...row, id: 22 } }).get('id')).not.toBe(id)
    expect(parse({ ...input, baseUrl: 'https://api.example/other' }).get('id')).not.toBe(id)
    expect(parse({ ...input, dashboardOrigin: 'https://other.example' }).get('id')).not.toBe(id)
  })

  it('uses the dashboard origin when there is no configured API address', () => {
    const query = parse({ dashboardOrigin, baseUrl: ' ', row: keyFor('openai') })
    expect(query.get('responses')).toBe(`${dashboardOrigin}/v1`)
  })

  it.each(['http://localhost:8484', 'http://127.0.0.1', 'http://192.168.1.50', 'http://relay.local', 'http://[::1]:8484', 'http://[fd12:3456::1]'])('allows local HTTP at %s', baseUrl => {
    const query = parse({ dashboardOrigin, baseUrl, row: keyFor('anthropic') })
    expect(query.get('anthropic')).toBe(baseUrl)
  })

  it.each([
    'http://api.example', 'http://10.example', 'http://172.32.0.1', 'http://[fe80::1]',
    'https://user:pass@api.example', 'https://a\tpi.example', `${root}?q=a`, `${root}?`, `${root}#section`,
    '//api.example', 'https:///api.example', 'javascript:void(0)'
  ])('rejects an invalid base at %s', baseUrl => {
    expect(buildMagpieImportLink({ dashboardOrigin, baseUrl, row: keyFor('openai') })).toEqual({ error: 'invalidBase' })
  })

  it.each([
    keyFor('typesafe'), keyFor('composite'), keyFor('unknown'),
    { ...keyFor('openai'), group_id: null, group: undefined },
    { ...keyFor('openai'), group_ids: [9, 10] },
    { ...keyFor('anthropic'), group: { ...keyFor('anthropic').group, claude_code_only: true } }
  ])('does not guess endpoints for an unsupported group', row => {
    expect(buildMagpieImportLink({ dashboardOrigin, row })).toEqual({ error: 'unsupportedGroup' })
  })

  it.each(['', ' \t', 'sk-test\nother', '密'.repeat(1400)])('rejects an empty, malformed or oversized key', key => {
    expect(buildMagpieImportLink({ dashboardOrigin, row: { ...keyFor('openai'), key } })).toEqual({ error: 'invalidKey' })
  })
})

import { buildMagpieImportUrl } from '../magpieImport'
import type { CcSwitchImportContext } from '../ccsModelSelection'

const gpt = ['gpt-6.1-sol', 'gpt-6-astra', 'gpt-6.1-terra', 'gpt-6-luna']
const claude = ['claude-opus-5-5', 'claude-sonnet-5-5', 'claude-haiku-5-5', 'claude-fable-5-2']
const gemini = ['gemini-4-argon', 'gemini-4-carbon', 'gemini-4-hydrogen', 'gemini-3.8-flash']
const models = (ids: string[], selectedModel?: string): CcSwitchImportContext => ({
  models: ids.map(id => ({ id, reasoningEfforts: ['high'], inputModalities: ['text'] })), selectedModel
})
function input(platform: GroupPlatform = 'openai', ids = gpt) {
  return { baseUrl: 'https://api.example.test/v1/', apiKey: 'sk-fixture+汉字&?=#%', keyId: 17,
    keyName: '我的密钥 & test', platform, app: 'codex' as const, context: models(ids) }
}
const params = (url: string) => new URLSearchParams(new URL(url).hash.slice(1))

describe('Magpie native provider import', () => {
  it('keeps the key in the bridge fragment and round-trips all UTF-8 and reserved characters', () => {
    const value = input(), url = new URL(buildMagpieImportUrl(value)), q = params(url.href)
    expect(url.origin + url.pathname).toBe('https://usemagpie.ai/import')
    expect(url.search).toBe('')
    expect(q.get('key')).toBe(value.apiKey)
    expect(q.get('name')).toBe('Ritel.SI API · 我的密钥 & test')
    expect(q.get('id')).toMatch(/^sub2api-[a-f0-9]{16}-17$/)
    expect(q.get('responses')).toBe('https://api.example.test/v1')
    expect(q.get('models')).toBe(gpt.join(','))
    expect(q.get('balance')).toBe('https://api.example.test/v1/usage')
    expect(q.get('balance_path')).toBe('remaining')
    expect(q.has('config')).toBe(false)
    expect(q.has('preset')).toBe(false)
    expect(q.has('catalog')).toBe(false)
  })
  it.each([
    ['anthropic', claude, 'anthropic', 'https://api.example.test'],
    ['gemini', gemini, 'gemini', 'https://api.example.test/v1beta'],
    ['openai', gemini, 'chat', 'https://api.example.test/v1'],
    ['grok', ['grok-4.7', 'grok-4.6'], 'chat', 'https://api.example.test/v1'],
    ['deepseek', ['deepseek-v4.1-flash', 'minimax-m3'], 'chat', 'https://api.example.test/v1'],
    ['antigravity', claude, 'anthropic', 'https://api.example.test/antigravity'],
    ['antigravity', gemini, 'gemini', 'https://api.example.test/antigravity/v1beta'],
    ['composite', claude, 'anthropic', 'https://api.example.test']
  ] as const)('uses the supported protocol for %s and %s', (platform, ids, protocol, endpoint) => {
    const q = params(buildMagpieImportUrl(input(platform, [...ids])))
    expect(q.get(protocol)).toBe(endpoint)
    expect(['chat', 'responses', 'anthropic', 'gemini'].filter(key => q.has(key))).toEqual([protocol])
  })
  it('does not confuse the OpenCode Gemini exception with Magpie native Gemini support', () => {
    const q = params(buildMagpieImportUrl({ ...input('gemini', gemini), app: 'opencode' }))
    expect(q.get('gemini')).toBe('https://api.example.test/v1beta')
    expect(q.has('chat')).toBe(false)
  })
  it('orders the chosen model first, imports only permitted choices, and never adds role suffixes', () => {
    const q = params(buildMagpieImportUrl({ ...input(), context: models(['gpt-6.1-sol', 'gpt-6-astra', 'unconfirmed-model'], 'gpt-6-astra') }))
    expect(q.get('models')).toBe('gpt-6-astra,gpt-6.1-sol')
    expect(q.has('model')).toBe(false)
    expect(q.has('service_tier')).toBe(false)
    expect(q.has('model_reasoning_effort')).toBe(false)
  })
  it('retains the domestic application default without granting absent models', () => {
    const q = params(buildMagpieImportUrl({ ...input('deepseek', ['deepseek-v4.1-flash', 'minimax-m3']), app: 'mcode' }))
    expect(q.get('models')).toBe('minimax-m3,deepseek-v4.1-flash')
  })
  it('uses a stable provider ID per key across applications and a distinct ID for another key', () => {
    const first = params(buildMagpieImportUrl(input())).get('id')
    expect(params(buildMagpieImportUrl({ ...input(), app: 'claude' })).get('id')).toBe(first)
    expect(params(buildMagpieImportUrl({ ...input(), keyId: 18 })).get('id')).not.toBe(first)
  })
  it('does not cut a multibyte name inside a character', () => {
    const q = params(buildMagpieImportUrl({ ...input(), keyName: '密钥😀'.repeat(30) }))
    expect(new TextEncoder().encode(q.get('name')!).length).toBeLessThanOrEqual(80)
    expect(q.get('name')).not.toContain('�')
  })
  it.each(['http://api.example.test', 'https://name:password@api.example.test', 'https://api.example.test?key=bad', 'https://api.example.test#secret', 'https://api.example.test?', 'https://api.example.test#', 'not a URL'])('rejects invalid API base %s', baseUrl => {
    expect(() => buildMagpieImportUrl({ ...input(), baseUrl })).toThrow()
  })
  it('preserves deployment prefixes and avoids repeating endpoint versions', () => {
    const q = params(buildMagpieImportUrl({ ...input('antigravity', gemini), baseUrl: 'https://api.example.test/proxy/antigravity/v1beta/' }))
    expect(q.get('gemini')).toBe('https://api.example.test/proxy/antigravity/v1beta')
    expect(q.get('balance')).toBe('https://api.example.test/proxy/v1/usage')
  })
  it('refuses empty catalogs, non-agent groups and invalid key identities', () => {
    expect(() => buildMagpieImportUrl(input('typesafe'))).toThrow()
    expect(() => buildMagpieImportUrl(input('openai', []))).toThrow()
    expect(() => buildMagpieImportUrl({ ...input(), keyId: 0 })).toThrow()
    expect(() => buildMagpieImportUrl({ ...input(), apiKey: '' })).toThrow()
    expect(() => buildMagpieImportUrl({ ...input(), apiKey: 'sk-test\nheader' })).toThrow()
    expect(() => buildMagpieImportUrl({ ...input(), apiKey: 'k'.repeat(4097) })).toThrow()
  })
})
