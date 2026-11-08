import { mount, flushPromises, enableAutoUnmount } from '@vue/test-utils'
import { afterEach, describe, it, expect, vi } from 'vitest'
import AccountGroupsCell from '../AccountGroupsCell.vue'
import type { Group } from '@/types'
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({}) }))
enableAutoUnmount(afterEach)
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals() })
describe('account group popover viewport', () => {
  it('fits a right-edge trigger on mobile and repositions after resize', async () => {
    vi.stubGlobal('innerWidth', 320)
    vi.stubGlobal('innerHeight', 240)
    const groups = Array.from({ length: 12 }, (_, i) => ({ id: i, name: 'A long group name' })) as Group[]
    const wrapper = mount(AccountGroupsCell, { attachTo: document.body, props: { groups }, global: { stubs: { GroupBadge: true } } })
    const trigger = wrapper.find('button').element
    vi.spyOn(trigger, 'getBoundingClientRect').mockReturnValue({ left: 285, right: 315, top: 200, bottom: 224, width: 30, height: 24 } as DOMRect)
    await wrapper.find('button').trigger('click'); await flushPromises()
    const panel = document.body.querySelector<HTMLElement>('.fixed.z-50')!
    expect(parseFloat(panel.style.left)).toBeGreaterThanOrEqual(8)
    expect(parseFloat(panel.style.left) + parseFloat(panel.style.width)).toBeLessThanOrEqual(312)
    expect(parseFloat(panel.style.maxHeight)).toBeLessThanOrEqual(224)
    vi.stubGlobal('innerWidth', 240)
    window.dispatchEvent(new Event('resize')); await flushPromises()
    expect(parseFloat(panel.style.left) + parseFloat(panel.style.width)).toBeLessThanOrEqual(232)
    expect(panel.querySelector('.overflow-y-auto')).not.toBeNull()
  })
})
