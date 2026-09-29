<script setup>
import { computed, reactive, ref, watch, nextTick, onMounted, onBeforeUnmount } from 'vue'
import FolderTree from './FolderTree.vue'
import Hint from './Hint.vue'
import FileThumb from './FileThumb.vue'
import Resizer from './Resizer.vue'
import {
  store,
  activeSource,
  currentFolders,
  currentFilesFiltered,
  openFolder,
  goBreadcrumb,
  back,
  forward,
  goUp,
  canBack,
  canForward,
  canUp,
  toggleFile,
  isSelected,
  selectedIds,
  selectAllCurrent,
  clearSelection,
  openFile,
  chooseDest,
  doRecover,
  openDest,
  dismissRecover,
} from '../store'
import {
  folderLabel,
  statusInfo,
  statusList,
  sourceSummary,
  sourceLabel,
  recoverConfidence,
  fmtSize,
  iconFor,
} from '../utils'

const src = computed(() => activeSource())
const folders = computed(() => currentFolders())
const files = computed(() => currentFilesFiltered())
const summary = computed(() => (src.value ? sourceSummary(src.value) : { total: 0, recoverable: 0, nameOnly: 0 }))
const conf = computed(() => (src.value ? recoverConfidence(src.value) : null))
const selCount = computed(() => selectedIds().length)

// Windowed rendering: a raw source can hold 100k+ files, so only the rows near
// the viewport are mounted. Rows have fixed heights (ROW_H) so positions can be
// computed; spacers above and below keep the scrollbar honest.
const ROW_H = { grid: 164, list: 34, details: 32 } // px, including the gap
const TILE_MIN = 128 // matches minmax(128px,1fr) in the grid template
const GAP = 8 // gap-2
const OVERSCAN = 3 // extra rows above/below the viewport

const scroller = ref(null)
const filesEl = ref(null)
const view = reactive({ top: 0, height: 0, width: 0, filesTop: 0 })

function measure() {
  const s = scroller.value
  if (!s) return
  view.top = s.scrollTop
  view.height = s.clientHeight
  const f = filesEl.value
  if (f) {
    view.filesTop = f.getBoundingClientRect().top - s.getBoundingClientRect().top + s.scrollTop
    view.width = f.clientWidth
  }
}
let frame = 0
function onScroll() {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(measure)
}

const cols = computed(() =>
  store.viewMode === 'grid' ? Math.max(1, Math.floor((view.width + GAP) / (TILE_MIN + GAP))) : 1,
)
const win = computed(() => {
  const rowH = ROW_H[store.viewMode] || ROW_H.list
  const rows = Math.ceil(files.value.length / cols.value)
  const rel = view.top - view.filesTop
  const first = Math.min(rows, Math.max(0, Math.floor(rel / rowH) - OVERSCAN))
  const last = Math.min(rows, Math.max(first, Math.ceil((rel + view.height) / rowH) + OVERSCAN))
  return {
    files: files.value.slice(first * cols.value, last * cols.value),
    padTop: first * rowH,
    padBottom: (rows - last) * rowH,
  }
})

// Start at the top whenever the list changes identity (folder, filter, view, source).
watch(
  () => [store.activeSourceId, store.currentFolder, store.statusFilter, store.viewMode],
  async () => {
    if (scroller.value) scroller.value.scrollTop = 0
    await nextTick()
    measure()
  },
)
watch(() => folders.value.length, () => nextTick(measure))

let resizeObserver
onMounted(() => {
  resizeObserver = new ResizeObserver(measure)
  resizeObserver.observe(scroller.value)
  measure()
})
onBeforeUnmount(() => {
  cancelAnimationFrame(frame)
  resizeObserver?.disconnect()
})

// Resizable side panels. Widths persist across sessions; double-clicking a
// divider resets it to the default.
const TREE_DEFAULT = 224
const PREVIEW_DEFAULT = 288
const clamp = (v, lo, hi) => Math.min(hi, Math.max(lo, v))
const loadWidth = (key, fallback) => {
  const v = parseInt(localStorage.getItem(key) || '', 10)
  return Number.isFinite(v) ? v : fallback
}
const treeWidth = ref(loadWidth('findit.treeWidth', TREE_DEFAULT))
const previewWidth = ref(loadWidth('findit.previewWidth', PREVIEW_DEFAULT))
watch(treeWidth, (v) => localStorage.setItem('findit.treeWidth', String(v)))
watch(previewWidth, (v) => localStorage.setItem('findit.previewWidth', String(v)))
const resizeTree = (d) => (treeWidth.value = clamp(treeWidth.value + d, 160, 480))
const resizePreview = (d) => (previewWidth.value = clamp(previewWidth.value - d, 200, 560))
</script>

