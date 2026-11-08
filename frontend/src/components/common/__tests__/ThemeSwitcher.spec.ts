import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import ThemeSwitcher from '../ThemeSwitcher.vue'

const preference = ref('system')
const setTheme = vi.fn(value => { preference.value = value })
vi.mock('@/composables/useTheme', () => ({ useTheme: () => ({ preference, setTheme }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => ({
  'common.theme.label': 'Appearance', 'common.theme.system': 'System',
  'common.theme.light': 'Light', 'common.theme.dark': 'Dark',
}[key] ?? key) }) }))
enableAutoUnmount(afterEach)
beforeEach(() => { preference.value = 'system'; vi.clearAllMocks() })
afterEach(() => { document.body.innerHTML = '' })
const render = () => mount(ThemeSwitcher, { attachTo: document.body, global: { stubs: { Teleport: true } } })

describe('theme switcher', () => {
  it('keeps an open menu inside a short viewport and repositions on resize', async () => {
    const oldWidth = window.innerWidth
    const oldHeight = window.innerHeight
    Object.defineProperty(window, 'innerWidth', { configurable: true, value: 320 })
    Object.defineProperty(window, 'innerHeight', { configurable: true, value: 240 })
    try {
      const wrapper = render()
      const trigger = wrapper.get('button[aria-haspopup="menu"]')
      vi.spyOn(trigger.element, 'getBoundingClientRect').mockReturnValue({ left: 280, right: 316, top: 190, bottom: 226, width: 36, height: 36 } as DOMRect)
      await trigger.trigger('click'); await flushPromises()
      const menu = wrapper.get<HTMLElement>('[role="menu"]')
      expect(Number.parseFloat(menu.element.style.left)).toBeGreaterThanOrEqual(8)
      expect(Number.parseFloat(menu.element.style.left) + Number.parseFloat(menu.element.style.width)).toBeLessThanOrEqual(312)
      expect(menu.element.style.bottom).not.toBe('')
      expect(Number.parseFloat(menu.element.style.maxHeight)).toBeLessThanOrEqual(182)
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: 220 })
      window.dispatchEvent(new Event('resize'))
      await flushPromises()
      const resizedMenu = wrapper.get<HTMLElement>('[role="menu"]')
      expect(Number.parseFloat(resizedMenu.element.style.left) + Number.parseFloat(resizedMenu.element.style.width)).toBeLessThanOrEqual(212)
    } finally {
      Object.defineProperty(window, 'innerWidth', { configurable: true, value: oldWidth })
      Object.defineProperty(window, 'innerHeight', { configurable: true, value: oldHeight })
    }
  })
  it('uses the branded icon menu and commits exactly one explicit choice', async () => {
    const wrapper = render()
    expect(wrapper.find('select').exists()).toBe(false)
    const trigger = wrapper.get('button[aria-haspopup="menu"]')
    expect(trigger.attributes('aria-label')).toBe('Appearance')
    expect(trigger.attributes('aria-expanded')).toBe('false')
    await trigger.trigger('click'); await flushPromises()
    const options = wrapper.findAll('[role="menuitemradio"]')
    expect(options.map(option => option.text())).toEqual(['System', 'Light', 'Dark'])
    expect(options.every(option => option.find('svg').exists())).toBe(true)
    expect(options[0].attributes('aria-checked')).toBe('true')
    await options[1].trigger('click')
    expect(setTheme).toHaveBeenCalledTimes(1)
    expect(setTheme).toHaveBeenCalledWith('light')
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    expect(trigger.attributes('title')).toBe('Appearance: Light')
  })
  it('supports arrows and Escape without changing the selected theme', async () => {
    const wrapper = render()
    const trigger = wrapper.get('button[aria-haspopup="menu"]')
    trigger.element.focus()
    await trigger.trigger('keydown', { key: 'ArrowDown' }); await flushPromises()
    const options = wrapper.findAll('[role="menuitemradio"]')
    expect(document.activeElement).toBe(options[0].element)
    await options[0].trigger('keydown', { key: 'ArrowDown' }); await flushPromises()
    const light = wrapper.get('[role="menuitemradio"][tabindex="0"]')
    expect(light.text()).toBe('Light')
    expect(document.activeElement).toBe(light.element)
    await light.trigger('keydown', { key: 'End' }); await flushPromises()
    const dark = wrapper.get('[role="menuitemradio"][tabindex="0"]')
    expect(dark.text()).toBe('Dark')
    expect(document.activeElement).toBe(dark.element)
    await dark.trigger('keydown', { key: 'Escape' })
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    expect(document.activeElement).toBe(trigger.element)
    expect(setTheme).not.toHaveBeenCalled()
  })
  it('closes on outside pointer interaction without changing preferences', async () => {
    const wrapper = render()
    await wrapper.get('button').trigger('click'); await flushPromises()
    document.body.dispatchEvent(new MouseEvent('pointerdown', { bubbles: true }))
    document.body.dispatchEvent(new MouseEvent('click', { bubbles: true, detail: 1 }))
    await flushPromises()
    expect(wrapper.find('[role="menu"]').exists()).toBe(false)
    expect(setTheme).not.toHaveBeenCalled()
  })
  it('respects an externally changed theme and does not rewrite the same choice', async () => {
    const wrapper = render()
    preference.value = 'dark'
    await wrapper.get('button').trigger('click'); await flushPromises()
    const checked = wrapper.get('[role="menuitemradio"][aria-checked="true"]')
    expect(checked.text()).toBe('Dark')
    await checked.trigger('click')
    expect(setTheme).not.toHaveBeenCalled()
  })
})
