<script setup>
import { onMounted, computed } from 'vue'
import Explorer from './components/Explorer.vue'
import Hint from './components/Hint.vue'
import {
  store,
  initStore,
  loadDevices,
  selectDevice,
  selectedDevice,
  chooseImage,
  startClone,
  skipClone,
  cancelClone,
  cancelScan,
  startScan,
  setActiveSource,
  reset,
  openFullDiskAccess,
} from './store'
import { fmtSize, sourceSummary, sourceLabel, recoverConfidence } from './utils'
import appicon from './assets/appicon-128.png'

onMounted(initStore)

const steps = [
  { key: 'source', label: 'Choose source', icon: '💽' },
  { key: 'clone', label: 'Safe copy', icon: '🛟' },
  { key: 'scan', label: 'Scan', icon: '🔎' },
  { key: 'results', label: 'Recover', icon: '📂' },
]
const stepIndex = computed(() => ({ source: 0, clone: 1, scan: 2, scanning: 2, results: 3 }[store.step] ?? 0))

function deviceIcon(d) {
  return d.removable ? '💾' : '🖥️'
}
function gotoStep(key) {
  const order = ['source', 'clone', 'scan', 'results']
  if (order.indexOf(key) < stepIndex.value && key !== 'clone') store.step = key
  else if (key === 'clone' && store.sourceKind === 'device' && stepIndex.value > 1) store.step = 'clone'
}
</script>

