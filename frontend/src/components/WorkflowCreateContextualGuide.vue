<template>
  <SpotlightGuide v-model:active="active" :steps="guideSteps" step-i18n-prefix="contextualGuide.workflowCreate.steps"
    labels-prefix="contextualGuide" @finish="onFinish" @dismiss="onFinish" />
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import SpotlightGuide from '@/components/SpotlightGuide.vue'
import { isContextualGuideDone, isGlobalUserGuideDone, markContextualGuideDone } from '@/config/contextualGuides'
import type { SpotlightGuideStep } from '@/types/spotlightGuide'

/**
 * 工作流编排的首次引导。
 *
 * 工作流是这个产品里最"工程师思维"的功能，非技术用户第一眼看到空白画布时
 * 既不知道要做什么，也不知道从哪下手。这里按"选模板 → 认画布 → 认步骤 →
 * 认配置 → 试跑"的顺序走一遍，每一步都尽量指向屏幕上真实存在的东西。
 */
const props = defineProps<{
  when: boolean
  /** 画布仍是空白默认流程时，模板面板会占据画布位置。 */
  hasTemplateGallery: boolean
}>()

const active = ref(false)

const guideSteps = computed<SpotlightGuideStep[]>(() => {
  const steps: SpotlightGuideStep[] = []

  if (props.hasTemplateGallery) {
    steps.push({
      key: 'templates',
      target: '[data-guide="workflow-templates"]',
      placement: 'top',
    })
    steps.push({
      key: 'canvas',
      target: '[data-guide="workflow-canvas"]',
      placement: 'top',
      optional: true,
    })
  } else {
    steps.push({
      key: 'canvas',
      target: '[data-guide="workflow-canvas"]',
      placement: 'top',
    })
  }

  steps.push({
    key: 'palette',
    target: '[data-guide="workflow-palette"]',
    placement: 'right',
  })
  steps.push({
    key: 'inspector',
    target: '[data-guide="workflow-inspector"]',
    placement: 'left',
  })

  return steps
})

let openTimer: ReturnType<typeof setTimeout> | null = null
let waitGlobalTimer: ReturnType<typeof setTimeout> | null = null

const onFinish = () => {
  markContextualGuideDone('workflowCreate')
}

const clearTimers = () => {
  if (openTimer) {
    clearTimeout(openTimer)
    openTimer = null
  }
  if (waitGlobalTimer) {
    clearTimeout(waitGlobalTimer)
    waitGlobalTimer = null
  }
}

const tryOpen = () => {
  if (active.value || !props.when || isContextualGuideDone('workflowCreate')) return
  openTimer = setTimeout(() => {
    if (!props.when || isContextualGuideDone('workflowCreate') || active.value) return
    active.value = true
  }, 600)
}

const scheduleOpen = () => {
  clearTimers()
  if (!props.when || isContextualGuideDone('workflowCreate')) return

  if (isGlobalUserGuideDone()) {
    tryOpen()
    return
  }

  // 等全局新手引导结束再展示，避免两层遮罩叠加
  const poll = () => {
    if (!props.when || isContextualGuideDone('workflowCreate')) {
      clearTimers()
      return
    }
    if (isGlobalUserGuideDone()) {
      waitGlobalTimer = null
      tryOpen()
      return
    }
    waitGlobalTimer = setTimeout(poll, 400)
  }
  waitGlobalTimer = setTimeout(poll, 400)
}

watch(
  () => props.when,
  (val) => {
    if (val) {
      scheduleOpen()
      return
    }
    clearTimers()
    active.value = false
  },
  { immediate: true },
)

onBeforeUnmount(clearTimers)
</script>
