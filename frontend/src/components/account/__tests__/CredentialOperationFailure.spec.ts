import { describe, it, expect } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import View from '../CredentialOperationFailure.vue'
import zh from '@/i18n/locales/zh/tokenGuardV2'
import en from '@/i18n/locales/en/tokenGuardV2'
import hk from '@/i18n/locales/zh-Hant-HK/tokenGuardV2'
import { safeOperationFailure, safeDiagnosticToken } from '@/utils/credentialOperationFailure'

describe('credential operation failure display', () => {
  it.each(Object.entries({ en, zh, 'zh-Hant-HK': hk }))('renders the email prompt and manual action in %s', (locale, messages) => {
    const failure = safeOperationFailure({ code: 'email_otp_required', stage: 'email_verification' })
    expect(failure.action).toBe('complete_email_verification')
    const view = mount(View, { props: { failure }, global: { plugins: [createI18n({ legacy: false, locale, messages: { [locale]: { tokenGuardV2: messages } } })] } })
    expect(view.text()).toContain(messages.operationFailure.reasons.email_otp_required)
    expect(view.text()).toContain(messages.operationFailure.actions.complete_email_verification)
    expect(safeOperationFailure({ ...failure, stage: 'web_login' }).code).toBe('unknown')
  })
  it('preserves fixed identity validation reasons and drops arbitrary identity text', () => {
    for (const reason of ['account_id_missing', 'account_id_mismatch', 'user_id_missing', 'email_missing', 'token_account_conflict', 'id_token_invalid']) {
      expect(safeDiagnosticToken(reason)).toBe(reason)
    }
    for (const text of ['user@example.test', 'account_id_mismatch user@example.test', 'token private', 'workspace-actual-id']) {
      expect(safeDiagnosticToken(text)).toBe('unknown')
    }
  })
  it.each(['account_deleted', 'timeout'])('shows %s with its manual action and original time', code => {
    const failure = safeOperationFailure({ code, stage: 'web_login', occurred_at: '2026-10-01T01:02:03Z' })
    const view = mount(View, { props: { failure }, global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { tokenGuardV2: zh } } })] } })
    expect(view.text()).toContain(zh.operationFailure.reasons[code as keyof typeof zh.operationFailure.reasons])
    expect(view.text()).toContain(zh.operationFailure.actions[failure.action as keyof typeof zh.operationFailure.actions])
    expect(view.text()).toContain('web_login')
    expect(view.text()).not.toContain(zh.operationFailure.unknownTime)
    expect(view.find('details').exists()).toBe(true)
  })

  it('drops all arbitrary values, unrecorded time and sensitive text', () => {
    const view = mount(View, { props: { failure: { code: 'synthetic-private', category: 'account_deleted', stage: 'password synthetic-private', action: 'token synthetic-private', occurred_at: 'https://example.test/?cookie=synthetic-private' } }, global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { tokenGuardV2: zh } } })] } })
    expect(view.text()).toContain('未知异常')
    expect(view.text()).toContain('时间未记录')
    expect(view.text()).not.toContain('synthetic-private')
    expect(view.text()).not.toContain('账号已删除')
  })
})
