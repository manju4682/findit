// Central reactive store: all shared UI state plus the actions that call the
// Go backend and react to its events. Components stay presentational.
import { reactive } from 'vue'
import {
  ListDevices,
  SelectImageFile,
  SelectSaveImagePath,
  SelectDirectory,
  StartScan,
  StartScanDevice,
  StartClone,
  DetectPartitions,
  Preview,
  Recover,
  RevealInFinder,
  OpenFullDiskAccessSettings,
  SupportedRawTypes,
} from '../wailsjs/go/main/App'
import { EventsOn } from '../wailsjs/runtime/runtime'
import { folderLabel, sourceLabel } from './utils'

export const store = reactive({
  step: 'source', // source | clone | scan | scanning | results
  error: '',

  // source selection
  devices: [],
  loadingDevices: false,
  selectedDeviceId: '',
  sourceKind: '', // 'device' | 'image'
  imagePath: '',

  // clone
  cloning: false,
  cloneStatus: '',
  clonePct: 0,

  // scan options
  allRawTypes: [],
  filesystems: { NTFS: true, exFAT: true, FAT32: true },
  rawEnabled: true,
  rawSelected: {},

  // partition picker (advanced, optional)
  partitions: [], // [{ offset, fsType, label, size }]
  selectedPartitions: {}, // offset(string) -> bool
  partitionsExpanded: false,

  // scanning
  progress: [],

  // results
  diagnosis: null,
  sources: [],
  activeSourceId: '',
  currentFolder: null, // Node ref (filesystem) or null (raw)
  breadcrumb: [], // [{ name, node }]
  history: [], // visited folder nodes for back/forward
  histIndex: -1,
  selected: {}, // sourceId -> { fileId: true }
  viewMode: 'grid', // grid | list | details
  statusFilter: 'all', // all | recoverable | limited
  previews: {}, // fileId -> PreviewDTO
  selectedFile: null,

  // recover
  destDir: '',
  preservePaths: true,
  recovering: false,
  recoverResult: null,
})

// ---- helpers ----------------------------------------------------------------

export function activeSource() {
  return store.sources.find((s) => s.id === store.activeSourceId) || null
}
const isDir = (n) => n && n.isDir
export function childFolders(node) {
  return node && node.children ? node.children.filter(isDir) : []
}
export function childFiles(node) {
  return node && node.children ? node.children.filter((c) => !c.isDir).map((c) => c.file) : []
}
export function currentFolders() {
  const s = activeSource()
  if (!s || s.kind !== 'filesystem') return []
  return childFolders(store.currentFolder)
}
export function currentFiles() {
  const s = activeSource()
  if (!s) return []
  if (s.kind !== 'filesystem') return s.files || []
  return childFiles(store.currentFolder)
}

function findPath(root, target, acc = []) {
  if (!root) return null
  const here = [...acc, root]
  if (root === target) return here
  for (const c of root.children || []) {
    if (!c.isDir) continue
    const r = findPath(c, target, here)
    if (r) return r
  }
  return null
}

// ---- source / clone ---------------------------------------------------------

export async function loadDevices() {
  store.loadingDevices = true
  store.error = ''
  try {
    store.devices = (await ListDevices()) || []
  } catch (e) {
    store.error = String(e)
  } finally {
    store.loadingDevices = false
  }
}

export function selectDevice(id) {
  store.selectedDeviceId = id
  store.sourceKind = 'device'
  store.step = 'clone'
}

export async function chooseImage() {
  const p = await SelectImageFile()
  if (p) {
    store.imagePath = p
    store.sourceKind = 'image'
    store.step = 'scan'
    detectPartitions()
  }
}

export function selectedDevice() {
  return store.devices.find((d) => d.id === store.selectedDeviceId) || null
}

export async function startClone() {
  const dev = selectedDevice()
  if (!dev) return
  const def = `${dev.id}-${Date.now()}.bin`
  const dest = await SelectSaveImagePath(def)
  if (!dest) return
  store.cloning = true
  store.cloneStatus = ''
  store.error = ''
  try {
    await StartClone(dev.id, dest)
  } catch (e) {
    store.error = String(e)
    store.cloning = false
  }
}

export function skipClone() {
  store.step = 'scan'
}

// ---- scan -------------------------------------------------------------------

