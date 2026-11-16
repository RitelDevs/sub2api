<template>
  <BaseDialog
    :show="show"
    :title="t('keys.useKeyModal.title')"
    width="wide"
    @close="emit('close')"
  >
    <div class="space-y-4">
      <section v-if="keyRecord" class="space-y-4 border-b border-gray-200 pb-5 dark:border-dark-700" data-testid="key-connection-summary">
        <AppHeading as="h2" variant="section">{{ keyRecord.name }}</AppHeading>
        <dl class="space-y-3">
          <div v-for="endpoint in connectionUrls" :key="endpoint.url" class="space-y-1">
            <dt class="text-sm text-gray-500 dark:text-gray-400">{{ kc.configUrl }}<span v-if="endpoint.label"> · {{ endpoint.label }}</span></dt>
            <dd class="flex min-w-0 flex-wrap items-center gap-3">
              <code class="min-w-0 flex-1 break-all" data-testid="connection-url">{{ endpoint.url }}</code>
              <button type="button" class="btn btn-secondary" data-testid="copy-connection-url" @click="clipboardCopy(endpoint.url)">{{ kc.copyUrl }}</button>
            </dd>
          </div>
          <div class="space-y-1">
            <dt class="text-sm text-gray-500 dark:text-gray-400">API Key</dt>
            <dd class="flex min-w-0 flex-wrap items-center gap-3">
              <code class="min-w-0 flex-1 break-all" data-testid="connection-key">{{ apiKey }}</code>
              <button type="button" class="btn btn-secondary" data-testid="copy-connection-key" @click="clipboardCopy(apiKey)">{{ kc.copyKey }}</button>
            </dd>
          </div>
        </dl>
      </section>
      <!-- No Group Assigned Warning -->
      <div v-if="!platform" class="flex items-start gap-3 p-4 rounded-lg bg-yellow-50 dark:bg-yellow-900/20 border border-yellow-200 dark:border-yellow-800">
        <svg class="w-5 h-5 text-yellow-500 flex-shrink-0 mt-0.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m-9.303 3.376c-.866 1.5.217 3.374 1.948 3.374h14.71c1.73 0 2.813-1.874 1.948-3.374L13.949 3.378c-.866-1.5-3.032-1.5-3.898 0L2.697 16.126zM12 15.75h.007v.008H12v-.008z" />
        </svg>
        <div>
          <p class="text-sm font-medium text-yellow-800 dark:text-yellow-200">
            {{ t('keys.useKeyModal.noGroupTitle') }}
          </p>
          <p class="text-sm text-yellow-700 dark:text-yellow-300 mt-1">
            {{ t('keys.useKeyModal.noGroupDescription') }}
          </p>
        </div>
      </div>

      <!-- Platform-specific content -->
      <template v-else>
        <!-- Client Tabs -->
        <div v-if="clientTabs.length" class="connection-tabs overflow-x-auto border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex min-w-max gap-4 sm:gap-6" role="tablist" :aria-label="kc.application">
            <button
              v-for="tab in applicationTabs"
              :key="tab.id"
              type="button"
              role="tab"
              :id="`connection-tab-${tab.id}`"
              :data-app-tab="tab.id"
              :aria-selected="activeApplication === tab.id"
              :tabindex="activeApplication === tab.id ? 0 : -1"
              aria-controls="connection-application-panel"
              @keydown="onApplicationTabKeydown($event, tab.id)"
              @click="activeClientTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                (activeClientTab === tab.id || (tab.id === 'codex' && activeClientTab === 'codex-ws'))
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <CcsAppIcon v-if="typeof tab.icon === 'string'" :app="tab.icon" />
                <component v-else :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <section id="connection-application-panel" role="tabpanel" :aria-labelledby="`connection-tab-${activeApplication}`" :data-application="activeApplication" class="space-y-4">
        <section v-if="platform !== 'typesafe'" class="space-y-3" data-testid="application-model-selection" aria-live="polite">
          <p v-if="unavailableMessage()" :role="codexModelManifestState === 'error' ? 'alert' : 'status'" class="text-sm text-gray-600 dark:text-gray-300" data-testid="model-selection-status">{{ unavailableMessage() }}</p>
          <button v-if="codexModelManifestState === 'error'" type="button" class="btn btn-secondary" data-testid="models-retry" @click="loadCodexModelManifest">{{ kc.modelsRetry }}</button>
          <template v-if="modelSetupReady">
            <p v-if="nativeClaude" class="text-sm text-gray-600 dark:text-gray-300" data-testid="native-claude-model">{{ kc.nativeClaudeModel }}</p>
            <label v-else class="block">
              <span class="input-label">{{ kc.defaultModel }}</span>
              <select v-model="selectedModel" class="input" data-testid="application-model-select">
                <option v-for="model in modelOptions" :key="model" :value="model">{{ model }}</option>
              </select>
            </label>
            <p v-if="!nativeClaude" class="text-xs text-gray-500 dark:text-gray-400">{{ kc.modelSelectionScope }}</p>
            <p v-if="!nativeClaude" class="text-xs text-gray-500 dark:text-gray-400" data-testid="model-catalog-capabilities">
              {{ kc.catalogCapabilities }} · {{ kc.contextWindow }}: {{ activeProfile?.contextWindow ?? kc.capabilityUnknown }} · {{ kc.reasoningEffort }}: {{ activeProfile?.reasoningEffort ?? kc.clientDefault }} · {{ kc.inputModalities }}: {{ selectedInputModalities.join(' / ') || kc.capabilityUnknown }}
            </p>
            <dl v-if="roleModels || nativeClaude" class="space-y-2 text-sm" data-testid="model-role-table">
              <div v-for="role in ['opus', 'fable', 'sonnet', 'haiku'] as const" :key="role" class="grid grid-cols-[5rem_minmax(0,1fr)] gap-3">
                <dt class="capitalize text-gray-500 dark:text-gray-400">{{ role }}</dt>
                <dd class="break-all"><code v-if="roleModels">{{ roleModels[role] }}</code><span v-else>{{ kc.clientDefault }}</span></dd>
              </div>
              <div v-if="activeApplication === 'claude'" class="grid grid-cols-[5rem_minmax(0,1fr)] gap-3">
                <dt class="text-gray-500 dark:text-gray-400">Subagent</dt><dd>{{ kc.subagentInherit }}</dd>
              </div>
            </dl>
            <p v-if="activeApplication === 'claude-desktop'" class="text-sm text-amber-700 dark:text-amber-300" data-testid="desktop-model-limits">{{ kc.desktopModelLimits }}</p>
          </template>
        </section>
        <div v-if="selectedCcsProfiles.length" class="space-y-3" data-testid="selected-application-import">
          <p v-if="activeApplication === 'codex'" class="text-sm text-gray-600 dark:text-gray-300">{{ kc.codexSharedConfig }}</p>
          <p v-if="activeApplication === 'codex'" class="text-sm text-gray-500 dark:text-gray-400" data-testid="codex-fast-notice">{{ kc.codexFastNotice }}</p>
          <div v-if="activeApplication === 'claude-desktop'" class="space-y-2 text-sm text-gray-600 dark:text-gray-300" data-testid="claude-desktop-import-guide">
            <p>{{ kc.claudeDesktopSeparate }}</p>
          </div>
          <div class="flex flex-wrap gap-2">
            <button v-for="profile in selectedCcsProfiles" :key="profile.id" type="button" class="btn btn-primary" :disabled="!canImportApplication(profile)" :title="unavailableMessage(profile) || undefined" :data-testid="`connection-ccs-${profile.id}`" @click="importConnection(profile)">
              <Icon name="download" size="sm" class="mr-2" />{{ kc.oneClickImport }} · {{ profile.label }}
            </button>
          </div>

          <div v-if="fullProviderConfig" class="space-y-2" data-testid="full-provider-config">
            <button type="button" class="btn btn-secondary" data-testid="copy-full-provider-config" @click="clipboardCopy(fullProviderConfig.content)">{{ kc.copyFullProviderConfig }}</button>
            <p class="text-sm text-gray-500 dark:text-gray-400">{{ fullProviderConfig.formatLabel }} · {{ kc.fullProviderConfigScope }}</p>
            <p v-if="fullProviderConfig.requiresProviderId" class="text-sm text-gray-500 dark:text-gray-400">{{ kc.providerIdPlaceholder }}</p>
          </div>
          <div v-if="activeApplication === 'claude-desktop'" class="space-y-2 text-sm text-gray-600 dark:text-gray-300">
            <p>{{ kc.claudeDesktopImportNext }}</p>
            <p>{{ kc.claudeDesktopRestart }}</p>
            <a href="https://github.com/farion1231/cc-switch/blob/v4.0.26/docs/user-manual/zh/2-providers/2.6-claude-desktop.md" target="_blank" rel="noopener noreferrer" class="link">{{ kc.officialGuide }} ↗</a>
          </div>
          <p v-else class="text-sm text-gray-500 dark:text-gray-400">{{ ['gemini', 'agy'].includes(activeApplication) ? kc.geminiNativeOnly : kc.ccsRouting }} <a href="https://ccswitch.io" target="_blank" rel="noopener noreferrer" class="link">{{ kc.downloadCcs }}</a></p>
          <p v-if="['claude-desktop', 'mcode', 'kimi', 'dsh', 'omp'].includes(activeApplication)" class="text-sm text-gray-500 dark:text-gray-400">{{ kc.ccsNewImportVersion }}</p>
          <p v-if="activeApplication === 'kimi'" class="text-sm text-gray-500 dark:text-gray-400">{{ kc.kimiModelNamespace }}</p>
          <p v-if="activeApplication === 'omp'" class="text-sm text-gray-500 dark:text-gray-400">{{ kc.ompKeyLimit }}</p>
          <p v-if="ccsImportFailed" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ t('keys.ccSwitchNotInstalled') }}</p>
        </div>
        <section v-if="activeClientTab === 'dsh'" class="space-y-3" data-testid="dsh-guide">
          <AppHeading as="h3" variant="subsection">DeepSeek Harness · dsh</AppHeading>
          <ol class="list-decimal space-y-2 pl-5 text-sm leading-6">
            <li>{{ kc.dshStart }} <code>npx @deepseek-ai/dsh web</code></li>
            <li>{{ kc.dshProvider }}</li>
            <li>{{ kc.dshConnection }} <code>{{ dshConnectionUrl }}</code> · {{ dshProtocol }}</li>
            <li>{{ kc.dshModels }}</li>
          </ol>
          <a href="https://github.com/deepseek-ai/deepseek-harness/blob/master/docs/user/guide/providers.md" target="_blank" rel="noopener noreferrer" class="link text-sm">{{ kc.officialGuide }} ↗</a>
        </section>

        <details v-else-if="hasManualConfiguration" class="connection-advanced" data-testid="advanced-configuration" :open="advancedOpen" @toggle="advancedOpen = ($event.target as HTMLDetailsElement).open">
          <summary class="cursor-pointer py-3 text-sm font-medium">{{ kc.advancedConfiguration }}</summary>
          <div class="space-y-4 pt-2">
        <fieldset v-if="platform === 'openai' && (activeClientTab === 'codex' || activeClientTab === 'codex-ws')" class="space-y-2" data-testid="codex-transport">
          <legend class="input-label">{{ kc.connectionMethod }}</legend>
          <div class="flex flex-wrap gap-2">
            <button v-for="transport in ['codex', 'codex-ws']" :key="transport" type="button" :aria-pressed="activeClientTab === transport" :class="activeClientTab === transport ? 'btn btn-primary' : 'btn btn-secondary'" @click="activeClientTab = transport">{{ transport === 'codex' ? 'HTTPS' : t('keys.useKeyModal.cliTabs.codexCliWs') }}</button>
          </div>
        </fieldset>

        <!-- Codex Authentication Mode -->
        <div
          v-if="showCodexAuthMode"
          class="rounded-lg border border-gray-200 p-3 dark:border-dark-700"
        >
          <div class="mb-2">
            <p class="text-sm font-medium text-gray-900 dark:text-white">
              {{ t('keys.useKeyModal.openai.authModeTitle') }}
            </p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              {{ t('keys.useKeyModal.openai.authModeDescription') }}
            </p>
          </div>
          <div
            class="grid grid-cols-2 gap-1 rounded-lg bg-gray-100 p-1 dark:bg-dark-700"
            role="radiogroup"
            :aria-label="t('keys.useKeyModal.openai.authModeTitle')"
          >
            <button
              type="button"
              role="radio"
              data-testid="codex-auth-mode-legacy"
              :aria-checked="codexAuthMode === 'legacy'"
              :class="[
                'rounded-md px-3 py-2 text-sm font-medium transition-colors',
                codexAuthMode === 'legacy'
                  ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300'
                  : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
              ]"
              @click="codexAuthMode = 'legacy'"
            >
              {{ t('keys.useKeyModal.openai.authModeLegacy') }}
            </button>
            <button
              type="button"
              role="radio"
              data-testid="codex-auth-mode-api-key"
              :aria-checked="codexAuthMode === 'api-key'"
              :class="[
                'rounded-md px-3 py-2 text-sm font-medium transition-colors',
                codexAuthMode === 'api-key'
                  ? 'bg-white text-primary-700 shadow-sm dark:bg-dark-800 dark:text-primary-300'
                  : 'text-gray-600 hover:text-gray-900 dark:text-dark-300 dark:hover:text-white'
              ]"
              @click="codexAuthMode = 'api-key'"
            >
              {{ t('keys.useKeyModal.openai.authModeApiKey') }}
            </button>
          </div>
          <div
            v-if="codexAuthMode === 'api-key'"
            data-testid="codex-api-key-restart-notice"
            class="mt-3 flex items-start gap-2 border-l-2 border-amber-400 bg-amber-50 px-3 py-2 text-xs leading-5 text-amber-800 dark:border-amber-500 dark:bg-amber-950/30 dark:text-amber-200"
          >
            <Icon name="exclamationCircle" size="sm" class="mt-0.5 flex-shrink-0" />
            <p>{{ t('keys.useKeyModal.openai.authModeApiKeyRestartNotice') }}</p>
          </div>
        </div>

        <!-- OS/Shell Tabs -->
        <div v-if="showShellTabs" class="connection-tabs overflow-x-auto border-b border-gray-200 dark:border-dark-700">
          <nav class="-mb-px flex min-w-max gap-4" :aria-label="kc.operatingSystem">
            <button
              v-for="tab in currentTabs"
              :key="tab.id"
              type="button"
              @click="activeTab = tab.id"
              :class="[
                'whitespace-nowrap py-2.5 px-1 border-b-2 font-medium text-sm transition-colors',
                activeTab === tab.id
                  ? 'border-primary-500 text-primary-600 dark:text-primary-400'
                  : 'border-transparent text-gray-500 hover:text-gray-700 hover:border-gray-300 dark:text-gray-400 dark:hover:text-gray-300'
              ]"
            >
              <span class="flex items-center gap-2">
                <component :is="tab.icon" class="w-4 h-4" />
                {{ tab.label }}
              </span>
            </button>
          </nav>
        </div>

        <!-- Code Blocks (Stacked for multi-file platforms) -->
        <div class="space-y-4">
          <div
            v-for="(file, index) in currentFiles"
            :key="index"
            class="relative"
          >
            <!-- File Hint (if exists) -->
            <p v-if="file.hint" class="text-xs text-amber-600 dark:text-amber-400 mb-1.5 flex items-center gap-1">
              <Icon name="exclamationCircle" size="sm" class="flex-shrink-0" />
              {{ file.hint }}
            </p>
            <div class="bg-gray-900 dark:bg-dark-900 rounded-xl overflow-hidden">
              <!-- Code Header -->
              <div class="flex items-center justify-between px-4 py-2 bg-gray-800 dark:bg-dark-800 border-b border-gray-700 dark:border-dark-700">
                <span class="min-w-0 truncate text-xs text-gray-400 font-mono">{{ file.path }}</span>
                <button
                  type="button"
                  @click="copyContent(file.content, index)"
                  class="flex flex-shrink-0 items-center gap-1.5 px-2.5 py-1 text-xs font-medium rounded-lg transition-colors"
                  :class="copiedIndex === index
                    ? 'bg-green-500/20 text-green-400'
                    : 'bg-gray-700 hover:bg-gray-600 text-gray-300 hover:text-white'"
                >
                  <svg v-if="copiedIndex === index" class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="2">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M5 13l4 4L19 7" />
                  </svg>
                  <svg v-else class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24" stroke-width="1.5">
                    <path stroke-linecap="round" stroke-linejoin="round" d="M15.666 3.888A2.25 2.25 0 0013.5 2.25h-3c-1.03 0-1.9.693-2.166 1.638m7.332 0c.055.194.084.4.084.612v0a.75.75 0 01-.75.75H9a.75.75 0 01-.75-.75v0c0-.212.03-.418.084-.612m7.332 0c.646.049 1.288.11 1.927.184 1.1.128 1.907 1.077 1.907 2.185V19.5a2.25 2.25 0 01-2.25 2.25H6.75A2.25 2.25 0 014.5 19.5V6.257c0-1.108.806-2.057 1.907-2.185a48.208 48.208 0 011.927-.184" />
                  </svg>
                  {{ copiedIndex === index ? t('keys.useKeyModal.copied') : t('keys.useKeyModal.copy') }}
                </button>
              </div>
              <!-- Code Content -->
              <pre class="p-4 text-sm font-mono text-gray-100 overflow-x-auto"><code v-if="file.highlighted" v-html="file.highlighted"></code><code v-else v-text="file.content"></code></pre>
            </div>
          </div>
        </div>

        <section
          v-if="showCodexModelCatalog"
          data-testid="codex-model-catalog"
          class="overflow-hidden rounded-lg border border-gray-200 bg-gray-50 dark:border-dark-700 dark:bg-dark-800/50"
        >
          <div class="flex flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between">
            <div class="min-w-0">
              <AppHeading as="h3" variant="subsection" class="text-gray-900 dark:text-white">
                {{ t('keys.useKeyModal.codexModelCatalog.title') }}
              </AppHeading>
              <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                {{ t('keys.useKeyModal.codexModelCatalog.description') }}
              </p>
              <p class="mt-1 truncate font-mono text-xs text-gray-700 dark:text-gray-300">
                {{ codexModelCatalogMode === 'remote' ? codexModelCatalogUrl : codexModelCatalogPath }}
              </p>
              <label class="block min-w-0">
                <span class="mb-1 block text-2xs text-gray-600 dark:text-gray-300">{{ t('keys.useKeyModal.codexModelCatalog.mode') }}</span>
                <select
                v-model="codexModelCatalogMode"
                data-testid="codex-model-catalog-mode"
                :aria-label="t('keys.useKeyModal.codexModelCatalog.mode')"
                class="input mt-2 text-sm"
              >
                <option value="remote" :disabled="codexModelCatalogOversized">{{ t('keys.useKeyModal.codexModelCatalog.remote') }}</option>
                <option value="file">{{ t('keys.useKeyModal.codexModelCatalog.local') }}</option>
              </select>
              </label>
              <p v-if="codexModelCatalogOversized" class="mt-2 text-xs text-amber-700 dark:text-amber-300">
                {{ t('keys.useKeyModal.codexModelCatalog.oversized') }}
              </p>
            </div>
            <button
              v-if="codexModelManifestState === 'ready'"
              type="button"
              class="btn btn-primary min-h-9 flex-shrink-0 px-3 text-xs"
              @click="downloadCodexModelManifest"
            >
              <Icon name="download" size="sm" class="mr-1.5" />
              {{ t('keys.useKeyModal.codexModelCatalog.download') }}
            </button>
            <button
              v-else
              type="button"
              data-testid="codex-model-catalog-fetch"
              class="btn btn-primary min-h-9 flex-shrink-0 px-3 text-xs"
              :disabled="codexModelManifestState === 'loading' || !apiKey"
              @click="loadCodexModelManifest"
            >
              <Icon
                name="refresh"
                size="sm"
                class="mr-1.5"
                :class="codexModelManifestState === 'loading' ? 'animate-spin' : ''"
              />
              {{ codexModelManifestState === 'error'
                ? t('keys.useKeyModal.codexModelCatalog.retry')
                : t('keys.useKeyModal.codexModelCatalog.fetch') }}
            </button>
          </div>
          <p
            v-if="codexModelManifestState === 'ready'"
            class="border-t border-gray-200 px-4 py-2 text-xs text-emerald-700 dark:border-dark-700 dark:text-emerald-300"
          >
            {{ t('keys.useKeyModal.codexModelCatalog.modelsCount', { count: codexModelManifestModelCount }) }}
          </p>
          <p
            v-else-if="codexModelManifestState === 'error'"
            class="border-t border-red-200 px-4 py-2 text-xs text-red-700 dark:border-red-900 dark:text-red-300"
          >
            {{ t('keys.useKeyModal.codexModelCatalog.errorDescription') }}
          </p>
        </section>

        <!-- Usage Note -->
        <div v-if="showPlatformNote" class="flex items-start gap-3 p-3 rounded-lg theme-info-panel border">
          <Icon name="infoCircle" size="md" class="theme-info-icon flex-shrink-0 mt-0.5" />
          <p class="text-sm">
            {{ platformNote }}
          </p>
        </div>
          </div>
        </details>
        </section>
      </template>
    </div>

    <template #footer>
      <div class="flex flex-wrap justify-end gap-3">
        <slot name="footer-actions" />
        <button
          @click="emit('close')"
          class="btn btn-secondary"
        >
          {{ t('common.close') }}
        </button>
      </div>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import AppHeading from '@/components/common/AppHeading.vue'
