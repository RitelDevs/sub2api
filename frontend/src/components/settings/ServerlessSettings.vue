<template>
  <details class="open-section" @toggle="onToggle">
    <summary class="cursor-pointer px-6 py-4 font-semibold">{{ t('consoleInline.serverlessManagement_0f494d') }}
      <span class="ml-2 text-xs font-normal text-gray-500">{{ text('地区 → Pod（可选）', 'Region → Pod (optional)') }}</span>
      <NewFeatureBadge v-if="userId != null" :notice-id="NEW_FEATURE_NOTICES.serverlessIPRouting" :user-id="userId" class="ml-2" />
    </summary>
    <div class="space-y-5 border-t border-gray-100 p-6 dark:border-dark-700">
      <p class="text-sm text-gray-500">{{ text('管理已有请求 Pod，不创建或扩缩容。默认关闭；未知地区走主站。IP 国家识别结果在 Redis 缓存 24 小时，命中时不再请求地区服务；识别失败缓存 1 分钟。', 'Manage existing request Pods without creating or scaling them. Disabled by default; unknown regions use the primary. IP country results are cached in Redis for 24 hours without another provider lookup on a cache hit; failed lookups are cached for one minute.') }}</p>
      <p v-if="error" role="alert" class="text-sm text-red-600">{{ error }}</p>
      <p v-if="notice" role="status" class="text-sm text-green-600">{{ notice }}</p>
      <div class="flex flex-wrap items-center gap-4">
        <label class="flex items-center gap-2"><input v-model="draft.enabled" type="checkbox" :disabled="busy || !loaded" data-testid="serverless-enabled" />{{ t('consoleInline.enableRegionRouting_14520c') }}</label>
        <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="refresh(false)">{{ t('consoleInline.refreshStatus_a26544') }}</button>
        <button v-if="!managed && dirty" type="button" class="btn btn-primary btn-sm" :disabled="busy || !loaded" @click="save">{{ t('consoleInline.saveServerlessConfiguration_9c8473') }}</button>
      </div>
      <div class="space-y-3 rounded-lg border border-gray-200 p-3 text-sm dark:border-dark-700">
        <label class="flex flex-wrap items-center gap-3">{{ text('绑定范围', 'Binding scope') }}
          <NewFeatureBadge v-if="userId != null" :notice-id="NEW_FEATURE_NOTICES.serverlessIPRouting" :user-id="userId" />
          <select v-model="draft.affinity_scope" class="input w-auto" :disabled="busy || !loaded" data-testid="serverless-affinity-scope">
            <option value="api_key">{{ text('API Key（默认）', 'API Key (default)') }}</option>
            <option value="api_key_ip">{{ text('API Key ＋ 客户端 IP', 'API Key + client IP') }}</option>
          </select>
        </label>
        <p class="text-xs text-gray-500">{{ text('同一 Key 需要按使用者地区分流时，选择 Key ＋ IP。每个绑定最后使用后保留 24 小时。切换范围会使用独立绑定；IP 改变可能更换 Pod，不能用于依赖节点内状态的会话。', 'Choose Key + IP when one key is used from multiple regions. Each binding lasts 24 hours after last use. Changing scope uses separate bindings; changing IP may select another Pod, so do not use this for sessions with node-local state.') }}</p>
        <label class="flex items-center gap-2"><input v-model="draft.fallback_to_primary" type="checkbox" :disabled="busy || !loaded" data-testid="serverless-primary-fallback" />{{ text('已绑定 Pod 不可用时回主站', 'Use primary when a bound Pod is unavailable') }}</label>
        <p class="text-xs text-gray-500">{{ text('默认关闭，失联时保留绑定并返回错误。开启后仅在转发前探测失败或实例失联时将绑定改为主站；不会重试已发送请求或已开始的流。回主站后保持绑定，不自动切回。', 'Off by default: preserve the binding and return an error. When enabled, move the binding to the primary only if the instance is missing or its probe fails before forwarding. Sent requests and started streams are never retried. A fallback binding stays on the primary without automatic failback.') }}</p>
      </div>
      <p class="rounded-lg bg-amber-50 p-3 text-xs text-amber-800 dark:bg-amber-950/30 dark:text-amber-300">{{ text('停用分流或 Pod 只阻止新绑定；已有绑定继续使用原 Pod。首次启用或更改绑定范围前，先结束存量 BPS/WS 会话。', 'Disabling routing or a Pod stops new bindings; existing bindings remain. Finish existing BPS/WS conversations before first enabling routing or changing binding scope.') }}</p>

      <section class="space-y-3">
        <AppHeading as="h3" variant="subsection" >{{ t('consoleInline.automaticallyRegisteredPods_54aae8') }}</AppHeading>
        <p class="text-xs text-gray-500">{{ t('consoleInline.aReadyHeartbeatDoesNotProveIngress_13866b') }}</p>
        <p v-if="!pods.length" class="text-sm text-gray-500">{{ t('consoleInline.noPodsYetConfigureAnExistingGateway_ea822e') }}</p>
        <div v-for="pod in pods" :key="pod.id" class="border-t border-gray-200 py-4 text-sm dark:border-dark-700">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="font-medium">{{ pod.id }} <span class="text-xs text-gray-500">{{ pod.region || '—' }} · {{ pod.version || '—' }}</span></span>
            <span :class="pod.ready ? 'text-green-600' : 'text-amber-600'">{{ pod.ready ? t('consoleInline.heartbeatReady_e1e064') : t('consoleInline.notReadyOffline_118587') }}</span>
          </div>
          <p class="mt-1 break-all text-xs text-gray-500">{{ pod.endpoint }} · {{ formatDisplayTimestamp(pod.at * 1000) }}</p>
          <div class="mt-2 flex flex-wrap items-center gap-2">
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || approved(pod)" @click="approve(pod)">{{ approved(pod) ? t('consoleInline.endpointApproved_87f6b7') : t('consoleInline.approveEndpoint_c98329') }}</button>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !approved(pod)" @click="probe(pod.id)">{{ t('consoleInline.checkConnection_9b579b') }}</button>
            <span v-if="probes[pod.id] !== undefined" class="text-xs">{{ probes[pod.id] ? t('consoleInline.reachableAtLastCheck_624df9') : t('consoleInline.checkFailed_679606') }}</span>
          </div>
        </div>
        <div v-for="policy in draft.pods" :key="policy.id" class="flex flex-wrap items-center gap-3 text-sm">
          <label class="flex items-center gap-2"><input v-model="policy.enabled" type="checkbox" :disabled="busy" />{{ policy.id }} · {{ t('consoleInline.acceptNewBindings_5ed77b') }}</label>
          <code class="break-all text-xs text-gray-500">{{ policy.endpoint }}</code>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="removePod(policy.id)">{{ t('consoleInline.removePolicy_3c5607') }}</button>
        </div>
      </section>

      <section class="space-y-3">
        <div class="flex items-center justify-between"><AppHeading as="h3" variant="subsection" >{{ t('consoleInline.regionPodPool_b1e326') }}</AppHeading>
          <button type="button" class="btn btn-secondary btn-sm" :disabled="busy || !loaded" @click="addRegion">{{ t('consoleInline.addRegion_f621ad') }}</button>
        </div>
        <div v-for="(region, index) in draft.regions" :key="index" class="space-y-3 border-t border-gray-200 py-4 dark:border-dark-700">
          <div class="flex flex-wrap items-center gap-3">
            <label class="text-sm">{{ t('consoleInline.countryCode_e645d9') }}<input v-model="region.country" maxlength="2" class="input ml-2 w-24" placeholder="US" :disabled="busy" /></label>
            <button type="button" class="btn btn-secondary btn-sm" :disabled="busy" @click="draft.regions.splice(index, 1)">{{ t('consoleInline.removeRegion_89678b') }}</button>
          </div>
          <div class="flex flex-wrap gap-4">
            <label v-for="pod in draft.pods" :key="pod.id" class="flex items-center gap-2 text-sm"><input v-model="region.pod_ids" :value="pod.id" type="checkbox" :disabled="busy" />{{ pod.id }}</label>
          </div>
        </div>
      </section>

      <section>
        <AppHeading as="h3" variant="subsection" class="mb-2">{{ t('consoleInline.routingTodayUTC_8ee555') }}</AppHeading>
        <div class="overflow-x-auto"><table class="w-full text-left text-sm">
          <thead><tr><th>Pod</th><th>{{ t('consoleInline.region_9833d9') }}</th><th>{{ t('consoleInline.reason_2e3c65') }}</th><th>{{ t('consoleInline.requests_d02991') }}</th><th>{{ t('consoleInline.hTTPErrors_ccb3e3') }}</th></tr></thead>
          <tbody><tr v-for="row in statRows" :key="row.key"><td>{{ row.pod }}</td><td>{{ row.country }}</td><td>{{ reasonLabel(row.reason) }}</td><td>{{ row.requests }}</td><td>{{ row.errors }}</td></tr></tbody>
        </table></div>
        <p class="mt-2 text-xs text-gray-500">{{ t('consoleInline.countsCoverCompletedIngressHTTPWSRequests_1ee534') }}</p>
      </section>
      <details class="text-sm">
        <summary class="cursor-pointer">{{ t('consoleInline.podConnectionInstructions_89e705') }}</summary>
        <p class="mt-2">{{ t('consoleInline.thePrimaryAndPodsSharePostgreSQLRedis_fea5e4') }}</p>
        <pre class="mt-2 overflow-x-auto rounded bg-gray-50 p-3 text-xs dark:bg-dark-800">RUNTIME_ROLE=gateway
