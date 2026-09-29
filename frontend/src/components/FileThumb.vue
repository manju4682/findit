<script setup>
import { ref, onMounted, onBeforeUnmount } from 'vue'
import { store, loadPreview } from '../store'
import { iconFor, previewable } from '../utils'

const props = defineProps({ file: { type: Object, required: true } })

const el = ref(null)
let observer

onMounted(() => {
  if (!previewable(props.file.ext)) return
  observer = new IntersectionObserver(
    (entries) => {
      if (entries[0].isIntersecting) {
        loadPreview(props.file)
        observer.disconnect()
      }
    },
    { rootMargin: '300px' }, // start loading just before the thumbnail scrolls into view
  )
  observer.observe(el.value)
})
onBeforeUnmount(() => observer && observer.disconnect())

const thumb = () => store.previews[props.file.id]?.thumbnailDataUrl
</script>

<template>
  <div
    ref="el"
    class="h-24 w-24 flex items-center justify-center overflow-hidden rounded-lg bg-white ring-1 ring-slate-100"
  >
    <img v-if="thumb()" :src="thumb()" class="max-h-24 max-w-full object-contain" />
    <span v-else class="text-5xl">{{ iconFor(file.ext) }}</span>
  </div>
</template>