import { ref, computed, h, watch, onBeforeUnmount, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { useKeyConnectionCopy } from './keyConnectionCopy'
import CcsAppIcon from './CcsAppIcon.vue'
import { saveAs } from 'file-saver'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import { useClipboard } from '@/composables/useClipboard'
import { buildCodexModelCatalogUrl, fetchCodexModelsManifest } from '@/api/codex'
import type { ApiKey, GroupPlatform, PublicSettings } from '@/types'
import { buildCcSwitchImportDeeplink, getCcSwitchApplicationProfiles, isCcSwitchOmpKeyReference, type CcSwitchApplicationProfile } from '@/utils/ccswitchImport'
import { buildCcSwitchProviderConfig } from '@/utils/ccsProviderConfig'
import { buildCodexImportConfig } from '@/utils/codexImportConfig'
import { parseCcSwitchModels, ccSwitchContextWindow, type CcSwitchImportContext, type CcsModelInfo } from '@/utils/ccsModelSelection'

interface Props {
  show: boolean
  apiKey: string
  baseUrl: string
  platform: GroupPlatform | null
  allowMessagesDispatch?: boolean
  keyRecord?: ApiKey | null
  publicSettings?: PublicSettings | null
}

interface Emits {
  (e: 'close'): void
}

interface TabConfig {
  id: string
  label: string
  icon: Component | string
}

interface FileConfig {
  path: string
  content: string
  hint?: string  // Optional hint message for this file
  highlighted?: string
}

const props = defineProps<Props>()
const emit = defineEmits<Emits>()

const { t } = useI18n()
const kc = useKeyConnectionCopy()
const { copyToClipboard: clipboardCopy } = useClipboard()

const ccsImportFailed = ref(false)
const selectedModels = ref<Record<string, string>>({})
const availableModels = ref<CcsModelInfo[]>([])
const activeApplication = computed(() => activeClientTab.value === 'codex-ws' ? 'codex' : activeClientTab.value)
const importContext = computed<CcSwitchImportContext>(() => ({
  models: availableModels.value,
  selectedModel: selectedModels.value[activeApplication.value],
  allowLongContext: props.keyRecord?.allow_long_context,
  contextTokenLimit: props.keyRecord?.context_token_limit,
  maxReasoningEffort: props.keyRecord?.group?.max_reasoning_effort
}))
const applicationProfiles = computed(() => props.platform
  ? getCcSwitchApplicationProfiles(props.platform, props.publicSettings?.ccs_import_config, importContext.value) : [])
const activeProfiles = computed(() => applicationProfiles.value.filter(profile => profile.app === (activeApplication.value === 'grok' ? 'grokbuild' : activeApplication.value)))
const selectedCcsProfiles = computed(() => !props.keyRecord || props.publicSettings?.hide_ccs_import_button ? [] : activeProfiles.value)
const activeProfile = computed(() => activeProfiles.value[0] ?? (props.platform
  ? getCcSwitchApplicationProfiles(props.platform, undefined, importContext.value).find(profile => profile.app === (activeApplication.value === 'grok' ? 'grokbuild' : activeApplication.value)) : undefined))
const modelOptions = computed(() => activeProfile.value?.modelOptions ?? [])
const selectedModel = computed({
  get: () => String(activeProfile.value?.parameters.model || ''),
  set: (model: string) => { selectedModels.value[activeApplication.value] = model }
})
const nativeClaude = computed(() => activeProfile.value?.nativeClaude === true)
const roleModels = computed(() => activeProfile.value?.roleModels)
const selectedInputModalities = computed(() => availableModels.value.find(model => model.id === selectedModel.value)?.inputModalities ?? [])
const modelSetupReady = computed(() => codexModelManifestState.value === 'ready' && !!activeProfile.value && !activeProfile.value.unavailableReason && modelOptions.value.length > 0)
function unavailableMessage(profile = activeProfile.value): string {
  if (codexModelManifestState.value === 'loading' || codexModelManifestState.value === 'idle') return kc.value.modelsLoading
  if (codexModelManifestState.value === 'error') return kc.value.modelsError
  if (profile?.unavailableReason === 'protocol') return ['gemini', 'agy'].includes(profile.app)
    ? kc.value.geminiNativeOnly : kc.value.protocolUnavailable
  if (profile?.unavailableReason === 'models' || !profile?.modelOptions?.length) return kc.value.modelsUnavailable
  if (profile.app === 'omp' && isCcSwitchOmpKeyReference(props.apiKey)) return kc.value.ompKeyLimit
  return ''
}
const fullProviderConfig = computed(() => modelSetupReady.value && activeProfile.value ? buildCcSwitchProviderConfig({
  profile: activeProfile.value, baseUrl: props.baseUrl || window.location.origin, apiKey: props.apiKey, context: importContext.value
}) : null)
const canImportApplication = (profile: CcSwitchApplicationProfile) =>
  props.show && !!props.apiKey && codexModelManifestState.value === 'ready' && !unavailableMessage(profile)
function connectionDeeplink(profile: CcSwitchApplicationProfile) {
  return buildCcSwitchImportDeeplink({
    baseUrl: props.baseUrl || window.location.origin,
    platform: props.platform,
    profile,
    context: importContext.value,
    settings: props.publicSettings?.ccs_import_config,
    providerName: 'Ritel.SI API',
    apiKey: props.apiKey, keyName: props.keyRecord?.name, userId: props.keyRecord?.user_id
  })
}
const advancedOpen = ref(false)
const dshConnectionUrl = computed(() => {
  const root = (props.baseUrl || window.location.origin).replace(/\/+$/, '').replace(/\/v1(?:beta)?$/, '')
  if (props.platform === 'antigravity') return root.endsWith('/antigravity') ? root : `${root}/antigravity`
  return props.platform === 'anthropic' ? root : `${root}/v1`
})
const dshProtocol = computed(() => {
  const format = activeProfile.value?.parameters.apiFormat
  return format === 'anthropic' ? 'Anthropic Messages' : format === 'openai_responses' ? 'OpenAI Responses'
    : format === 'gemini_native' ? 'Gemini Native' : 'OpenAI Chat Completions'
})
const connectionUrls = computed(() => {
  const root = (props.baseUrl || window.location.origin).replace(/\/+$/, '').replace(/\/v1(?:beta)?$/, '')
  const antigravityRoot = root.endsWith('/antigravity') ? root : `${root}/antigravity`
  const client = activeClientTab.value
  const profile = selectedCcsProfiles.value[0]
  if (profile && canImportApplication(profile)) {
    try {
      return [{ url: new URL(connectionDeeplink(profile)).searchParams.get('endpoint') || root, label: '' }]
    } catch { /* Preserve manual setup when an import profile cannot be built. */ }
  }
  if (client === 'dsh') return [{ url: dshConnectionUrl.value, label: '' }]
  if (client === 'opencode') {
    if (props.platform === 'antigravity') return [
      { url: `${antigravityRoot}/v1`, label: 'Claude' },
      { url: `${root.replace(/\/antigravity$/, '')}/v1`, label: 'Gemini' }
    ]
    return [{ url: `${root}/v1`, label: '' }]
  }
  if (client === 'codex' || client === 'codex-ws' || client === 'grok') return [{ url: `${root}/v1`, label: '' }]
  if (props.platform === 'antigravity') return [{ url: antigravityRoot, label: '' }]
  if (props.platform === 'typesafe') return [{ url: `${root}/v1/systemone`, label: '' }]
  return [{ url: root, label: '' }]
})
function importConnection(profile: CcSwitchApplicationProfile) {
  const record = props.keyRecord
  if (!props.show || !record || !canImportApplication(profile) || !selectedCcsProfiles.value.some(item => item.id === profile.id)) return
  ccsImportFailed.value = false
  try {
    const deeplink = connectionDeeplink(profile)
    window.open(deeplink, '_self')
  } catch {
    ccsImportFailed.value = true
  }
}
watch(() => [props.show, props.keyRecord?.id], () => { ccsImportFailed.value = false; advancedOpen.value = false })

const copiedIndex = ref<number | null>(null)
const activeTab = ref<string>('unix')
const activeClientTab = ref<string>('claude')
type CodexAuthMode = 'legacy' | 'api-key'
const codexAuthMode = ref<CodexAuthMode>('api-key')
type CodexModelManifestState = 'idle' | 'loading' | 'ready' | 'error'
const codexModelManifestState = ref<CodexModelManifestState>('idle')
const codexModelManifestContent = ref('')
const codexModelManifestModelCount = ref(0)
const codexModelCatalogMode = ref<'remote' | 'file'>('remote')
const codexModelManifestResponseBytes = ref(0)
const codexModelCatalogOversized = computed(() => codexModelManifestResponseBytes.value > 1024 * 1024)
const codexModelCatalogUrl = computed(() => buildCodexModelCatalogUrl(props.baseUrl))
const codexLocalCatalogToml = computed(() => codexModelCatalogMode.value === 'file'
  ? `model_catalog_json = "${CODEX_MODEL_CATALOG_CONFIG_PATH}"\n`
  : '')
let codexModelManifestController: AbortController | null = null
let codexModelManifestRequestID = 0

const showCodexModelCatalog = computed(() =>
  props.show &&
  (activeClientTab.value === 'codex' ||
    (props.platform === 'openai' && activeClientTab.value === 'codex-ws'))
)

const codexModelCatalogPath = computed(() => {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.codex' : '~/.codex'
  return joinConfigPath(configDir, 'codex-models.json', isWindows)
})

// Codex expands a leading ~/ on every platform but not %userprofile%, which it
// resolves relative to the config directory, so config.toml always uses ~/.
const CODEX_MODEL_CATALOG_CONFIG_PATH = '~/.codex/codex-models.json'

const codexManifestContext = computed(() => {
  if (!props.show || !props.apiKey || !props.platform || props.platform === 'typesafe') return ''
  return JSON.stringify([props.platform, props.baseUrl, props.apiKey, props.keyRecord?.id])
})

// Reset tabs when platform changes
const defaultClientTab = computed(() => {
  switch (props.platform) {
    case 'openai':
      return 'codex'
    case 'grok':
      return 'grok'
    case 'gemini':
      return 'gemini'
    case 'antigravity':
      return 'claude'
    case 'typesafe':
      return 'systemone'
    default:
      return 'claude'
  }
})

watch(() => props.platform, () => {
  activeTab.value = 'unix'
  activeClientTab.value = defaultClientTab.value
  codexAuthMode.value = 'api-key'
}, { immediate: true })

watch(() => props.show, (show) => {
  if (show) {
    codexAuthMode.value = 'api-key'
  } else {
    resetCodexModelManifest()
  }
})

watch(codexManifestContext, (context, previousContext) => {
  if (context !== previousContext) {
    resetCodexModelManifest()
    selectedModels.value = {}
    codexModelCatalogMode.value = 'remote'
    if (context) void loadCodexModelManifest()
  }
}, { immediate: true })

onBeforeUnmount(resetCodexModelManifest)

// Reset shell tab when client changes
watch(activeClientTab, () => {
  activeTab.value = 'unix'
  advancedOpen.value = false
  ccsImportFailed.value = false
})

// Icon components
const AppleIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M18.71 19.5c-.83 1.24-1.71 2.45-3.05 2.47-1.34.03-1.77-.79-3.29-.79-1.53 0-2 .77-3.27.82-1.31.05-2.3-1.32-3.14-2.53C4.25 17 2.94 12.45 4.7 9.39c.87-1.52 2.43-2.48 4.12-2.51 1.28-.02 2.5.87 3.29.87.78 0 2.26-1.07 3.81-.91.65.03 2.47.26 3.64 1.98-.09.06-2.17 1.28-2.15 3.81.03 3.02 2.65 4.03 2.68 4.04-.03.07-.42 1.44-1.38 2.83M13 3.5c.73-.83 1.94-1.46 2.94-1.5.13 1.17-.34 2.35-1.04 3.19-.69.85-1.83 1.51-2.95 1.42-.15-1.15.41-2.35 1.05-3.11z' })
    ])
  }
}