RUNTIME_SERVERLESS_ID=nerd-us-1
RUNTIME_SERVERLESS_REGION=US
RUNTIME_SERVERLESS_ENDPOINT=https://pod.example.com</pre>
        <p class="mt-2 text-xs text-gray-500">{{ t('consoleInline.iDsMustBeGloballyUniqueTheEndpoint_e46638') }}</p>
      </details>
    </div>
  </details>

  <ConfirmationPrompt :confirmation="confirmation" />
</template>

<script setup lang="ts">
import { formatTimestamp as formatDisplayTimestamp } from "@/utils/consoleDisplay"

import AppHeading from '@/components/common/AppHeading.vue'
import ConfirmationPrompt from '@/components/common/ConfirmationPrompt.vue'
import { useConfirmation } from '@/composables/useConfirmation'

import { computed, onBeforeUnmount, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { apiClient } from '@/api/client'
import NewFeatureBadge from '@/components/common/NewFeatureBadge.vue'
import { NEW_FEATURE_NOTICES } from '@/constants/newFeatureNotices'


interface Policy { id: string; endpoint: string; enabled: boolean }
interface Region { country: string; pod_ids: string[] }
interface Config { enabled: boolean; affinity_scope?: 'api_key' | 'api_key_ip'; fallback_to_primary?: boolean; pods: Policy[]; regions: Region[] }
interface Pod { id: string; boot: string; endpoint: string; region: string; version: string; ready: boolean; at: number }
interface Snapshot { config: Config; pods: Pod[]; stats: Record<string, string> }
withDefaults(defineProps<{ managed?: boolean; userId?: number | null }>(), { managed: false })
const saved = ref<Config | null>(null)
const dirty = computed(() => saved.value !== null && JSON.stringify(draft.value) !== JSON.stringify(saved.value))
function discard() { if (saved.value) draft.value = JSON.parse(JSON.stringify(saved.value)) }
defineExpose({ dirty, save, discard })
const { t, locale } = useI18n()
const text = (zh: string, en: string) => locale.value.startsWith('zh') ? zh : en
const loaded = ref(false), busy = ref(false), error = ref(''), notice = ref('')
const draft = ref<Config>({ enabled: false, affinity_scope: 'api_key', fallback_to_primary: false, pods: [], regions: [] })
function normalizeConfig(config: Config): Config {
  return { ...config, affinity_scope: config.affinity_scope || 'api_key', fallback_to_primary: config.fallback_to_primary ?? false }
}
const pods = ref<Pod[]>([]), stats = ref<Record<string, string>>({}), probes = ref<Record<string, boolean>>({})
let disposed = false
onBeforeUnmount(() => { disposed = true })
const statRows = computed(() => {
  const rows = new Map<string, {key: string; pod: string; country: string; reason: string; requests: number; errors: number}>()
  for (const [key, value] of Object.entries(stats.value)) {
    const [pod, country, reason, metric] = key.split('|')
    if (!pod || !country || !reason || !['requests', 'errors'].includes(metric ?? '')) continue
    const id = [pod, country, reason].join('|')
    const row = rows.get(id) ?? { key: id, pod, country, reason, requests: 0, errors: 0 }
    if (metric === 'requests') row.requests = Number(value)
    if (metric === 'errors') row.errors = Number(value)
    rows.set(id, row)
  }
  return [...rows.values()]
})
function reasonLabel(reason: string) {
  const labels: Record<string, string> = {
    region: t('consoleInline.regionMatch_a03aba'), binding: t('consoleInline.existingBinding_b54730'),
    default: t('consoleInline.defaultPrimary_1e5a9b'), unhealthy_fallback: t('consoleInline.noHealthyPodPrimary_ca4d26'),
    binding_unavailable: t('consoleInline.boundInstanceUnavailable_7342f7'),
    binding_fallback: text('绑定实例失联，已回主站', 'Bound instance unavailable; moved to primary'),
  }
  return labels[reason] ?? reason
}
async function action(fn: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''; notice.value = ''
  try { await fn() } catch { if (!disposed) error.value = t('consoleInline.operationFailedCheckConfigurationClusterCredentialsAnd_7e6d1a') }
  finally { if (!disposed) busy.value = false }
}
async function refresh(replace: boolean) {
  await action(async () => {
    const data = (await apiClient.get<Snapshot>('/admin/serverless')).data
    if (disposed) return
    pods.value = data.pods; stats.value = data.stats
    if ((!dirty.value && replace) || !loaded.value) { draft.value = normalizeConfig(data.config); saved.value = JSON.parse(JSON.stringify(draft.value)) }
    loaded.value = true
  })
}
function onToggle(event: Event) { if ((event.target as HTMLDetailsElement).open && !loaded.value) void refresh(true) }
async function save() {
  await action(async () => {
    const config: Config = JSON.parse(JSON.stringify(draft.value))
    config.regions.forEach(r => { r.country = r.country.trim().toUpperCase() })
    const { data } = await apiClient.put<Config>('/admin/serverless', config)
    if (!disposed) { draft.value = normalizeConfig(data); saved.value = JSON.parse(JSON.stringify(draft.value)); notice.value = text('已保存。新规则在数秒内生效；相同绑定范围内继续使用已有绑定。', 'Saved. New rules take effect within seconds; existing bindings remain within the same scope.') }
  })
}
function approved(p: Pod) { return draft.value.pods.some(v => v.id === p.id && v.endpoint.replace(/\/$/, '') === p.endpoint) }
function approve(p: Pod) {
  const existing = draft.value.pods.find(v => v.id === p.id)
  if (existing) { existing.endpoint = p.endpoint; existing.enabled = false }
  else draft.value.pods.push({ id: p.id, endpoint: p.endpoint, enabled: false })
  notice.value = t('consoleInline.addedToDraftSaveBeforeCheckingConnectivity_d34750')
}
async function removePod(id: string) {
  await confirmation.run({ message: text('移除后，已有绑定会返回错误，或按已保存的回退设置迁到主站。确认该 Pod 已排空？', 'After removal, existing bindings fail or move to the primary according to the saved fallback policy. Confirm that this Pod is drained?'), title: t('consoleInline.remove_ca30f1'), confirmText: t('consoleInline.remove_ca30f1'), danger: true }, async () => {
    draft.value.pods = draft.value.pods.filter(p => p.id !== id)
    draft.value.regions.forEach(r => { r.pod_ids = r.pod_ids.filter(p => p !== id) })
  })
}
function addRegion() { draft.value.regions.push({ country: '', pod_ids: [] }) }
async function probe(id: string) {
  await action(async () => {
    const { data } = await apiClient.post<{ ready: boolean }>('/admin/serverless/pods/' + encodeURIComponent(id) + '/probe')
    if (!disposed) probes.value[id] = data.ready
  })
}
const confirmation = useConfirmation(() => ({ active: true, identity: draft.value }))
</script>
