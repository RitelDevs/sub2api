import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest'
import type { NavigationGuard, RouteLocationNormalized } from 'vue-router'

const state = vi.hoisted(() => ({
  central: true,
  guard: null as NavigationGuard | null,
  auth: {
    isAuthenticated: true,
    isAdmin: false,
    isObserver: false,
    checkAuth: vi.fn(),
    initializeCentralAuth: vi.fn().mockResolvedValue(undefined),
  },
  centralLogin: vi.fn(),
}))

vi.mock('vue-router', async importOriginal => {
  const actual = await importOriginal<typeof import('vue-router')>()
  return {
    ...actual,
    createRouter: (options: Parameters<typeof actual.createRouter>[0]) => {
      const router = actual.createRouter(options)
      const beforeEach = router.beforeEach.bind(router)
      router.beforeEach = guard => {
        state.guard = guard
        return beforeEach(guard)
      }
      return router
    },
  }
})
vi.mock('@/utils/accountConfig', () => ({
  get centralAccountEnabled() { return state.central },
  accountOrigin: 'https://account.example.test',
  accountPageURL: vi.fn(),
}))
// Exercise the existing safety policy; only the external login side effect is mocked.
vi.mock('@/utils/centralAccount', async importOriginal => ({
  ...await importOriginal<typeof import('@/utils/centralAccount')>(),
  centralLogin: state.centralLogin,
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => state.auth }))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ siteName: 'Sub2API', cachedPublicSettings: null, backendModeEnabled: false }),
}))
vi.mock('@/stores/adminSettings', () => ({ useAdminSettingsStore: () => ({ customMenuItems: [] }) }))
vi.mock('@/composables/useNavigationLoading', () => ({
  useNavigationLoadingState: () => ({ startNavigation: vi.fn(), endNavigation: vi.fn() }),
}))
vi.mock('@/composables/useRoutePrefetch', () => ({ useRoutePrefetch: vi.fn() }))

let router: typeof import('@/router')['default']
async function loginRedirect(redirect?: string | null | string[], path = '/login') {
  const to = router.resolve({ path, query: redirect === undefined ? {} : { redirect } })
  const from = router.resolve('/usage')
  const next = vi.fn()
  await state.guard!(to as RouteLocationNormalized, from as RouteLocationNormalized, next)
  expect(next).toHaveBeenCalledOnce()
  return next
}

beforeAll(async () => { router = (await import('@/router')).default })
beforeEach(() => {
  state.central = true
  state.auth.isAuthenticated = true
  state.auth.isAdmin = false
  state.auth.isObserver = false
  state.centralLogin.mockClear()
})

describe('authenticated central login continuation', () => {
  it.each([
    '/keys',
    '/keys?group=2&search=a%2Fb#new-key',
    '/usage?range=week#requests',
    '/admin/accounts?status=active#table',
  ])('honors the safe explicit business target %s', async target => {
    expect(await loginRedirect(target)).toHaveBeenCalledWith(target)
    expect(state.centralLogin).not.toHaveBeenCalled()
  })

  it.each([
    { admin: false, observer: false, fallback: '/dashboard' },
    { admin: true, observer: false, fallback: '/admin/dashboard' },
    { admin: false, observer: true, fallback: '/admin/accounts' },
  ])('uses the role default for missing or rejected targets: $fallback', async role => {
    state.auth.isAdmin = role.admin
    state.auth.isObserver = role.observer
    for (const target of [
      undefined, null, '', ['/keys', '/usage'],
      'https://evil.example/keys', '//evil.example/keys',
      'javascript:alert(1)', 'keys', '/\\evil.example/keys', '/keys\n',
      '/login', '/login?redirect=/keys#again', '/LOGIN', '/login/',
      '/register?aff=ref', '/REGISTER/', '/auth/oauth/callback?code=fake',
      '/auth/callback?code=fake', '/forgot-password', '/reset-password', '/email-verify',
      '/usage/../login?redirect=/keys',
    ]) {
      expect(await loginRedirect(target), String(target)).toHaveBeenCalledWith(role.fallback)
    }
  })

  it.each([
    { admin: true, observer: false },
    { admin: false, observer: true },
  ])('respects an explicit user target even when a privileged role has a different default: %j', async role => {
    state.auth.isAdmin = role.admin
    state.auth.isObserver = role.observer
    expect(await loginRedirect('/keys?group=2#new-key')).toHaveBeenCalledWith('/keys?group=2#new-key')
  })

  it('keeps register continuations safe and does not re-enter central login', async () => {
    expect(await loginRedirect('/keys?group=2#new-key', '/register')).toHaveBeenCalledWith('/keys?group=2#new-key')
    expect(state.centralLogin).not.toHaveBeenCalled()
  })
})

describe('unchanged login branches', () => {
  it('still delegates an unauthenticated central visitor to central login', async () => {
    state.auth.isAuthenticated = false
    expect(await loginRedirect('/keys?group=2#new-key')).toHaveBeenCalledWith(false)
    expect(state.centralLogin).toHaveBeenCalledWith('/keys?group=2#new-key')
  })

  it.each([
    { admin: false, observer: false, fallback: '/dashboard' },
    { admin: true, observer: false, fallback: '/admin/dashboard' },
    { admin: false, observer: true, fallback: '/admin/accounts' },
  ])('preserves legacy authenticated login behavior: $fallback', async role => {
    state.central = false
    state.auth.isAdmin = role.admin
    state.auth.isObserver = role.observer
    expect(await loginRedirect('/keys?group=2#new-key')).toHaveBeenCalledWith(role.fallback)
    expect(state.centralLogin).not.toHaveBeenCalled()
  })
})