const WindowsIcon = {
  render() {
    return h('svg', {
      fill: 'currentColor',
      viewBox: '0 0 24 24',
      class: 'w-4 h-4'
    }, [
      h('path', { d: 'M3 12V6.75l6-1.32v6.48L3 12zm17-9v8.75l-10 .15V5.21L20 3zM3 13l6 .09v6.81l-6-1.15V13zm7 .25l10 .15V21l-10-1.91v-5.84z' })
    ])
  }
}

// Terminal icon for Claude Code
const TerminalIcon = {
  render() {
    return h('svg', {
      fill: 'none',
      stroke: 'currentColor',
      viewBox: '0 0 24 24',
      'stroke-width': '1.5',
      class: 'w-4 h-4'
    }, [
      h('path', {
        'stroke-linecap': 'round',
        'stroke-linejoin': 'round',
        d: 'm6.75 7.5 3 2.25-3 2.25m4.5 0h3m-9 8.25h13.5A2.25 2.25 0 0 0 21 17.25V6.75A2.25 2.25 0 0 0 18.75 4.5H5.25A2.25 2.25 0 0 0 3 6.75v10.5A2.25 2.25 0 0 0 5.25 20.25Z'
      })
    ])
  }
}

const clientTabs = computed((): TabConfig[] => {
  if (!props.platform) return []
  switch (props.platform) {
    case 'openai': {
      const tabs: TabConfig[] = [
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: 'codex' },
        { id: 'codex-ws', label: t('keys.useKeyModal.cliTabs.codexCliWs'), icon: 'codex-ws' },
      ]
      if (props.allowMessagesDispatch) {
        tabs.push({ id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: 'claude' })
      }
      tabs.push({ id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: 'opencode' })
      return tabs
    }
    case 'gemini':
      return [
        { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: 'gemini' },
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: 'codex' },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: 'opencode' }
      ]
    case 'antigravity':
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: 'claude' },
        { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: 'gemini' },
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: 'codex' },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: 'opencode' }
      ]
    case 'grok':
      return [
        { id: 'grok', label: t('keys.useKeyModal.cliTabs.grokCli'), icon: 'grok' },
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: 'claude' },
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: 'codex' },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: 'opencode' }
      ]
    case 'typesafe':
      return [
        { id: 'systemone', label: t('keys.useKeyModal.cliTabs.systemOne'), icon: TerminalIcon }
      ]
    case 'deepseek':
    case 'minimax':
    case 'composite':
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: 'claude' },
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: 'codex' },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: 'opencode' }
      ]
    default:
      return [
        { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: 'claude' },
        { id: 'codex', label: t('keys.useKeyModal.cliTabs.codexCli'), icon: 'codex' },
        { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: 'opencode' }
      ]
  }
})

