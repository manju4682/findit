<script setup>
// A thin vertical divider that resizes the panel next to it. Drag to stretch,
// double-click to reset. It emits incremental horizontal deltas so the parent
// can apply and clamp them however it likes.
const emit = defineEmits(['resize', 'reset'])

let prevX = 0
let dragging = false

function onMove(e) {
  if (!dragging) return
  emit('resize', e.clientX - prevX)
  prevX = e.clientX
}

function stop() {
  dragging = false
  document.body.style.cursor = ''
  document.body.style.userSelect = ''
  window.removeEventListener('mousemove', onMove)
  window.removeEventListener('mouseup', stop)
}

function onDown(e) {
  e.preventDefault()
  dragging = true
  prevX = e.clientX
  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
  window.addEventListener('mousemove', onMove)
  window.addEventListener('mouseup', stop)
}
</script>

<template>
  <div
    class="group relative w-px shrink-0 cursor-col-resize bg-slate-200 hover:bg-blue-400 transition-colors"
    title="Drag to resize · double-click to reset"
    @mousedown="onDown"
    @dblclick="emit('reset')"
  >
    <!-- widened invisible hit area so the 1px divider is easy to grab -->
    <div class="absolute inset-y-0 -left-1.5 -right-1.5"></div>
    <!-- grip that fades in on hover -->
    <div
      class="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 opacity-0 group-hover:opacity-100 transition
             flex items-center justify-center h-9 w-4 rounded-full bg-white border border-slate-200 shadow-sm
             text-[11px] leading-none text-slate-400 group-hover:text-blue-500 pointer-events-none"
    >⟺</div>
  </div>
</template>