export const selectedFilesystems = () =>
  Object.keys(store.filesystems).filter((k) => store.filesystems[k])
export const selectedRawTypes = () =>
  Object.keys(store.rawSelected).filter((k) => store.rawSelected[k])

// detectPartitions does a fast partition-table read of the chosen image so the
// user can optionally narrow the scan. It's best-effort: on any failure the
// picker simply stays hidden and we scan everything (the safe default). Only
// images are probed — scanning a device directly always scans all partitions.
export async function detectPartitions() {
  store.partitions = []
  for (const k of Object.keys(store.selectedPartitions)) delete store.selectedPartitions[k]
  store.partitionsExpanded = false
  if (!store.imagePath) return
  try {
    const parts = (await DetectPartitions(store.imagePath)) || []
    store.partitions = parts
    for (const p of parts) store.selectedPartitions[String(p.offset)] = true
  } catch (e) {
    /* picker is optional */
  }
}

// scanPartitionOffsets returns the offsets to scan, or [] to scan everything.
// We only send a narrowed list when the user actually deselected some.
export function scanPartitionOffsets() {
  const selected = store.partitions.filter((p) => store.selectedPartitions[String(p.offset)])
  if (!selected.length || selected.length === store.partitions.length) return []
  return selected.map((p) => p.offset)
}

export async function startScan() {
  store.error = ''
  store.progress = []
  store.sources = []
  store.diagnosis = null
  store.activeSourceId = ''
  store.recoverResult = null
  store.previews = {}
  for (const k of Object.keys(store.selected)) delete store.selected[k]
  store.step = 'scanning'

  const fs = selectedFilesystems()
  const raw = store.rawEnabled
  const exts = raw ? selectedRawTypes() : []
  try {
    if (store.sourceKind === 'image' || store.imagePath) {
      await StartScan(store.imagePath, fs, raw, exts, scanPartitionOffsets())
    } else {
      await StartScanDevice(store.selectedDeviceId, fs, raw, exts, [])
    }
  } catch (e) {
    store.error = String(e)
    store.step = 'scan'
  }
}

// ---- explorer navigation ----------------------------------------------------

export function setActiveSource(id) {
  store.activeSourceId = id
  store.selectedFile = null
  const s = activeSource()
  const root = s && s.kind === 'filesystem' ? s.root : null
  store.history = [root]
  store.histIndex = 0
  applyFolder(root)
}

// applyFolder sets the current folder + breadcrumb without touching history.
function applyFolder(node) {
  const s = activeSource()
  store.currentFolder = node
  store.selectedFile = null
  if (s && s.kind === 'filesystem' && s.root) {
    const path = findPath(s.root, node || s.root) || [s.root]
    store.breadcrumb = path.map((n, i) => ({
      name: i === 0 ? sourceLabel(s) : folderLabel(n.name),
      node: n,
    }))
  } else {
    store.breadcrumb = [{ name: 'Raw files', node: null }]
  }
}

export function openFolder(node) {
  store.history = store.history.slice(0, store.histIndex + 1)
  store.history.push(node)
  store.histIndex = store.history.length - 1
  applyFolder(node)
}

export function goBreadcrumb(i) {
  const crumb = store.breadcrumb[i]
  if (crumb) openFolder(crumb.node)
}

export const canBack = () => store.histIndex > 0
export const canForward = () => store.histIndex < store.history.length - 1
export const canUp = () => store.breadcrumb.length > 1

export function back() {
  if (canBack()) {
    store.histIndex--
    applyFolder(store.history[store.histIndex])
  }
}
export function forward() {
  if (canForward()) {
    store.histIndex++
    applyFolder(store.history[store.histIndex])
  }
}
export function goUp() {
  if (canUp()) openFolder(store.breadcrumb[store.breadcrumb.length - 2].node)
}

// ---- selection & preview ----------------------------------------------------

