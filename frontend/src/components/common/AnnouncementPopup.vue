<template>
  <BaseDialog
    :show="!!displayedAnnouncement"
    :title="displayedAnnouncement?.title || t('announcements.title')"
    width="wide"
    :z-index="120"
    content-class="announcement-popup"
    @close="handleDismiss"
  >
    <template v-if="displayedAnnouncement">
      <div class="mb-4 flex flex-wrap items-center gap-3 text-sm text-gray-500 dark:text-gray-400">
        <span v-if="!preview">{{ t('announcements.unread') }}</span>
        <time>{{ formatRelativeWithDateTime(displayedAnnouncement.created_at) }}</time>
      </div>
      <div class="markdown-body prose prose-sm max-w-none dark:prose-invert" v-html="renderedContent"></div>
    </template>
    <template #footer>
      <button type="button" @click="handleDismiss" data-testid="announcement-popup-dismiss" class="btn btn-primary">
        {{ preview ? t('common.close') : t('announcements.markRead') }}
      </button>
    </template>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import BaseDialog from './BaseDialog.vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useAnnouncementStore } from '@/stores/announcements'
import { formatRelativeWithDateTime } from '@/utils/format'
import type { Announcement, UserAnnouncement } from '@/types'
import '@/styles/announcement-markdown.css'

type PreviewAnnouncement = Pick<Announcement | UserAnnouncement, 'title' | 'content' | 'created_at'>

const props = withDefaults(defineProps<{
  announcement?: PreviewAnnouncement | null
  preview?: boolean
}>(), {
  announcement: null,
  preview: false,
})

const emit = defineEmits<{
  close: []
}>()

const { t } = useI18n()
const announcementStore = useAnnouncementStore()
const displayedAnnouncement = computed(() => (
  props.preview ? props.announcement : announcementStore.currentPopup
))

marked.setOptions({
  breaks: true,
  gfm: true,
})

const renderedContent = computed(() => {
  const content = displayedAnnouncement.value?.content
  if (!content) return ''
  const html = marked.parse(content) as string
  return DOMPurify.sanitize(html)
})

function handleDismiss() {
  if (props.preview) {
    emit('close')
    return
  }
  announcementStore.dismissPopup()
}

</script>

<style>
/* Keep both dismissal actions outside the scrolling announcement content. */
.modal-content.announcement-popup {
  max-height: calc(100vh - 2rem);
  max-height: min(48rem, calc(100dvh - 2rem - env(safe-area-inset-top) - env(safe-area-inset-bottom)));
  min-height: 0;
  overflow: hidden;
}
.announcement-popup .modal-header { gap: 0.75rem; }
.announcement-popup .modal-title {
  min-width: 0;
  max-height: 4.2em;
  line-height: 1.4;
  overflow-y: auto;
  overflow-wrap: anywhere;
}
.announcement-popup .modal-header > button {
  flex-shrink: 0;
  min-width: 44px;
  min-height: 44px;
}
.announcement-popup .modal-body {
  min-height: 0;
  min-width: 0;
  overscroll-behavior: contain;
  overflow-wrap: anywhere;
}
</style>
