<script setup>
import { ref } from 'vue'
import { childFolders, openFolder, store } from '../store'
import { folderLabel } from '../utils'

defineOptions({ name: 'FolderTree' })

const props = defineProps({
  node: { type: Object, required: true },
  label: { type: String, default: '' },
  depth: { type: Number, default: 0 },
})

const open = ref(props.depth < 1)
const folders = () => childFolders(props.node)
const isCurrent = () => store.currentFolder === props.node
</script>

<template>
  <div>
    <div
      class="flex items-center gap-1 py-1 px-2 rounded-md cursor-pointer text-sm"
      :class="isCurrent() ? 'bg-blue-500/10 text-blue-700 font-medium' : 'hover:bg-slate-200/60 text-slate-700'"
      :style="{ paddingLeft: depth * 12 + 6 + 'px' }"
      @click="openFolder(node)"
    >
      <button
        class="w-3 text-slate-400 shrink-0"
        @click.stop="open = !open"
        v-if="folders().length"
      >
        {{ open ? '▾' : '▸' }}
      </button>
      <span v-else class="w-3 shrink-0"></span>
      <span class="shrink-0">{{ isCurrent() ? '📂' : '📁' }}</span>
      <span class="truncate">{{ label || folderLabel(node.name) }}</span>
    </div>
    <div v-show="open">
      <FolderTree v-for="(f, i) in folders()" :key="i" :node="f" :depth="depth + 1" />
    </div>
  </div>
</template>