export function selMap(sourceId) {
  if (!store.selected[sourceId]) store.selected[sourceId] = {}
  return store.selected[sourceId]
}
export function toggleFile(fileId) {
  store.recoverResult = null // starting a new selection clears the last result
  const m = selMap(store.activeSourceId)
  if (m[fileId]) delete m[fileId]
  else m[fileId] = true
}
export function isSelected(fileId) {
  return !!(store.selected[store.activeSourceId] && store.selected[store.activeSourceId][fileId])
}
export function selectedIds(sourceId = store.activeSourceId) {
  return Object.keys(store.selected[sourceId] || {})
}
export function selectAllCurrent() {
  store.recoverResult = null
  const m = selMap(store.activeSourceId)
  for (const f of currentFiles()) if (f.recoverable) m[f.id] = true
}
export function clearSelection() {
  const m = store.selected[store.activeSourceId]
  if (m) for (const k of Object.keys(m)) delete m[k]
}

export async function loadPreview(file) {
  if (!file || store.previews[file.id]) return store.previews[file.id]
  try {
    const p = await Preview(store.activeSourceId, file.id)
    store.previews[file.id] = p
    return p
  } catch (e) {
    return null
  }
}

// currentFilesFiltered applies the status filter to the current folder's files.
export function currentFilesFiltered() {
  const files = currentFiles()
  if (store.statusFilter === 'recoverable') return files.filter((f) => f.recoverable)
  if (store.statusFilter === 'limited') return files.filter((f) => !f.recoverable)
  return files
}

export async function openFile(file) {
  store.selectedFile = file
  await loadPreview(file)
}

// ---- recover ----------------------------------------------------------------

export async function chooseDest() {
  const d = await SelectDirectory()
  if (d) store.destDir = d
}

export async function doRecover() {
  const ids = selectedIds()
  if (!ids.length || !store.destDir) return
  store.recovering = true
  store.recoverResult = null
  try {
    const res = await Recover(store.activeSourceId, ids, store.destDir, store.preservePaths)
    store.recoverResult = res
    // On success, clear the selection so the action bar collapses and the user
    // gets a clear confirmation instead of being able to re-run the same job.
    if (res && res.written > 0) clearSelection()
  } catch (e) {
    store.error = String(e)
  } finally {
    store.recovering = false
  }
}

export function dismissRecover() {
  store.recoverResult = null
}

export async function openDest() {
  if (store.destDir) {
    try {
      await RevealInFinder(store.destDir)
    } catch (e) {
      /* ignore */
    }
  }
}

export async function openFullDiskAccess() {
  try {
    await OpenFullDiskAccessSettings()
  } catch (e) {
    /* ignore */
  }
}

export function reset() {
  store.step = 'source'
  store.sourceKind = ''
  store.selectedDeviceId = ''
  store.imagePath = ''
  store.partitions = []
  store.partitionsExpanded = false
  store.sources = []
  store.diagnosis = null
  store.activeSourceId = ''
  store.recoverResult = null
  store.error = ''
}

// ---- init -------------------------------------------------------------------

let wired = false
export async function initStore() {
  if (wired) return
  wired = true

  try {
    store.allRawTypes = (await SupportedRawTypes()) || []
    for (const t of store.allRawTypes)
      store.rawSelected[t] = ['jpg', 'png', 'mp4', 'pdf'].includes(t)
  } catch (e) {
    /* bindings may be missing in bare preview */
  }

  EventsOn('scan:progress', (e) => {
    if (e && e.message) store.progress.push(e.message)
  })
  EventsOn('scan:source', (s) => {
    if (s && !store.sources.find((x) => x.id === s.id)) store.sources.push(s)
  })
  EventsOn('scan:done', (done) => {
    store.diagnosis = done?.diagnosis || null
    if (done?.result?.sources) store.sources = done.result.sources
    if (store.sources.length) setActiveSource(store.sources[0].id)
    store.step = 'results'
  })
  EventsOn('scan:error', (msg) => {
    store.error = msg || 'Scan failed'
    store.step = 'scan'
  })

  EventsOn('clone:progress', (e) => {
    if (e && e.message) {
      store.cloneStatus = e.message
      if (e.total > 0) store.clonePct = Math.min(100, Math.round((e.done / e.total) * 100))
    }
  })
  EventsOn('clone:done', (e) => {
    store.cloning = false
    store.clonePct = 100
    store.imagePath = e?.path || ''
    store.sourceKind = 'image'
    store.step = 'scan'
    detectPartitions()
  })
  EventsOn('clone:error', (msg) => {
    store.cloning = false
    store.error = msg || 'Clone failed'
  })

  await loadDevices()
}