<template>
  <div class="flex-1 flex flex-col overflow-hidden bg-white">
    <!-- Nav + view toolbar -->
    <div class="flex items-center gap-2 px-3 py-2 border-b bg-white/80 backdrop-blur">
      <div class="flex items-center gap-0.5">
        <button
          class="w-7 h-7 rounded-md flex items-center justify-center text-slate-500 disabled:opacity-30 hover:bg-slate-100"
          :disabled="!canBack()" @click="back" title="Back"
        >‹</button>
        <button
          class="w-7 h-7 rounded-md flex items-center justify-center text-slate-500 disabled:opacity-30 hover:bg-slate-100"
          :disabled="!canForward()" @click="forward" title="Forward"
        >›</button>
        <button
          class="w-7 h-7 rounded-md flex items-center justify-center text-slate-500 disabled:opacity-30 hover:bg-slate-100"
          :disabled="!canUp()" @click="goUp" title="Up to parent folder"
        >⤴</button>
      </div>

      <!-- breadcrumb -->
      <div class="flex items-center gap-1 text-sm text-slate-600 min-w-0 flex-1">
        <template v-for="(c, i) in store.breadcrumb" :key="i">
          <button
            class="hover:text-blue-600 truncate max-w-[180px] px-1 rounded"
            :class="i === store.breadcrumb.length - 1 ? 'font-semibold text-slate-900' : ''"
            @click="goBreadcrumb(i)"
          >{{ c.name }}</button>
          <span v-if="i < store.breadcrumb.length - 1" class="text-slate-300">›</span>
        </template>
      </div>

      <!-- status legend -->
      <Hint side="bottom" text="Green = you can recover it now. Orange = only partly recoverable, or the name is known but the contents aren’t here (try the Raw files source).">
        <span class="text-xs text-slate-400 hover:text-slate-600 cursor-help px-1.5 py-1 rounded">legend</span>
      </Hint>
      <div class="hidden xl:flex items-center gap-3 text-[11px] text-slate-500">
        <span v-for="s in statusList()" :key="s.label" class="flex items-center gap-1">
          <span class="w-2 h-2 rounded-full" :class="s.dot"></span>{{ s.label }}
        </span>
      </div>

      <!-- view toggles -->
      <div class="flex rounded-lg border overflow-hidden text-sm ml-1">
        <button
          v-for="m in [['grid','▦','Icons'],['list','☰','List'],['details','≣','Details']]"
          :key="m[0]"
          class="px-2.5 py-1"
          :class="store.viewMode === m[0] ? 'bg-blue-600 text-white' : 'bg-white text-slate-500 hover:bg-slate-50'"
          :title="m[2]"
          @click="store.viewMode = m[0]"
        >{{ m[1] }}</button>
      </div>

      <!-- status filter -->
      <select
        v-model="store.statusFilter"
        class="text-xs border rounded-lg px-2 py-1 bg-white text-slate-600"
        title="Filter files by status"
      >
        <option value="all">All files</option>
        <option value="recoverable">Recoverable only</option>
        <option value="limited">Limited only</option>
      </select>
    </div>

    <!-- Source summary -->
    <div class="px-4 py-2 border-b bg-slate-50 flex items-center gap-2 text-sm">
      <span class="font-medium text-slate-700">
        {{ sourceLabel(src) }}
      </span>
      <span
        v-if="conf"
        class="text-[10px] px-1.5 py-0.5 rounded flex items-center gap-1"
        :class="conf.cls"
      >
        {{ conf.label }} recoverable
        <Hint text="Based on how many files in this source actually have recoverable contents." />
      </span>
      <span class="text-slate-400">·</span>
      <span class="text-emerald-600">{{ summary.recoverable }} recoverable</span>
      <span v-if="summary.nameOnly" class="text-slate-400">
        · {{ summary.nameOnly }} name-only
        <Hint text="Files we can see listed, but whose contents aren’t available from this source. Check the Raw files source too." />
      </span>
    </div>

    <!-- PROMINENT selection / recover bar -->
    <transition name="slide">
      <div
        v-if="selCount > 0"
        class="mx-3 mt-3 rounded-xl border border-blue-200 bg-blue-50 px-4 py-2.5 flex items-center gap-3 shadow-sm"
      >
        <span class="text-sm font-medium text-blue-800">{{ selCount }} file{{ selCount === 1 ? '' : 's' }} selected</span>
        <button class="text-xs text-blue-600 hover:underline" @click="clearSelection">Clear</button>
        <div class="flex-1"></div>
        <label class="flex items-center gap-1 text-xs text-slate-600">
          <input type="checkbox" v-model="store.preservePaths" />
          Keep folders
          <Hint text="Recreate the original folder structure inside the destination." />
        </label>
        <div class="flex items-center gap-1">
          <input
            v-model="store.destDir"
            placeholder="Choose destination…"
            class="border rounded-lg px-2.5 py-1.5 text-xs w-52 bg-white"
          />
          <button class="px-2.5 py-1.5 bg-white border rounded-lg text-xs hover:bg-slate-50" @click="chooseDest">
            Browse…
          </button>
        </div>
        <button
          class="px-4 py-1.5 bg-emerald-600 text-white rounded-lg text-sm font-medium hover:bg-emerald-700 disabled:opacity-40 shadow-sm"
          :disabled="store.recovering || !store.destDir"
          @click="doRecover"
        >
          {{ store.recovering ? 'Recovering…' : `Recover ${selCount} file${selCount === 1 ? '' : 's'}` }}
        </button>
      </div>
    </transition>

    <!-- Recover confirmation -->
    <transition name="slide">
      <div
        v-if="store.recoverResult"
        class="mx-3 mt-3 rounded-xl border px-4 py-3 flex items-center gap-3 shadow-sm"
        :class="store.recoverResult.written ? 'bg-emerald-50 border-emerald-200' : 'bg-red-50 border-red-200'"
      >
        <span class="text-2xl">{{ store.recoverResult.written ? '✅' : '⚠️' }}</span>
        <div class="min-w-0">
          <div class="text-sm font-medium" :class="store.recoverResult.written ? 'text-emerald-800' : 'text-red-700'">
            <template v-if="store.recoverResult.written">
              Recovered {{ store.recoverResult.written }} file{{ store.recoverResult.written === 1 ? '' : 's' }}
            </template>
            <template v-else>Nothing could be recovered</template>
            <span v-if="store.recoverResult.failed" class="text-red-600 font-normal">
              · {{ store.recoverResult.failed }} couldn’t be saved
            </span>
          </div>
          <div class="text-xs text-slate-500 truncate">to {{ store.destDir }}</div>
        </div>
        <div class="flex-1"></div>
        <button
          v-if="store.recoverResult.written"
          class="px-3 py-1.5 bg-white border rounded-lg text-sm hover:bg-slate-50"
          @click="openDest"
        >
          Open folder
        </button>
        <button class="px-3 py-1.5 text-slate-500 text-sm hover:text-slate-700" @click="dismissRecover">
          Dismiss
        </button>
      </div>
    </transition>

    <div class="flex-1 flex overflow-hidden">
      <!-- Left: folder tree -->
      <aside
        v-if="src && src.kind === 'filesystem' && src.root"
        class="border-r bg-slate-50/70 overflow-auto p-2 shrink-0"
        :style="{ width: treeWidth + 'px' }"
      >
        <FolderTree :node="src.root" :label="sourceLabel(src)" />
      </aside>
      <Resizer
        v-if="src && src.kind === 'filesystem' && src.root"
        @resize="resizeTree"
        @reset="treeWidth = TREE_DEFAULT"
      />

      <!-- Center: contents -->
      <section ref="scroller" class="flex-1 overflow-auto p-4" @scroll="onScroll">
        <div class="flex items-center gap-2 mb-3 text-xs text-slate-500">
          <button class="px-2 py-1 bg-slate-100 rounded-md hover:bg-slate-200" @click="selectAllCurrent">Select all</button>
          <span>{{ folders.length }} folder{{ folders.length === 1 ? '' : 's' }}, {{ files.length }} file{{ files.length === 1 ? '' : 's' }}</span>
        </div>

        <!-- GRID -->
        <template v-if="store.viewMode === 'grid'">
          <div v-if="folders.length" class="grid grid-cols-[repeat(auto-fill,minmax(128px,1fr))] gap-2 mb-2">
            <button
              v-for="(f, i) in folders"
              :key="'d' + i"
              class="flex flex-col items-center p-3 rounded-xl hover:bg-slate-100 transition"
              @dblclick="openFolder(f)"
              @click="openFolder(f)"
            >
              <div class="text-5xl leading-none">📁</div>
              <div class="text-xs text-center truncate w-full mt-1.5 text-slate-700">{{ folderLabel(f.name) }}</div>
            </button>
          </div>

          <div ref="filesEl" :style="{ paddingTop: win.padTop + 'px', paddingBottom: win.padBottom + 'px' }">
            <div class="grid grid-cols-[repeat(auto-fill,minmax(128px,1fr))] gap-2">
              <div
                v-for="f in win.files"
                :key="f.id"
                class="group relative h-[156px] overflow-hidden flex flex-col items-center p-2.5 rounded-xl cursor-pointer ring-1 transition"
                :class="isSelected(f.id) ? 'ring-blue-400 bg-blue-50' : 'ring-transparent hover:bg-slate-100'"
                @click="openFile(f)"
              >
                <input
                  type="checkbox"
                  class="absolute top-1.5 left-1.5 opacity-0 group-hover:opacity-100"
                  :class="isSelected(f.id) ? 'opacity-100' : ''"
                  :checked="isSelected(f.id)"
                  :disabled="!f.recoverable"
                  @click.stop
                  @change="toggleFile(f.id)"
                />
                <FileThumb :file="f" />
                <div class="text-xs text-center truncate w-full mt-1.5" :class="f.deleted ? 'text-slate-400 line-through' : 'text-slate-700'">
                  {{ f.name }}
                </div>
                <div class="text-[10px] text-slate-400 flex items-center gap-1">
                  {{ fmtSize(f.size) }}
                  <Hint :text="statusInfo(f.assessment?.status).tip">
                    <span class="w-2 h-2 rounded-full inline-block cursor-help" :class="statusInfo(f.assessment?.status).dot"></span>
                  </Hint>
                </div>
              </div>
            </div>
          </div>
        </template>

        <!-- LIST -->
        <template v-else-if="store.viewMode === 'list'">
          <div v-if="folders.length" class="flex flex-col gap-0.5 mb-0.5">
            <button
              v-for="(f, i) in folders"
              :key="'d' + i"
              class="w-full flex items-center gap-2 px-2 h-8 rounded-lg hover:bg-slate-100 text-sm text-left"
              @click="openFolder(f)"
            ><span>📁</span><span class="truncate">{{ folderLabel(f.name) }}</span></button>
          </div>
          <div ref="filesEl" :style="{ paddingTop: win.padTop + 'px', paddingBottom: win.padBottom + 'px' }">
            <div class="flex flex-col gap-0.5">
              <div
                v-for="f in win.files"
                :key="f.id"
                class="flex items-center gap-2 px-2 h-8 shrink-0 rounded-lg text-sm cursor-pointer"
                :class="isSelected(f.id) ? 'bg-blue-50' : 'hover:bg-slate-100'"
                @click="openFile(f)"
              >
                <input type="checkbox" :checked="isSelected(f.id)" :disabled="!f.recoverable" @click.stop @change="toggleFile(f.id)" />
                <span>{{ iconFor(f.ext) }}</span>
                <span class="flex-1 truncate" :class="f.deleted ? 'text-slate-400 line-through' : ''">{{ f.name }}</span>
                <span class="text-xs text-slate-400">{{ fmtSize(f.size) }}</span>
                <Hint :text="statusInfo(f.assessment?.status).tip">
                  <span class="text-[11px] flex items-center gap-1 cursor-help" :class="statusInfo(f.assessment?.status).text">
                    <span class="w-2 h-2 rounded-full" :class="statusInfo(f.assessment?.status).dot"></span>
                    {{ statusInfo(f.assessment?.status).label }}
                  </span>
                </Hint>
              </div>
            </div>
          </div>
        </template>

        <!-- DETAILS -->
        <table v-else class="w-full text-sm table-fixed">
          <thead class="text-xs text-slate-400 border-b">
            <tr>
              <th class="w-8"></th>
              <th class="text-left py-1.5">Name</th>
              <th class="text-left w-20">Type</th>
              <th class="text-right w-24">Size</th>
              <th class="text-left pl-3 w-32">Status</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="(f, i) in folders" :key="'d' + i" class="h-8 hover:bg-slate-100 cursor-pointer" @click="openFolder(f)">
              <td></td>
              <td class="truncate">📁 {{ folderLabel(f.name) }}</td>
              <td class="text-slate-400">Folder</td>
              <td></td>
              <td></td>
            </tr>
            <tr ref="filesEl" aria-hidden="true">
              <td colspan="5" class="p-0" :style="{ height: win.padTop + 'px' }"></td>
            </tr>
            <tr
              v-for="f in win.files"
              :key="f.id"
              class="h-8 cursor-pointer"
              :class="isSelected(f.id) ? 'bg-blue-50' : 'hover:bg-slate-100'"
              @click="openFile(f)"
            >
              <td class="text-center">
                <input type="checkbox" :checked="isSelected(f.id)" :disabled="!f.recoverable" @click.stop @change="toggleFile(f.id)" />
              </td>
              <td class="truncate" :class="f.deleted ? 'text-slate-400 line-through' : ''" :title="f.name">{{ iconFor(f.ext) }} {{ f.name }}</td>
              <td class="text-slate-400 uppercase text-xs truncate">{{ f.ext || '—' }}</td>
              <td class="text-right text-slate-500">{{ fmtSize(f.size) }}</td>
              <td class="pl-3">
                <Hint :text="statusInfo(f.assessment?.status).tip">
                  <span class="flex items-center gap-1 cursor-help" :class="statusInfo(f.assessment?.status).text">
                    <span class="w-2 h-2 rounded-full" :class="statusInfo(f.assessment?.status).dot"></span>
                    {{ statusInfo(f.assessment?.status).label }}
                  </span>
                </Hint>
              </td>
            </tr>
            <tr aria-hidden="true">
              <td colspan="5" class="p-0" :style="{ height: win.padBottom + 'px' }"></td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- Right: preview (list/details) -->
      <Resizer
        v-if="store.selectedFile && store.viewMode !== 'grid'"
        @resize="resizePreview"
        @reset="previewWidth = PREVIEW_DEFAULT"
      />
      <aside
        v-if="store.selectedFile && store.viewMode !== 'grid'"
        class="border-l bg-slate-50/60 overflow-auto p-4 shrink-0"
        :style="{ width: previewWidth + 'px' }"
      >
        <div class="text-sm font-medium truncate mb-3">{{ store.selectedFile.name }}</div>
        <div class="rounded-xl border bg-white flex items-center justify-center min-h-[160px] mb-3 overflow-hidden">
          <img
            v-if="store.previews[store.selectedFile.id]?.thumbnailDataUrl"
            :src="store.previews[store.selectedFile.id].thumbnailDataUrl"
            class="max-w-full max-h-64 object-contain"
          />
          <span v-else class="text-6xl">{{ iconFor(store.selectedFile.ext) }}</span>
        </div>
        <dl class="text-xs text-slate-600 space-y-1.5">
          <div class="flex justify-between"><dt class="text-slate-400">Size</dt><dd>{{ fmtSize(store.selectedFile.size) }}</dd></div>
          <div class="flex justify-between"><dt class="text-slate-400">Type</dt><dd class="uppercase">{{ store.selectedFile.ext || '—' }}</dd></div>
          <div class="flex justify-between items-center">
            <dt class="text-slate-400">Status</dt>
            <dd class="flex items-center gap-1" :class="statusInfo(store.selectedFile.assessment?.status).text">
              <span class="w-2 h-2 rounded-full" :class="statusInfo(store.selectedFile.assessment?.status).dot"></span>
              {{ statusInfo(store.selectedFile.assessment?.status).label }}
            </dd>
          </div>
          <p class="text-slate-400 pt-1 leading-snug">{{ statusInfo(store.selectedFile.assessment?.status).tip }}</p>
        </dl>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.slide-enter-active,
.slide-leave-active {
  transition: all 0.18s ease;
}
.slide-enter-from,
.slide-leave-to {
  opacity: 0;
  transform: translateY(-6px);
}
</style>