// Shell tabs (3 types for environment variable based configs)
const shellTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'cmd', label: 'Windows CMD', icon: WindowsIcon },
  { id: 'powershell', label: 'PowerShell', icon: WindowsIcon }
]

// OpenAI tabs (2 OS types)
const openaiTabs: TabConfig[] = [
  { id: 'unix', label: 'macOS / Linux', icon: AppleIcon },
  { id: 'windows', label: 'Windows', icon: WindowsIcon }
]

const applicationTabs = computed(() => props.platform === 'typesafe' ? clientTabs.value : [
  { id: 'codex', label: kc.value.codexSharedApp, icon: 'codex' },
  { id: 'claude', label: t('keys.useKeyModal.cliTabs.claudeCode'), icon: 'claude' },
  { id: 'claude-desktop', label: 'Claude Desktop', icon: 'claude-desktop' },
  { id: 'opencode', label: t('keys.useKeyModal.cliTabs.opencode'), icon: 'opencode' },
  { id: 'openclaw', label: 'OpenClaw', icon: 'openclaw' },
  { id: 'gemini', label: t('keys.useKeyModal.cliTabs.geminiCli'), icon: 'gemini' },
  { id: 'agy', label: 'Antigravity', icon: 'agy' },
  { id: 'grok', label: 'Grok Build', icon: 'grok' },
  { id: 'hermes', label: 'Hermes', icon: 'hermes' },
  { id: 'pi', label: 'Pi', icon: 'pi' },
  { id: 'mcode', label: 'MiniMax Code', icon: 'mcode' },
  { id: 'kimi', label: 'Kimi Code', icon: 'kimi' },
  { id: 'dsh', label: 'DeepSeek Harness', icon: 'dsh' },
  { id: 'omp', label: 'Oh My Pi', icon: 'omp' }
])
const hasManualConfiguration = computed(() => clientTabs.value.some(tab => tab.id === activeClientTab.value))
function onApplicationTabKeydown(event: KeyboardEvent, current: string) {
  const tabs = applicationTabs.value
  const index = tabs.findIndex(tab => tab.id === current)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? tabs.length - 1
    : event.key === 'ArrowRight' ? (index + 1) % tabs.length
      : event.key === 'ArrowLeft' ? (index - 1 + tabs.length) % tabs.length : -1
  if (next < 0) return
  event.preventDefault()
  activeClientTab.value = tabs[next].id
  document.getElementById(`connection-tab-${tabs[next].id}`)?.focus()
}

