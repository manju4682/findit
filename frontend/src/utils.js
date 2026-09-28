// Pure presentation helpers shared across components. No app state or backend
// calls live here.

export function fmtSize(n) {
  if (!n) return ''
  if (n < 1024) return n + ' B'
  if (n < 1024 * 1024) return (n / 1024).toFixed(0) + ' KB'
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB'
  return (n / 1024 / 1024 / 1024).toFixed(1) + ' GB'
}

export function confBadge(c) {
  if (c === 'High') return 'bg-emerald-100 text-emerald-700'
  if (c === 'Medium') return 'bg-amber-100 text-amber-700'
  return 'bg-slate-100 text-slate-600'
}

export function iconFor(ext) {
  if (['jpg', 'jpeg', 'png', 'gif'].includes(ext)) return '🖼️'
  if (['mp4', 'mov', 'm4v'].includes(ext)) return '🎬'
  if (ext === 'pdf') return '📄'
  if (ext === 'zip') return '🗜️'
  return '📄'
}

export function previewable(ext) {
  return ['jpg', 'jpeg', 'png', 'gif', 'mp4', 'mov', 'm4v'].includes(ext)
}

// File status meanings. Dots use only two colors so the legend stays simple:
// green = recoverable now, orange = anything less than fully recoverable.
const STATUS = {
  Good: {
    label: 'Recoverable',
    dot: 'bg-emerald-500',
    text: 'text-emerald-600',
    tip: 'The file’s contents are available — you can recover it now.',
  },
  Partial: {
    label: 'Partial',
    dot: 'bg-amber-500',
    text: 'text-amber-600',
    tip: 'Only part of this file could be recovered; it may not open correctly.',
  },
  MetadataOnly: {
    label: 'Name only',
    dot: 'bg-amber-500',
    text: 'text-amber-600',
    tip: 'We found the file’s name and details, but its contents aren’t available from this source. It may still be recoverable under “Raw files”.',
  },
  RawFragment: {
    label: 'Fragment',
    dot: 'bg-amber-500',
    text: 'text-amber-600',
    tip: 'Found by scanning raw content; the file may be incomplete.',
  },
  Uncertain: {
    label: 'Uncertain',
    dot: 'bg-amber-500',
    text: 'text-amber-600',
    tip: 'We can’t confirm how much of this file is recoverable.',
  },
}
export function statusInfo(s) {
  return STATUS[s] || STATUS.Uncertain
}

// Two-color legend shown in the toolbar.
export const legendItems = [
  { label: 'Recoverable', dot: 'bg-emerald-500' },
  { label: 'Limited', dot: 'bg-amber-500' },
]
export const statusList = () => legendItems

// folderLabel makes engine-internal names friendlier (e.g. TSK’s $OrphanFiles).
export function folderLabel(name) {
  if (name === '$OrphanFiles') return 'Orphaned files'
  if (name && name.startsWith('$')) return name.slice(1)
  return name || '/'
}

// sourceLabel builds a human label for a source, distinguishing partitions of
// the same filesystem type by their volume label and size (e.g.
// "NTFS · “Backup” · 120 GB").
export function sourceLabel(source) {
  if (!source) return ''
  if (source.kind !== 'filesystem') return 'Raw files'
  const parts = [source.fsType || 'Filesystem']
  if (source.label) parts.push(`“${source.label}”`)
  if (source.size) parts.push(fmtSize(source.size))
  return parts.join(' · ')
}

// sourceSummary uses the backend counts (O(1)) — no tree walking per render.
export function sourceSummary(source) {
  const total = source?.fileCount || 0
  const rec = source?.recoverableCount ?? total
  return { total, recoverable: rec, nameOnly: total - rec }
}

// recoverConfidence reflects how much of a source is actually recoverable,
// rather than how confident we are the filesystem exists.
export function recoverConfidence(source) {
  const s = sourceSummary(source)
  if (!s.total) return null
  const r = s.recoverable / s.total
  if (r >= 0.8) return { label: 'High', cls: 'bg-emerald-100 text-emerald-700' }
  if (r >= 0.4) return { label: 'Medium', cls: 'bg-amber-100 text-amber-700' }
  return { label: 'Low', cls: 'bg-slate-100 text-slate-600' }
}
