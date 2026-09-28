<script setup>
import { ref } from 'vue'

defineProps({ text: { type: String, required: true } })

const show = ref(false)
const x = ref(0)
const y = ref(0)
const below = ref(false)

function enter(e) {
  const r = e.currentTarget.getBoundingClientRect()
  x.value = Math.min(Math.max(r.left + r.width / 2, 130), window.innerWidth - 130)
  if (r.top < 130) {
    below.value = true
    y.value = r.bottom + 8
  } else {
    below.value = false
    y.value = r.top - 8
  }
  show.value = true
}
function leave() {
  show.value = false
}
</script>

<template>
  <span class="relative inline-flex align-middle" @mouseenter="enter" @mouseleave="leave">
    <slot>
      <span
        class="w-4 h-4 rounded-full border border-slate-300 text-slate-500 text-[11px] font-serif italic flex items-center justify-center cursor-help select-none hover:border-slate-400 hover:text-slate-700 leading-none"
      >i</span>
    </slot>
    <Teleport to="body">
      <div
        v-if="show"
        class="fixed z-[9999] w-56 rounded-lg bg-slate-800 text-white text-xs leading-snug px-2.5 py-1.5 shadow-xl pointer-events-none -translate-x-1/2"
        :class="below ? '' : '-translate-y-full'"
        :style="{ left: x + 'px', top: y + 'px' }"
      >
        {{ text }}
      </div>
    </Teleport>
  </span>
</template>