const showShellTabs = computed(() => activeClientTab.value !== 'opencode')

const showCodexAuthMode = computed(() =>
  props.platform === 'openai' &&
  (activeClientTab.value === 'codex' || activeClientTab.value === 'codex-ws')
)

const currentTabs = computed(() => {
  if (!showShellTabs.value) return []
  if (activeClientTab.value === 'codex' || activeClientTab.value === 'codex-ws' || activeClientTab.value === 'grok') {
    return openaiTabs
  }
  return shellTabs
})



const platformNote = computed(() => {
  if (activeClientTab.value === 'codex' &&
    props.platform !== 'openai' &&
    props.platform !== 'grok' &&
    props.platform !== 'deepseek' &&
    props.platform !== 'minimax' &&
    props.platform !== 'composite') {
    return t('keys.useKeyModal.routedCodex.note')
  }
  switch (props.platform) {
    case 'openai':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.note')
      }
      return activeTab.value === 'windows'
        ? t('keys.useKeyModal.openai.noteWindows')
        : t('keys.useKeyModal.openai.note')
    case 'gemini':
      return t('keys.useKeyModal.gemini.note')
    case 'antigravity':
      return activeClientTab.value === 'claude'
        ? t('keys.useKeyModal.antigravity.claudeNote')
        : t('keys.useKeyModal.antigravity.geminiNote')
    case 'grok':
      if (activeClientTab.value === 'claude') {
        return t('keys.useKeyModal.grok.claudeNote')
      }
      if (activeClientTab.value === 'codex') {
        return activeTab.value === 'windows'
          ? t('keys.useKeyModal.grok.codexNoteWindows')
          : t('keys.useKeyModal.grok.codexNote')
      }
      // Grok CLI: shell-specific path guidance (env + ~/.grok/config.toml).
      if (activeClientTab.value === 'grok' && (activeTab.value === 'cmd' || activeTab.value === 'powershell')) {
        return t('keys.useKeyModal.grok.noteWindows')
      }
      if (activeClientTab.value === 'grok' && activeTab.value === 'windows') {
        return t('keys.useKeyModal.grok.noteWindows')
      }
      return t('keys.useKeyModal.grok.note')
    case 'deepseek':
      return activeClientTab.value === 'codex'
        ? t('keys.useKeyModal.deepseek.codexNote')
        : t('keys.useKeyModal.note')
    case 'minimax':
      return activeClientTab.value === 'codex'
        ? t('keys.useKeyModal.minimax.codexNote')
        : t('keys.useKeyModal.note')
    case 'composite':
      return activeClientTab.value === 'codex'
        ? t('keys.useKeyModal.composite.codexNote')
        : t('keys.useKeyModal.note')
    case 'typesafe':
      return t('keys.useKeyModal.typesafe.note')
    default:
      return t('keys.useKeyModal.note')
  }
})

