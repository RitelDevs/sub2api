import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia } from 'pinia'
import UserApiKeysModal from '../UserApiKeysModal.vue'
import type { AdminUser } from '@/types'

const { getKeys } = vi.hoisted(() => ({ getKeys: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: {
  users: { getUserApiKeys: getKeys }, groups: { getAll: vi.fn().mockResolvedValue([]) },
} }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isAdmin: true, user: { id: 41 } }) }))
vi.mock('@/utils/format', () => ({ formatDateTime: (value: string) => value }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
beforeEach(() => { getKeys.mockReset(); vi.spyOn(console, 'error').mockImplementation(() => {}) })
afterEach(() => vi.restoreAllMocks())
function deferred() {
  let resolve!: (value: unknown) => void
  let reject!: (error: Error) => void
  const promise = new Promise((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}
const user = (id: number) => ({ id, email: `user${id}@example.com`, username: `user${id}` }) as AdminUser
const keys = (id: number, name: string) => ({ items: [{ id, name, key: 'sk-example-key-value-for-tests', status: 'active', created_at: '2026-09-20', group_id: null }] })
async function open() {
  const wrapper = mount(UserApiKeysModal, {
    props: { show: false, user: user(1) },
    global: { plugins: [createPinia()], stubs: {
      BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
      GroupBadge: true, GroupOptionItem: true,
      ApiKeyModelPolicyDialog: true,
    } },
  })
  await wrapper.setProps({ show: true })
  return wrapper
}
async function switchUser(wrapper: Awaited<ReturnType<typeof open>>) {
  await wrapper.setProps({ show: false })
  await wrapper.setProps({ show: true, user: user(2) })
}

describe('user API key loading', () => {
  it('closes only the nested model dialog on Escape', async () => {
    getKeys.mockResolvedValueOnce(keys(17, 'selected-key'))
    const wrapper = mount(UserApiKeysModal, {
      attachTo: document.body,
      props: { show: false, user: user(1) },
      global: { plugins: [createPinia()], stubs: { GroupBadge: true, GroupOptionItem: true } }
    })
    await wrapper.setProps({ show: true }); await flushPromises()
    const button = document.querySelector<HTMLButtonElement>('[data-test="edit-key-model-policy"]')!
    button.click(); await flushPromises()
    expect(document.querySelectorAll('[role="dialog"]')).toHaveLength(2)
    document.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    await flushPromises()
    expect(wrapper.findComponent({ name: 'ApiKeyModelPolicyDialog' }).props('show')).toBe(false)
    expect(wrapper.emitted('close')).toBeUndefined()
  })
  it('opens model settings for the selected key and replaces its saved settings', async () => {
    getKeys.mockResolvedValueOnce(keys(17, 'selected-key'))
    const wrapper = await open(); await flushPromises()
    await wrapper.get('[data-test="edit-key-model-policy"]').trigger('click')
    const dialog = wrapper.getComponent({ name: 'ApiKeyModelPolicyDialog' })
    expect(dialog.props('apiKey').id).toBe(17)
    dialog.vm.$emit('updated', { ...keys(17, 'selected-key').items[0], reasoning_effort: 'high', allow_long_context: false })
    dialog.vm.$emit('close')
    await flushPromises()
    await wrapper.get('[data-test="edit-key-model-policy"]').trigger('click')
    expect(dialog.props('apiKey').reasoning_effort).toBe('high')
    expect(dialog.props('apiKey').allow_long_context).toBe(false)
    await wrapper.setProps({ show: false })
    expect(dialog.props('show')).toBe(false)
  })
  it('does not display the previous user keys when the next load fails', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-user-key')).mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = await open(); await flushPromises()
    expect(wrapper.text()).toContain('first-user-key')
    await switchUser(wrapper); await flushPromises()
    expect(wrapper.text()).toContain('user2@example.com')
    expect(wrapper.text()).not.toContain('first-user-key')
  })

  it('does not replace current keys with a late previous response', async () => {
    const old = deferred()
    getKeys.mockReturnValueOnce(old.promise).mockResolvedValueOnce(keys(2, 'current-user-key'))
    const wrapper = await open()
    await switchUser(wrapper); await flushPromises()
    old.resolve(keys(1, 'old-user-key')); await flushPromises()
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.text()).not.toContain('old-user-key')
  })

  it('keeps the current request loading when an obsolete request fails', async () => {
    const old = deferred(); const current = deferred()
    getKeys.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const wrapper = await open()
    await switchUser(wrapper)
    old.reject(new Error('obsolete')); await flushPromises()
    expect(wrapper.find('.animate-spin').exists()).toBe(true)
    current.resolve(keys(2, 'current-user-key')); await flushPromises()
    expect(wrapper.text()).toContain('current-user-key')
    expect(wrapper.find('.animate-spin').exists()).toBe(false)
  })

  it('loads keys when the selected user changes while the dialog is open', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-user-key')).mockResolvedValueOnce(keys(2, 'second-user-key'))
    const wrapper = await open(); await flushPromises()
    await wrapper.setProps({ user: user(2) }); await flushPromises()
    expect(getKeys).toHaveBeenLastCalledWith(2)
    expect(wrapper.text()).toContain('second-user-key')
    expect(wrapper.text()).not.toContain('first-user-key')
  })
})

describe('user API keys owned group portal', () => {
  it('Tabs into sibling portal and consumes Escape before parent', async () => {
    getKeys.mockResolvedValueOnce(keys(1, 'first-user-key'))
    const wrapper = mount(UserApiKeysModal, { attachTo: document.body, props: { show: false, user: user(1) }, global: { stubs: { transition: true, GroupBadge: true, GroupOptionItem: true } } })
    await wrapper.setProps({ show: true }); await flushPromises()
    const trigger = document.querySelector<HTMLButtonElement>('[data-test="edit-key-group"]')!
    trigger.click(); await flushPromises(); trigger.focus()
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Tab', bubbles: true, cancelable: true }))
    const portal = document.querySelector<HTMLElement>('.fixed.z-\\[100000020\\]')
    expect(portal?.contains(document.activeElement)).toBe(true)
    document.activeElement!.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    await flushPromises()
    expect(document.activeElement).toBe(trigger)
    expect(wrapper.emitted('close')).toBeUndefined()
    trigger.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true, cancelable: true }))
    expect(wrapper.emitted('close')).toHaveLength(1)
  })
  it('keeps the group panel within a narrow viewport and updates on resize', async () => {
    vi.stubGlobal('innerWidth', 320)
    vi.stubGlobal('innerHeight', 240)
    getKeys.mockResolvedValue(keys(1, 'first-user-key'))
    const wrapper = await open(); await flushPromises()
    const trigger = wrapper.get('[data-test="edit-key-group"]').element
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue({ left: 290, right: 318, top: 180, bottom: 204, width: 28, height: 24 } as DOMRect)
    trigger.click(); await flushPromises()
    const panel = document.querySelector<HTMLElement>('.fixed.z-\\[100000020\\]')!
    expect(parseFloat(panel.style.left)).toBeGreaterThanOrEqual(8)
    expect(parseFloat(panel.style.left) + parseFloat(panel.style.width)).toBeLessThanOrEqual(312)
    expect(parseFloat(panel.style.maxHeight)).toBeLessThanOrEqual(224)
    vi.stubGlobal('innerWidth', 240)
    window.dispatchEvent(new Event('resize')); await flushPromises()
    expect(parseFloat(panel.style.left) + parseFloat(panel.style.width)).toBeLessThanOrEqual(232)
    vi.unstubAllGlobals()
  })
  it('dismisses group portal on hide and does not reopen it next time', async () => {
    getKeys.mockResolvedValue(keys(1, 'first-user-key'))
    const wrapper = mount(UserApiKeysModal, { attachTo: document.body, props: { show: false, user: user(1) }, global: { stubs: { transition: true, GroupBadge: true, GroupOptionItem: true } } })
    await wrapper.setProps({ show: true }); await flushPromises()
    document.querySelector<HTMLButtonElement>('[data-test="edit-key-group"]')!.click(); await flushPromises()
    await wrapper.setProps({ show: false }); await flushPromises()
    expect(document.querySelector('.fixed.z-\\[100000020\\]')).toBeNull()
    await wrapper.setProps({ show: true }); await flushPromises()
    expect(document.querySelector('.fixed.z-\\[100000020\\]')).toBeNull()
  })
})