<template>
  <div class="h-full flex bg-white text-slate-900">
    <!-- SIDEBAR (Finder-style) -->
    <aside class="w-60 shrink-0 bg-slate-100/80 backdrop-blur border-r border-slate-200 flex flex-col">
      <!-- drag strip: leaves room for the macOS window controls -->
      <div class="h-9" style="--wails-draggable: drag"></div>
      <div class="px-4 pb-3 flex items-center gap-2.5" style="--wails-draggable: drag">
        <img :src="appicon" alt="FindIt" class="w-8 h-8 rounded-[10px] shadow-sm" />
        <div>
          <div class="font-semibold leading-tight text-slate-800">FindIt</div>
          <div class="text-[11px] text-slate-500">Recover your files</div>
        </div>
      </div>

      <!-- Pre-results: stepper -->
      <nav v-if="store.step !== 'results'" class="px-3 py-2 space-y-0.5 flex-1">
        <div class="text-[11px] uppercase tracking-wide text-slate-400 px-2 mb-1">Steps</div>
        <button
          v-for="(s, i) in steps"
          :key="s.key"
          class="w-full flex items-center gap-2.5 px-2.5 py-2 rounded-lg text-sm text-left transition"
          :class="[
            i === stepIndex ? 'bg-blue-500/10 text-blue-700 font-medium' : i < stepIndex ? 'text-slate-500 hover:bg-slate-200/60' : 'text-slate-400',
            i < stepIndex ? 'cursor-pointer' : 'cursor-default',
          ]"
          @click="gotoStep(s.key)"
        >
          <span
            class="w-5 h-5 rounded-full flex items-center justify-center text-[11px] shrink-0"
            :class="i < stepIndex ? 'bg-emerald-500 text-white' : i === stepIndex ? 'bg-blue-600 text-white' : 'bg-slate-300 text-white'"
          >{{ i < stepIndex ? '✓' : i + 1 }}</span>
          {{ s.label }}
        </button>
      </nav>

      <!-- Results: sources list -->
      <nav v-else class="px-3 py-2 flex-1 overflow-auto">
        <div class="text-[11px] uppercase tracking-wide text-slate-400 px-2 mb-1 flex items-center gap-1">
          Sources found
          <Hint text="Each place we found files — a filesystem (with folders) or raw content. They’re kept separate; the same file can appear in more than one." />
        </div>
        <button
          v-for="s in store.sources"
          :key="s.id"
          class="w-full text-left px-2.5 py-2 rounded-lg mb-0.5 transition"
          :class="s.id === store.activeSourceId ? 'bg-blue-500/10 ring-1 ring-blue-200' : 'hover:bg-slate-200/60'"
          @click="setActiveSource(s.id)"
        >
          <div class="flex items-center gap-2">
            <span>{{ s.kind === 'filesystem' ? '🗂️' : '🧩' }}</span>
            <span class="flex-1 truncate text-sm text-slate-800" :title="sourceLabel(s)">
              {{ sourceLabel(s) }}
            </span>
            <span
              v-if="recoverConfidence(s)"
              class="text-[10px] px-1.5 py-0.5 rounded"
              :class="recoverConfidence(s).cls"
            >{{ recoverConfidence(s).label }}</span>
          </div>
          <div class="text-[11px] text-slate-500 pl-6 mt-0.5">
            <span class="text-emerald-600">{{ sourceSummary(s).recoverable }} recoverable</span>
            <span v-if="sourceSummary(s).nameOnly"> · {{ sourceSummary(s).nameOnly }} name-only</span>
          </div>
        </button>
      </nav>

      <div class="p-3 border-t border-slate-200 text-[11px] text-slate-500">
        <div v-if="store.imagePath" class="truncate" :title="store.imagePath">📁 {{ store.imagePath.split('/').pop() }}</div>
        <div v-else-if="selectedDevice()" class="truncate">💾 {{ selectedDevice().name || selectedDevice().id }}</div>
        <button class="mt-2 text-slate-500 hover:text-slate-800" @click="reset">↺ Start over</button>
      </div>
    </aside>

    <!-- MAIN -->
    <main class="flex-1 flex flex-col overflow-hidden bg-slate-50">
      <div v-if="store.error" class="mx-6 mt-4 px-4 py-2 bg-red-50 text-red-700 rounded-lg text-sm border border-red-100">
        {{ store.error }}
        <button
          v-if="store.error.includes('Full Disk Access')"
          class="ml-1 text-blue-600 hover:underline"
          @click="openFullDiskAccess"
        >Open Full Disk Access settings…</button>
      </div>
      <div
        v-if="store.notice"
        class="mx-6 mt-4 px-4 py-2 bg-amber-50 text-amber-800 rounded-lg text-sm border border-amber-200 flex items-start gap-2"
      >
        <span class="flex-1">{{ store.notice }}</span>
        <button class="text-amber-700 hover:text-amber-900" title="Dismiss" @click="store.notice = ''">✕</button>
      </div>

      <!-- SOURCE -->
      <div v-if="store.step === 'source'" class="flex-1 overflow-auto p-8">
        <div class="max-w-3xl mx-auto">
          <h2 class="text-2xl font-semibold mb-1 text-slate-800">Where are your files?</h2>
          <p class="text-slate-500 mb-6">Pick the drive you want to recover from — or open a disk image you already made.</p>

          <div class="flex items-center justify-between mb-2">
            <h3 class="font-medium flex items-center gap-1">
              Connected drives
              <Hint text="Removable drives (USB sticks, SD cards) are the usual place to recover from. Internal disks are shown too." />
            </h3>
            <button class="text-sm text-blue-600 hover:underline" @click="loadDevices">
              {{ store.loadingDevices ? 'Refreshing…' : '↻ Refresh' }}
            </button>
          </div>

          <div class="grid sm:grid-cols-2 gap-3 mb-6">
            <button
              v-for="d in store.devices"
              :key="d.id"
              class="text-left border border-slate-200 rounded-xl p-4 bg-white hover:border-blue-400 hover:shadow-md transition"
              @click="selectDevice(d.id)"
            >
              <div class="flex items-center gap-3">
                <span class="text-3xl">{{ deviceIcon(d) }}</span>
                <div class="min-w-0">
                  <div class="font-medium truncate text-slate-800">{{ d.name || d.id }}</div>
                  <div class="text-xs text-slate-500">
                    {{ fmtSize(d.size) }} · {{ d.protocol || 'disk' }}
                    <span v-if="d.removable" class="text-emerald-600">· removable</span>
                    <span v-else class="text-amber-600">· internal ⚠️</span>
                  </div>
                </div>
              </div>
            </button>
            <div v-if="!store.devices.length && !store.loadingDevices" class="text-sm text-slate-400 p-4">No drives detected.</div>
          </div>

          <div class="border-t border-slate-200 pt-5">
            <h3 class="font-medium mb-1 flex items-center gap-1">
              Already have a copy?
              <Hint text="A disk image (.bin) is a full copy of a drive saved to a file. If you made one earlier, start straight from it." />
            </h3>
            <p class="text-sm text-slate-500 mb-3">Start straight from a saved disk image.</p>
            <button class="px-4 py-2 border border-slate-200 rounded-xl bg-white hover:bg-slate-50 text-sm" @click="chooseImage">
              Open a disk image…
            </button>
          </div>
        </div>
      </div>

      <!-- CLONE -->
      <div v-else-if="store.step === 'clone'" class="flex-1 overflow-auto p-8">
        <div class="max-w-2xl mx-auto">
          <div
            v-if="selectedDevice()?.internal"
            class="mb-3 px-4 py-3 bg-amber-50 border border-amber-200 rounded-xl text-sm text-amber-800 flex items-start gap-2"
          >
            <span class="text-lg">⚠️</span>
            <span>
              This looks like an <b>internal/system disk</b>. FindIt is designed for external drives
              (USB sticks, SD cards). Recovering from your system disk is risky — continue only if you
              understand what you’re doing.
            </span>
          </div>
          <div class="border border-slate-200 rounded-xl bg-white p-6 shadow-sm">
            <div class="flex items-start gap-3">
              <span class="text-3xl">🛟</span>
              <div>
                <h2 class="text-lg font-semibold flex items-center gap-1">
                  Make a safe copy first
                  <Hint text="Working on a copy means a failing drive can’t get worse, and we never write anything back to your original." />
                </h2>
                <p class="text-slate-600 text-sm mt-1">
                  We strongly recommend copying
                  <span class="font-medium">{{ selectedDevice()?.name || selectedDevice()?.id }}</span>
                  ({{ fmtSize(selectedDevice()?.size) }}) to a file before recovering. FindIt then works only on the copy.
                </p>
              </div>
            </div>

            <div v-if="!store.cloning" class="mt-6 flex gap-3">
              <button class="px-5 py-2.5 bg-blue-600 text-white rounded-xl font-medium hover:bg-blue-700 shadow-sm" @click="startClone">
                Clone drive (recommended)
              </button>
              <button class="px-5 py-2.5 border border-slate-200 rounded-xl text-slate-600 hover:bg-slate-50 text-sm" @click="skipClone">
                Skip and scan the drive directly
              </button>
            </div>

            <div v-else class="mt-6">
              <div class="text-sm font-medium mb-2">Copying… {{ store.clonePct }}%</div>
              <div class="h-2.5 bg-slate-200 rounded-full overflow-hidden mb-2">
                <div class="h-full bg-blue-600 transition-all" :style="{ width: store.clonePct + '%' }"></div>
              </div>
              <div class="flex items-center justify-between">
                <div class="text-xs text-slate-500">{{ store.cloneStatus || 'Waiting for permission…' }}</div>
                <button
                  class="px-3 py-1.5 border border-slate-200 rounded-lg text-xs text-slate-600 hover:bg-slate-50"
                  @click="cancelClone"
                >
                  Cancel
                </button>
              </div>
            </div>
          </div>

          <div class="mt-3 px-4 py-3 bg-slate-50 border border-slate-200 rounded-xl text-xs text-slate-500 flex items-start gap-2">
            <span>🔐</span>
            <div>
              Reading a drive needs administrator access, so cloning and “scan directly” both ask for
              your password once. If macOS still blocks the drive, allow FindIt under <b>Full Disk Access</b>.
              <button class="text-blue-600 hover:underline" @click="openFullDiskAccess">Open settings…</button>
              <br />
              <b>Scan directly</b> uses no extra disk space but keeps nothing for next time; <b>cloning</b>
              saves a reusable image and is safer for a failing drive.
            </div>
          </div>
        </div>
      </div>

      <!-- SCAN OPTIONS -->
      <div v-else-if="store.step === 'scan'" class="flex-1 overflow-auto p-8">
        <div class="max-w-2xl mx-auto space-y-5">
          <div>
            <h2 class="text-2xl font-semibold text-slate-800">What should we look for?</h2>
            <p class="text-slate-500 text-sm">Choose which filesystems and file types to search.</p>
          </div>

          <div class="border border-slate-200 rounded-xl bg-white p-5 shadow-sm">
            <h3 class="font-medium mb-3 flex items-center gap-1">
              Filesystems
              <Hint text="A filesystem is how a drive organizes files. Common ones are NTFS, exFAT and FAT32. Not sure which yours used? Leave them all on." />
            </h3>
            <div class="flex flex-wrap gap-4">
              <label v-for="fs in Object.keys(store.filesystems)" :key="fs" class="flex items-center gap-2 text-sm">
                <input type="checkbox" v-model="store.filesystems[fs]" /> {{ fs }}
              </label>
            </div>
          </div>

          <div class="border border-slate-200 rounded-xl bg-white p-5 shadow-sm">
            <label class="flex items-center gap-2 font-medium mb-3">
              <input type="checkbox" v-model="store.rawEnabled" />
              Search by file content (raw recovery)
              <Hint text="Finds files by their content even when the filesystem is too damaged to read. Pick the file types you care about." />
            </label>
            <div v-if="store.rawEnabled" class="flex flex-wrap gap-2">
              <label
                v-for="t in store.allRawTypes"
                :key="t"
                class="text-xs px-2.5 py-1 rounded-full border cursor-pointer select-none"
                :class="store.rawSelected[t] ? 'bg-blue-50 border-blue-300 text-blue-700' : 'bg-white text-slate-500 border-slate-200'"
              >
                <input type="checkbox" class="hidden" v-model="store.rawSelected[t]" /> .{{ t }}
              </label>
            </div>
          </div>

          <!-- Advanced: partition picker (only when the drive has more than one) -->
          <div v-if="store.partitions.length > 1" class="border border-slate-200 rounded-xl bg-white shadow-sm">
            <button
              class="w-full flex items-center justify-between px-5 py-3.5 text-left"
              @click="store.partitionsExpanded = !store.partitionsExpanded"
            >
              <span class="font-medium flex items-center gap-1">
                Advanced: choose partitions
                <Hint text="This drive has more than one partition. By default FindIt scans them all — you don’t need to change anything. If you know which partition your files were on, expand this to search only that one." />
              </span>
              <span class="text-slate-400 text-xs">{{ store.partitionsExpanded ? '▲' : '▼' }}</span>
            </button>
            <div v-if="store.partitionsExpanded" class="px-5 pb-4 pt-3 space-y-2 border-t border-slate-100">
              <p class="text-xs text-slate-500">All partitions are selected by default. Uncheck any you want to skip.</p>
              <label
                v-for="p in store.partitions"
                :key="p.offset"
                class="flex items-center gap-2 text-sm"
              >
                <input type="checkbox" v-model="store.selectedPartitions[String(p.offset)]" />
                <span class="font-medium">{{ p.fsType }}</span>
                <span v-if="p.label" class="text-slate-600">“{{ p.label }}”</span>
                <span class="text-slate-400">· {{ fmtSize(p.size) }}</span>
              </label>
            </div>
          </div>

          <button class="w-full py-3 bg-blue-600 text-white rounded-xl font-medium hover:bg-blue-700 shadow-sm" @click="startScan">
            Scan for my files
          </button>
        </div>
      </div>

      <!-- SCANNING -->
      <div v-else-if="store.step === 'scanning'" class="flex-1 flex items-center justify-center p-8">
        <div class="max-w-lg w-full text-center">
          <div class="text-lg font-medium mb-1">Analyzing your drive…</div>
          <div class="text-xs text-slate-500 mb-4">
            <template v-if="store.scanStatus">{{ store.scanStatus }} — a full drive can take several minutes.</template>
            <template v-else>Waiting for permission… a full drive can take several minutes.</template>
          </div>
          <div class="h-2 bg-slate-200 rounded-full overflow-hidden mb-4">
            <div
              v-if="store.scanPct > 0"
              class="h-full bg-blue-600 transition-all"
              :style="{ width: store.scanPct + '%' }"
            ></div>
            <div v-else class="h-full bg-blue-600 animate-pulse w-2/3 mx-auto"></div>
          </div>
          <div
            v-if="store.progress.length"
            class="text-left text-xs text-slate-500 bg-white border rounded-lg p-3 max-h-44 overflow-auto"
          >
            <div v-for="(m, i) in store.progress" :key="i" class="py-0.5">{{ m }}</div>
          </div>
          <button
            class="mt-4 px-4 py-2 border border-slate-200 rounded-lg text-sm text-slate-600 hover:bg-slate-50"
            @click="cancelScan"
          >
            Cancel scan
          </button>
        </div>
      </div>

      <!-- RESULTS -->
      <template v-else>
        <div v-if="store.diagnosis" class="mx-3 mt-3 px-4 py-2.5 bg-blue-50 border border-blue-100 rounded-xl text-sm text-slate-700 flex items-start gap-2">
          <span>💡</span><span>{{ store.diagnosis.narrative }}</span>
        </div>
        <Explorer class="flex-1 mt-2" />
      </template>
    </main>
  </div>
</template>