const showPlatformNote = computed(() => activeClientTab.value !== 'opencode')

function resetCodexModelManifest() {
  codexModelManifestController?.abort()
  codexModelManifestController = null
  codexModelManifestRequestID += 1
  codexModelManifestState.value = 'idle'
  codexModelManifestContent.value = ''
  availableModels.value = []
  codexModelManifestModelCount.value = 0
  codexModelManifestResponseBytes.value = 0
}

async function loadCodexModelManifest() {
  if (!codexManifestContext.value) return

  const requestContext = codexManifestContext.value
  codexModelManifestController?.abort()
  const controller = new AbortController()
  const requestID = ++codexModelManifestRequestID
  codexModelManifestController = controller
  codexModelManifestState.value = 'loading'

  try {
    const result = await fetchCodexModelsManifest(props.baseUrl, props.apiKey, controller.signal)
    if (requestID !== codexModelManifestRequestID || requestContext !== codexManifestContext.value) return
    const models = parseCcSwitchModels(result.content)
    availableModels.value = models
    codexModelManifestContent.value = result.content
    codexModelManifestModelCount.value = result.modelCount
    codexModelManifestResponseBytes.value = result.responseBytes
    if (codexModelCatalogOversized.value) codexModelCatalogMode.value = 'file'
    codexModelManifestState.value = 'ready'
  } catch (error) {
    const errorName = error && typeof error === 'object' && 'name' in error
      ? String((error as { name?: unknown }).name || '')
      : ''
    if (requestID !== codexModelManifestRequestID || requestContext !== codexManifestContext.value || errorName === 'AbortError') return
    codexModelManifestState.value = 'error'
  } finally {
    if (requestID === codexModelManifestRequestID) {
      codexModelManifestController = null
    }
  }
}

function downloadCodexModelManifest() {
  if (!codexModelManifestContent.value) return
  saveAs(
    new Blob([codexModelManifestContent.value], { type: 'application/json;charset=utf-8' }),
    'codex-models.json'
  )
}

const escapeHtml = (value: string) => value
  .replace(/&/g, '&amp;')
  .replace(/</g, '&lt;')
  .replace(/>/g, '&gt;')
  .replace(/"/g, '&quot;')
  .replace(/'/g, '&#39;')

const wrapToken = (className: string, value: string) =>
  `<span class="${className}">${escapeHtml(value)}</span>`

const keyword = (value: string) => wrapToken('text-emerald-300', value)
const variable = (value: string) => wrapToken('text-sky-200', value)
const operator = (value: string) => wrapToken('text-slate-400', value)
const string = (value: string) => wrapToken('text-amber-200', value)
const comment = (value: string) => wrapToken('text-slate-500', value)

// Syntax highlighting helpers
// Generate file configs based on platform and active tab
const currentFiles = computed((): FileConfig[] => {
  if (props.platform !== 'typesafe' && !modelSetupReady.value) return []
  const root = (props.baseUrl || window.location.origin).replace(/\/+$/, '').replace(/\/v1(?:beta)?$/, '')
  const endpoint = connectionUrls.value[0]?.url || root
  const apiKey = props.apiKey
  if (props.platform === 'typesafe') return [generateSystemOneCurl(root, apiKey)]
  if (activeApplication.value === 'opencode') return [generateOpenCodeConfig(), ritelEnvironmentFile(apiKey)]
  if (activeApplication.value === 'codex') return generateCodexFiles(endpoint, apiKey)
  if (activeApplication.value === 'claude') return generateAnthropicFiles(endpoint, apiKey)
  if (activeApplication.value === 'gemini') return [generateGeminiCliContent(endpoint, apiKey)]
  if (activeApplication.value === 'grok') return generateGrokFiles(endpoint, apiKey)
  return []
})

function generateSystemOneCurl(baseUrl: string, apiKey: string): FileConfig {
  const endpoint = `${baseUrl}/v1/systemone`
  const payload = `{
  "model": "jev-latest",
  "state": "Text to evaluate",
  "questions": {
    "safety": {
      "type": "noul",
      "instructions": "Evaluate whether the text is unsafe"
    }
  }
}`
  if (activeTab.value === 'powershell') {
    return {
      path: 'PowerShell',
      content: `$headers = @{ Authorization = "Bearer ${apiKey}" }
$body = @'
${payload}
'@
Invoke-RestMethod -Method Post -Uri "${endpoint}" -Headers $headers -ContentType "application/json" -Body $body`
    }
  }
  if (activeTab.value === 'cmd') {
    return {
      path: 'Command Prompt',
      content: `curl -X POST "${endpoint}" ^
  -H "Authorization: Bearer ${apiKey}" ^
  -H "Content-Type: application/json" ^
  --data "{\"model\":\"jev-latest\",\"state\":\"Text to evaluate\",\"questions\":{\"safety\":{\"type\":\"noul\",\"instructions\":\"Evaluate whether the text is unsafe\"}}}"`
    }
  }
  return {
    path: 'Terminal',
    content: `curl -X POST "${endpoint}" \\
  -H "Authorization: Bearer ${apiKey}" \\
  -H "Content-Type: application/json" \\
  --data '${payload}'`
  }
}

function generateAnthropicFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const environment: Record<string, string> = {
    ANTHROPIC_BASE_URL: baseUrl,
    ANTHROPIC_AUTH_TOKEN: apiKey,
    CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC: '1'
  }
  if (!nativeClaude.value) {
    const mark = (model: string) => (ccSwitchContextWindow(model, importContext.value) ?? 0) >= 1000000 ? `${model}[1M]` : model
    environment.ANTHROPIC_MODEL = mark(selectedModel.value)
    for (const [role, model] of Object.entries(roleModels.value ?? {})) {
      environment[`ANTHROPIC_DEFAULT_${role.toUpperCase()}_MODEL`] = mark(model)
    }
  }
  if (activeProfile.value?.reasoningEffort) {
    environment.CLAUDE_CODE_EFFORT_LEVEL = activeProfile.value.reasoningEffort
  }
  const shell = activeTab.value
  const content = Object.entries(environment).map(([name, value]) => {
    const literal = name === 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC' ? value : `"${value}"`
    if (shell === 'powershell') return `$env:${name}=${literal}`
    if (shell === 'cmd') return `set ${name}=${value}`
    return `export ${name}=${literal}`
  }).join('\n')
  return [
    { path: shell === 'powershell' ? 'PowerShell' : shell === 'cmd' ? 'Command Prompt' : 'Terminal', content },
    {
      path: shell === 'unix' ? '~/.claude/settings.json' : '%USERPROFILE%\\.claude\\settings.json',
      content: JSON.stringify({ $schema: 'https://json.schemastore.org/claude-code-settings.json', env: environment }, null, 2),
      hint: t('keys.useKeyModal.claudeSettingsHint')
    }
  ]
}

function generateGeminiCliContent(baseUrl: string, apiKey: string): FileConfig {
  const model = selectedModel.value
  const modelComment = t('keys.useKeyModal.gemini.modelComment')
  let path: string
  let content: string
  let highlighted: string

  switch (activeTab.value) {
    case 'unix':
      path = 'Terminal'
      content = `export GOOGLE_GEMINI_BASE_URL="${baseUrl}"
export GEMINI_API_KEY="${apiKey}"
export GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('export')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('export')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('export')} ${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    case 'cmd':
      path = 'Command Prompt'
      content = `set GOOGLE_GEMINI_BASE_URL=${baseUrl}
set GEMINI_API_KEY=${apiKey}
set GEMINI_MODEL=${model}`
      highlighted = `${keyword('set')} ${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(baseUrl)}
${keyword('set')} ${variable('GEMINI_API_KEY')}${operator('=')}${string(apiKey)}
${keyword('set')} ${variable('GEMINI_MODEL')}${operator('=')}${string(model)}
${comment(`REM ${modelComment}`)}`
      break
    case 'powershell':
      path = 'PowerShell'
      content = `$env:GOOGLE_GEMINI_BASE_URL="${baseUrl}"
$env:GEMINI_API_KEY="${apiKey}"
$env:GEMINI_MODEL="${model}"  # ${modelComment}`
      highlighted = `${keyword('$env:')}${variable('GOOGLE_GEMINI_BASE_URL')}${operator('=')}${string(`"${baseUrl}"`)}
${keyword('$env:')}${variable('GEMINI_API_KEY')}${operator('=')}${string(`"${apiKey}"`)}
${keyword('$env:')}${variable('GEMINI_MODEL')}${operator('=')}${string(`"${model}"`)}  ${comment(`# ${modelComment}`)}`
      break
    default:
      path = 'Terminal'
      content = ''
      highlighted = ''
  }

  return { path, content, highlighted }
}

function generateCodexFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.codex' : '~/.codex'
  const model = selectedModel.value
  const useApiKey = props.platform !== 'openai' || codexAuthMode.value === 'api-key'
  const websocket = activeClientTab.value === 'codex-ws'
  let config = buildCodexImportConfig({
    apiKey, endpoint: baseUrl, model,
    contextWindow: activeProfile.value?.contextWindow,
    reasoningEffort: activeProfile.value?.reasoningEffort ?? null,
    modelCatalogUrl: codexModelCatalogMode.value === 'remote' ? buildCodexModelCatalogUrl(baseUrl) : undefined
  }).config
  config = config.replace('\n[model_providers.custom]', `\nreview_model = "${escapeTomlBasicString(model)}"\n${codexLocalCatalogToml.value}\n[model_providers.custom]`)
  const auth = useApiKey ? generateCodexProviderAuthConfig() : 'requires_openai_auth = true'
  config = config.replace('requires_openai_auth = true', `supports_websockets = ${websocket}\n${auth}`)
  if (props.platform === 'openai') {
    if (!config.includes('[features]')) config += '\n[features]\n'
    if (websocket) config += 'responses_websockets_v2 = true\n'
    config += 'goals = true\n'
  }
  const files: FileConfig[] = [{
    path: joinConfigPath(configDir, 'config.toml', isWindows), content: config,
    hint: t('keys.useKeyModal.openai.configTomlHint')
  }]
  if (useApiKey) files.unshift(ritelEnvironmentFile(apiKey))
  else files.push({ path: joinConfigPath(configDir, 'auth.json', isWindows), content: JSON.stringify({ OPENAI_API_KEY: apiKey }, null, 2) })
  return files
}

function generateCodexProviderAuthConfig(): string {
  return `requires_openai_auth = false
env_key = "RITEL_API_KEY"${props.platform === 'openai' ? '\nhttp_headers = { "x-openai-actor-authorization" = "local-image-extension" }' : ''}`
}

function ritelEnvironmentFile(apiKey: string): FileConfig {
  return { path: activeTab.value === 'windows' ? 'PowerShell' : 'Terminal', content: activeTab.value === 'windows' ? `$env:RITEL_API_KEY="${apiKey}"` : `export RITEL_API_KEY="${apiKey}"`, hint: t('consoleExperience.legacyConfig') }
}

function joinConfigPath(dir: string, file: string, windows: boolean): string {
  if (!windows) return `${dir}/${file}`
  return `${dir}\\${file}`
}

function escapeTomlBasicString(value: string): string {
  return value.replace(/\\/g, '\\\\').replace(/"/g, '\\"')
}

function generateGrokFiles(baseUrl: string, apiKey: string): FileConfig[] {
  const isWindows = activeTab.value === 'windows'
  const configDir = isWindows ? '%userprofile%\\.grok' : '~/.grok'
  // Grok Build's custom-model contract names the three native API backends.
  const format = activeProfile.value?.parameters.apiFormat
  const backend = format === 'openai_chat' ? 'chat_completions' : format === 'anthropic' ? 'messages' : 'responses'
  const effort = activeProfile.value?.reasoningEffort
  const modelBlocks = modelOptions.value.map(model => {
    const window = ccSwitchContextWindow(model, importContext.value)
    return `[model."${escapeTomlBasicString(model)}"]
model = "${escapeTomlBasicString(model)}"
name = "${escapeTomlBasicString(model)}"
env_key = "XAI_API_KEY"
api_backend = "${backend}"${window ? `\ncontext_window = ${window}` : ''}`
  }).join('\n\n')
  return [
    {
      path: isWindows ? 'PowerShell' : 'Terminal',
      content: isWindows ? `$env:GROK_MODELS_BASE_URL="${baseUrl}"\n$env:XAI_API_KEY="${apiKey}"` : `export GROK_MODELS_BASE_URL="${baseUrl}"\nexport XAI_API_KEY="${apiKey}"`
    },
    {
      path: joinConfigPath(configDir, 'config.toml', isWindows),
      content: `[endpoints]
models_base_url = "${baseUrl}"
models_list_url = "${baseUrl.replace(/\/+$/, '')}/models"
xai_api_base_url = "${baseUrl}"
cli_chat_proxy_base_url = "${baseUrl}"

[auth]
preferred_method = "api_key"

${modelBlocks}

[models]
default = "${escapeTomlBasicString(selectedModel.value)}"${effort ? `\ndefault_reasoning_effort = "${effort}"` : ''}`,
      hint: t('keys.useKeyModal.grok.configTomlHint')
    }
  ]
}

function generateOpenCodeConfig(): FileConfig {
  // The editor helper owns the same filtered catalog and capabilities as the import.
  const provider = JSON.parse(fullProviderConfig.value!.content)
  provider.name = 'Ritel.SI API'
  provider.options.apiKey = '{env:RITEL_API_KEY}'
  return {
    path: 'opencode.json',
    content: JSON.stringify({
      $schema: 'https://opencode.ai/config.json',
      model: `ritel/${selectedModel.value}`,
      provider: { ritel: provider }
    }, null, 2),
    hint: t('keys.useKeyModal.opencode.hint')
  }
}

const copyContent = async (content: string, index: number) => {
  const success = await clipboardCopy(content, t('keys.copied'))
  if (success) {
    copiedIndex.value = index
    setTimeout(() => {
      copiedIndex.value = null
    }, 2000)
  }
}
</script>
