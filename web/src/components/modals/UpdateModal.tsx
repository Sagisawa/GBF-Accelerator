import React, { useState, useEffect, useRef } from 'react'
import { Modal } from '../common/Modal'
import {
  RefreshCw,
  ExternalLink,
  CheckCircle2,
  AlertCircle,
  Download,
  XCircle,
  ArrowUpCircle,
  Sparkles,
  ShieldCheck,
  ShieldAlert,
  FolderOpen,
} from 'lucide-react'
import {
  checkForUpdate,
  downloadUpdate,
  fetchDownloadStatus,
  cancelDownload,
  applyDownloadedUpdate,
  openUpdateFolder,
} from '../../api'
import { UpdateDownloadStatus } from '../../types'
import { isNewerVersion } from '../../utils/version'
import { useI18n } from '../../i18n'

export interface UpdateModalProps {
  isOpen: boolean
  onClose: () => void
  currentVersion?: string
  onUpdateSuccess?: () => void
}

type ApplyingState = 'idle' | 'preparing' | 'restarting' | 'success' | 'failed' | 'timeout'

const formatBytes = (bytes: number): string => {
  if (!bytes || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let val = bytes
  while (val >= 1024 && i < units.length - 1) {
    val /= 1024
    i++
  }
  return `${val.toFixed(1)} ${units[i]}`
}

// Lightweight Markdown text formatter to render styled text instead of raw syntax
const renderFormattedInline = (text: string) => {
  const parts = text.split(/(\*\*.*?\*\*|`.*?`)/g)
  return parts.map((part, idx) => {
    if (part.startsWith('**') && part.endsWith('**')) {
      return (
        <strong key={idx} className="font-bold text-slate-900">
          {part.slice(2, -2)}
        </strong>
      )
    }
    if (part.startsWith('`') && part.endsWith('`')) {
      return (
        <code key={idx} className="font-mono text-xs bg-slate-100 border border-slate-200 text-sky-800 px-1.5 py-0.5 rounded font-medium">
          {part.slice(1, -1)}
        </code>
      )
    }
    return part
  })
}

const ReleaseNotesViewer: React.FC<{ content: string }> = ({ content }) => {
  if (!content) return null

  const rawLines = content.split('\n')
  const elements: React.ReactNode[] = []
  let inCodeBlock = false
  let codeBlockBuffer: string[] = []

  rawLines.forEach((rawLine, idx) => {
    const line = rawLine.trim()

    // Code block fences (```)
    if (line.startsWith('```')) {
      if (inCodeBlock) {
        elements.push(
          <pre key={`code-${idx}`} className="bg-slate-900 text-slate-100 p-3 rounded-xl font-mono text-xs overflow-x-auto my-2 border border-slate-800">
            <code>{codeBlockBuffer.join('\n')}</code>
          </pre>
        )
        codeBlockBuffer = []
        inCodeBlock = false
      } else {
        inCodeBlock = true
      }
      return
    }

    if (inCodeBlock) {
      codeBlockBuffer.push(rawLine)
      return
    }

    if (!line) {
      elements.push(<div key={`sp-${idx}`} className="h-2" />)
      return
    }

    // Horizontal Rule
    if (line === '---' || line === '***' || line === '___') {
      elements.push(<hr key={`hr-${idx}`} className="border-t border-slate-200 my-3" />)
      return
    }

    // Header 1 (# )
    if (line.startsWith('# ')) {
      const text = line.replace(/^#\s+/, '')
      elements.push(
        <h3 key={`h1-${idx}`} className="text-base sm:text-lg font-extrabold text-slate-900 pt-2 pb-1 border-b border-slate-200/80 flex items-center gap-2">
          <span className="text-sky-500">✦</span>
          <span>{renderFormattedInline(text)}</span>
        </h3>
      )
      return
    }

    // Header 2 (## )
    if (line.startsWith('## ')) {
      const text = line.replace(/^##\s+/, '')
      elements.push(
        <h4 key={`h2-${idx}`} className="text-sm sm:text-base font-bold text-slate-900 pt-2 pb-0.5 flex items-center gap-1.5">
          <span>{renderFormattedInline(text)}</span>
        </h4>
      )
      return
    }

    // Header 3 (### )
    if (line.startsWith('### ')) {
      const text = line.replace(/^###\s+/, '')
      elements.push(
        <h5 key={`h3-${idx}`} className="text-[13px] sm:text-sm font-semibold text-slate-800 pt-1.5 pb-0.5">
          {renderFormattedInline(text)}
        </h5>
      )
      return
    }

    // Blockquote / Metadata (> )
    if (line.startsWith('>')) {
      const text = line.replace(/^>\s*/, '')
      return elements.push(
        <div key={`bq-${idx}`} className="bg-sky-50/70 border-l-3 border-sky-500 pl-3 py-1.5 rounded-r-lg text-slate-700 text-xs sm:text-[13px] leading-relaxed my-1">
          {renderFormattedInline(text)}
        </div>
      )
    }

    // Bullet point (- or * or +)
    if (line.startsWith('- ') || line.startsWith('* ') || line.startsWith('+ ')) {
      const text = line.replace(/^[-*+]\s+/, '')
      elements.push(
        <div key={`li-${idx}`} className="flex items-start gap-2 pl-1.5 text-xs sm:text-sm text-slate-700 leading-relaxed my-0.5">
          <span className="text-sky-500 font-bold leading-none mt-1.5 shrink-0 text-sm">•</span>
          <span className="flex-1">{renderFormattedInline(text)}</span>
        </div>
      )
      return
    }

    // Numbered list (1. 2. etc)
    const numMatch = line.match(/^(\d+)\.\s+(.*)/)
    if (numMatch) {
      elements.push(
        <div key={`num-${idx}`} className="flex items-start gap-2 pl-1.5 text-xs sm:text-sm text-slate-700 leading-relaxed my-0.5">
          <span className="font-mono text-xs font-semibold text-sky-600 shrink-0 mt-0.5">{numMatch[1]}.</span>
          <span className="flex-1">{renderFormattedInline(numMatch[2])}</span>
        </div>
      )
      return
    }

    // Standard paragraph
    elements.push(
      <p key={`p-${idx}`} className="text-xs sm:text-sm text-slate-700 leading-relaxed">
        {renderFormattedInline(line)}
      </p>
    )
  })

  return <div className="space-y-1">{elements}</div>
}

export const UpdateModal: React.FC<UpdateModalProps> = ({
  isOpen,
  onClose,
  currentVersion = '2.5.0',
  onUpdateSuccess,
}) => {
  const { t } = useI18n()
  const [checking, setChecking] = useState(false)
  const [hasChecked, setHasChecked] = useState(false)
  const [checkError, setCheckError] = useState<string | null>(null)
  const [latestVersion, setLatestVersion] = useState<string>('')
  const [releaseTitle, setReleaseTitle] = useState<string>('')
  const [hasUpdate, setHasUpdate] = useState<boolean>(false)
  const [releaseUrl, setReleaseUrl] = useState<string>('https://github.com/Sagisawa/GBF-Accelerator/releases')
  const [bodyText, setBodyText] = useState<string>('')
  const [sha256, setSha256] = useState<string>('')
  const [publishedAt, setPublishedAt] = useState<string>('')

  // Download & apply states
  const [downloading, setDownloading] = useState(false)
  const [downloadStatus, setDownloadStatus] = useState<UpdateDownloadStatus | null>(null)
  const [applyingState, setApplyingState] = useState<ApplyingState>('idle')
  const [applyingError, setApplyingError] = useState<string | null>(null)
  const [reconnectAttempt, setReconnectAttempt] = useState<number>(0)
  const [openingFolder, setOpeningFolder] = useState<boolean>(false)
  const [openFolderError, setOpenFolderError] = useState<string | null>(null)

  const pollTimerRef = useRef<any>(null)
  const reconnectTimerRef = useRef<any>(null)

  const stopDownloadPolling = () => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current)
      pollTimerRef.current = null
    }
  }

  const stopReconnectPolling = () => {
    if (reconnectTimerRef.current) {
      clearInterval(reconnectTimerRef.current)
      reconnectTimerRef.current = null
    }
  }

  const stopAllTimers = () => {
    stopDownloadPolling()
    stopReconnectPolling()
  }

  const startDownloadPolling = () => {
    stopDownloadPolling()
    pollTimerRef.current = setInterval(async () => {
      try {
        const st = await fetchDownloadStatus()
        setDownloadStatus(st)
        if (st.done || st.error || !st.active) {
          setDownloading(false)
          stopDownloadPolling()
        }
      } catch {
        stopDownloadPolling()
        setDownloading(false)
      }
    }, 1000)
  }

  const startReconnectPolling = (_targetVer?: string) => {
    stopReconnectPolling()
    setApplyingState('restarting')
    setReconnectAttempt(1)

    // Give 1.8s grace period for helper to replace files and server to restart
    setTimeout(() => {
      let attempt = 0
      const maxAttempts = 30 // 30 * 1.5s = 45s max

      reconnectTimerRef.current = setInterval(async () => {
        attempt++
        setReconnectAttempt(attempt)

        try {
          const controller = new AbortController()
          const timeoutId = setTimeout(() => controller.abort(), 1200)
          const res = await fetch('/api/status', {
            signal: controller.signal,
            cache: 'no-store',
          })
          clearTimeout(timeoutId)

          if (res.ok) {
            stopReconnectPolling()
            setApplyingState('success')
            onUpdateSuccess?.()
            setTimeout(() => {
              window.location.reload()
            }, 1500)
            return
          }
        } catch {
          // Still waiting for server to come back up
        }

        if (attempt >= maxAttempts) {
          stopReconnectPolling()
          setApplyingState('timeout')
        }
      }, 1500)
    }, 1800)
  }

  const checkUpdates = async () => {
    setChecking(true)
    setCheckError(null)
    if (applyingState === 'failed') {
      setApplyingState('idle')
    }

    try {
      const data = await checkForUpdate()
      if (data?.error) {
        throw new Error(data.error)
      }
      if (data) {
        setHasUpdate(Boolean(data.has_update))
        setLatestVersion(data.latest_version || currentVersion)
        setReleaseTitle(data.release_title || '')
        setReleaseUrl(data.release_url || data.html_url || 'https://github.com/Sagisawa/GBF-Accelerator/releases')
        setBodyText(data.release_notes || t('modals.update.noDetailedLogs'))
        setSha256(data.sha256 || '')
        setPublishedAt(data.published_at || '')
        setCheckError(null)
      }
    } catch (backendErr: any) {
      // Browser fallback to GitHub API
      try {
        const res = await fetch('https://api.github.com/repos/Sagisawa/GBF-Accelerator/releases/latest')
        if (res.ok) {
          const data = await res.json()
          const tag = (data.tag_name || '').replace(/^v/i, '').trim()
          setLatestVersion(tag)
          setReleaseTitle(data.name || '')
          setReleaseUrl(data.html_url || 'https://github.com/Sagisawa/GBF-Accelerator/releases')
          setBodyText(data.body || t('modals.update.noDetailedLogs'))
          setPublishedAt(data.published_at ? data.published_at.slice(0, 10) : '')
          setHasUpdate(isNewerVersion(tag, currentVersion))
          setCheckError(null)
        } else {
          setHasUpdate(false)
          setLatestVersion('')
          setCheckError(backendErr?.message || t('modals.update.githubResponseAbnormal', { status: res.status }))
          setBodyText(t('modals.update.cannotFetchRelease'))
        }
      } catch (browserErr: any) {
        setHasUpdate(false)
        setLatestVersion('')
        setCheckError(backendErr?.message || browserErr?.message || t('modals.update.checkFailedCannotConnect'))
        setBodyText(t('modals.update.cannotFetchRelease'))
      }
    } finally {
      setChecking(false)
      setHasChecked(true)
    }

    // Inspect existing download status to restore any in-progress or completed download
    try {
      const st = await fetchDownloadStatus()
      if (st.active) {
        setDownloading(true)
        setDownloadStatus(st)
        startDownloadPolling()
      } else if (st.done && !st.error && st.dest) {
        setDownloadStatus(st)
      } else if (st.applying) {
        startReconnectPolling()
      }
    } catch {
      // Ignore background status check failure
    }
  }

  const handleStartDownload = async () => {
    if (downloading || applyingState === 'restarting') return
    setDownloading(true)
    setDownloadStatus(null)

    try {
      // Pass empty url to trigger backend official release validation and set managed=true
      await downloadUpdate('', '', '', latestVersion)
      startDownloadPolling()
    } catch (e: any) {
      setDownloading(false)
      setDownloadStatus({
        ok: false,
        active: false,
        done: false,
        downloaded: 0,
        total: 0,
        percent: 0,
        dest: '',
        version: latestVersion,
        sha256: sha256,
        managed: true,
        applying: false,
        error: e.message || t('modals.update.downloadFailed', { error: '' }),
      })
    }
  }

  const handleCancelDownload = async () => {
    try {
      await cancelDownload()
    } catch {
      // Ignore
    } finally {
      stopDownloadPolling()
      setDownloading(false)
      setDownloadStatus((prev) =>
        prev
          ? { ...prev, error: t('modals.update.downloadCancelled'), active: false }
          : {
              ok: true,
              active: false,
              done: false,
              downloaded: 0,
              total: 0,
              percent: 0,
              dest: '',
              version: latestVersion,
              sha256: sha256,
              managed: true,
              applying: false,
              error: t('modals.update.downloadCancelled'),
            }
      )
    }
  }

  const handleApplyUpdate = async () => {
    if (applyingState === 'preparing' || applyingState === 'restarting' || !downloadStatus?.done) {
      return
    }

    setApplyingState('preparing')
    setApplyingError(null)

    try {
      const res = await applyDownloadedUpdate()
      const targetVer = res?.version || latestVersion
      startReconnectPolling(targetVer)
    } catch (e: any) {
      setApplyingState('failed')
      setApplyingError(e?.message || t('modals.update.autoUpdateFailed', { error: '' }))
    }
  }

  const handleOpenFolder = async () => {
    setOpeningFolder(true)
    setOpenFolderError(null)
    try {
      await openUpdateFolder()
    } catch (err: any) {
      setOpenFolderError(err?.message || t('modals.update.openFolderFailed', { error: '' }))
    } finally {
      setOpeningFolder(false)
    }
  }

  useEffect(() => {
    if (isOpen) {
      checkUpdates()
    } else {
      stopAllTimers()
      setDownloading(false)
    }
    return () => stopAllTimers()
  }, [isOpen])

  const hasNew = Boolean(hasUpdate)
  const isRestarting = applyingState === 'preparing' || applyingState === 'restarting'
  const isReconnectSuccess = applyingState === 'success'
  const isReconnectTimeout = applyingState === 'timeout'
  const isApplyFailed = applyingState === 'failed'

  return (
    <Modal
      isOpen={isOpen}
      onClose={() => {
        if (isRestarting) return // Prevent accidental dismiss while restarting
        onClose()
      }}
      title={
        <div className="flex items-center gap-2.5">
          <div className="w-8 h-8 rounded-lg bg-sky-50 text-sky-600 flex items-center justify-center border border-sky-200/60">
            <ArrowUpCircle className="w-5 h-5" />
          </div>
          <span className="text-base sm:text-lg font-bold text-slate-900">{t('modals.update.title')}</span>
        </div>
      }
      subtitle={t('modals.update.subtitle')}
      maxWidth="max-w-[760px]"
    >
      <div className="space-y-4 text-sm text-slate-700">
        {/* Version Status Hero Banner */}
        <div className="p-4 sm:p-5 bg-gradient-to-r from-slate-50 to-slate-100/80 border border-slate-200/90 rounded-2xl flex items-center justify-between flex-wrap gap-4 shadow-2xs">
          <div className="flex items-start gap-3.5 flex-1 min-w-[280px]">
            <div
              className={`w-12 h-12 rounded-2xl flex items-center justify-center shrink-0 shadow-2xs mt-1 ${
                checking
                  ? 'bg-sky-500 text-white shadow-sky-500/20'
                  : hasNew
                  ? 'bg-amber-500 text-white shadow-amber-500/20'
                  : 'bg-emerald-600 text-white shadow-emerald-500/20'
              }`}
            >
              {checking ? (
                <RefreshCw className="w-6 h-6 animate-spin" />
              ) : hasNew ? (
                <Sparkles className="w-6 h-6" />
              ) : (
                <CheckCircle2 className="w-6 h-6" />
              )}
            </div>
            <div className="space-y-1.5 flex-1 min-w-0">
              <div className="flex items-center gap-2.5 flex-wrap">
                <span className="text-base sm:text-lg font-extrabold text-slate-900 tracking-tight">
                  {checking
                    ? t('modals.update.checking')
                    : hasNew
                    ? t('modals.update.foundNew', { version: latestVersion })
                    : t('modals.update.isLatest', { version: currentVersion })}
                </span>
                {hasNew && !checking && (
                  <span className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-amber-100 text-amber-900 border border-amber-300/80">
                    {t('modals.update.canUpgrade')}
                  </span>
                )}
                {!hasNew && hasChecked && !checking && (
                  <span className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-emerald-100 text-emerald-900 border border-emerald-300/80">
                    {t('modals.update.latestStable')}
                  </span>
                )}
              </div>
              {hasNew && !checking ? (
                <div className="mt-2 p-3 bg-white border border-slate-200/90 border-l-[4px] border-l-amber-500 rounded-xl shadow-2xs space-y-1">
                  <div className="flex items-center gap-2 flex-wrap">
                    <span className="text-[11px] font-bold text-amber-800 bg-amber-50 px-2 py-0.5 rounded border border-amber-200/80">
                      {t('modals.update.positioning')}
                    </span>
                    {publishedAt && (
                      <span className="text-[11px] text-slate-400 font-medium">
                        {t('modals.update.publishedAt', { date: publishedAt })}
                      </span>
                    )}
                  </div>
                  <div className="text-[15px] sm:text-base font-extrabold text-slate-900 leading-snug break-words [word-break:break-word] max-h-40 overflow-y-auto pt-0.5">
                    {releaseTitle || t('modals.update.releaseFallback')}
                  </div>
                </div>
              ) : (
                <p className="text-xs sm:text-sm text-slate-600 leading-snug">
                  {checking
                    ? t('modals.update.fetchingGithub')
                    : t('modals.update.runningStatus')}
                </p>
              )}
            </div>
          </div>

          <div className="flex items-center gap-3 bg-white border border-slate-200/90 px-4 py-2.5 rounded-xl shrink-0 shadow-2xs">
            <div className="text-center">
              <div className="text-[11px] text-slate-500 font-medium">{t('modals.update.currentVersion')}</div>
              <div className="font-mono text-sm sm:text-base font-bold text-slate-700">v{currentVersion}</div>
            </div>
            <span className="text-slate-300 font-bold text-sm">➔</span>
            <div className="text-center">
              <div className="text-[11px] text-slate-500 font-medium">{t('modals.update.latestVersion')}</div>
              {checking ? (
                <div className="flex items-center justify-center gap-1 text-xs text-slate-400 font-mono">
                  <RefreshCw className="w-3 h-3 animate-spin text-sky-500" />
                  <span>{t('common.checking')}</span>
                </div>
              ) : (
                <div className={`font-mono text-sm sm:text-base font-extrabold ${hasNew ? 'text-amber-600' : 'text-emerald-600'}`}>
                  {latestVersion ? `v${latestVersion}` : '--'}
                </div>
              )}
            </div>
          </div>
        </div>

        {/* Security & Integrity Fingerprint Banner (Only when update is available) */}
        {hasNew && !checking && (
          sha256 ? (
            <div className="p-3.5 rounded-xl border bg-sky-50/80 border-sky-200/80 text-sky-900 flex items-start gap-3">
              <ShieldCheck className="w-5 h-5 text-sky-600 shrink-0 mt-0.5" />
              <div className="space-y-1 text-xs sm:text-sm">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="font-bold text-sky-950">{t('modals.update.officialSha256Verified')}</span>
                  <span className="font-mono text-[11px] bg-white px-2 py-0.5 rounded border border-sky-200 text-sky-800">
                    {sha256.length > 20 ? `${sha256.slice(0, 16)}...${sha256.slice(-8)}` : sha256}
                  </span>
                </div>
                <p className="text-sky-700 leading-relaxed text-xs">
                  {t('modals.update.sha256Desc')}
                </p>
              </div>
            </div>
          ) : (
            <div className="p-3.5 rounded-xl border bg-amber-50/80 border-amber-200/80 text-amber-900 flex items-start gap-3">
              <ShieldAlert className="w-5 h-5 text-amber-600 shrink-0 mt-0.5" />
              <div className="space-y-1 text-xs sm:text-sm">
                <span className="font-bold text-amber-950">{t('modals.update.noOfficialSha256')}</span>
                <p className="text-amber-800 leading-relaxed text-xs">
                  {t('modals.update.noSha256Desc')}
                </p>
              </div>
            </div>
          )
        )}

        {/* Check Error Notification */}
        {hasChecked && !checking && checkError && (
          <div className="p-4 rounded-xl border bg-rose-50/80 border-rose-200/80 text-rose-900 flex items-start gap-3">
            <AlertCircle className="w-5 h-5 text-rose-600 shrink-0 mt-0.5" />
            <div className="space-y-1 flex-1">
              <span className="font-bold text-sm">{t('modals.update.checkFailedTitle')}</span>
              <p className="text-xs sm:text-sm text-rose-700 leading-relaxed">
                {t('modals.update.checkFailedDesc', { error: checkError })}
              </p>
              <button
                type="button"
                onClick={checkUpdates}
                className="mt-2 inline-flex items-center gap-1.5 px-3 py-1 rounded-lg bg-white border border-rose-300 text-rose-800 text-xs font-semibold hover:bg-rose-100 transition-all cursor-pointer"
              >
                <RefreshCw className="w-3.5 h-3.5" />
                <span>{t('modals.update.retryCheck')}</span>
              </button>
            </div>
          </div>
        )}

        {/* DEDICATED APPLYING / RESTARTING / TIMEOUT VIEW */}
        {isRestarting || isReconnectSuccess || isReconnectTimeout || isApplyFailed ? (
          <div className="p-6 rounded-2xl border bg-slate-50/95 border-slate-200 text-center space-y-4 py-8 shadow-xs">
            {isRestarting && (
              <div className="space-y-3">
                <div className="w-14 h-14 rounded-2xl bg-sky-100 text-sky-600 flex items-center justify-center mx-auto border border-sky-200">
                  <RefreshCw className="w-7 h-7 animate-spin" />
                </div>
                <div className="space-y-1.5">
                  <h4 className="text-lg font-extrabold text-slate-900">
                    {t('modals.update.restartingTitle')}
                  </h4>
                  <p className="text-xs sm:text-sm text-slate-600 max-w-md mx-auto leading-relaxed">
                    {t('modals.update.restartingDesc')}
                  </p>
                </div>
                <div className="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-sky-50 border border-sky-200/80 text-sky-800 text-xs font-mono font-medium">
                  <span className="w-2 h-2 rounded-full bg-sky-500 animate-ping" />
                  <span>{t('modals.update.reconnectAttempt', { current: reconnectAttempt })}</span>
                </div>
                <p className="text-[11px] text-slate-500 pt-1">
                  {t('modals.update.reconnectTip')}
                </p>
              </div>
            )}

            {isReconnectSuccess && (
              <div className="space-y-3">
                <div className="w-14 h-14 rounded-2xl bg-emerald-100 text-emerald-600 flex items-center justify-center mx-auto border border-emerald-200">
                  <CheckCircle2 className="w-7 h-7" />
                </div>
                <div className="space-y-1">
                  <h4 className="text-lg font-extrabold text-slate-900">
                    {t('modals.update.updateSuccessTitle')}
                  </h4>
                  <p className="text-xs sm:text-sm text-emerald-700">
                    {t('modals.update.updateSuccessDesc')}
                  </p>
                </div>
              </div>
            )}

            {isReconnectTimeout && (
              <div className="space-y-3">
                <div className="w-14 h-14 rounded-2xl bg-amber-100 text-amber-600 flex items-center justify-center mx-auto border border-amber-200">
                  <AlertCircle className="w-7 h-7" />
                </div>
                <div className="space-y-1.5">
                  <h4 className="text-lg font-extrabold text-slate-900">
                    {t('modals.update.waitTimeoutTitle')}
                  </h4>
                  <p className="text-xs sm:text-sm text-slate-600 max-w-md mx-auto leading-relaxed">
                    {t('modals.update.waitTimeoutDesc')}
                  </p>
                </div>
                <div className="flex items-center justify-center gap-3 pt-2">
                  <button
                    type="button"
                    onClick={() => startReconnectPolling(latestVersion)}
                    className="px-4 py-2 rounded-xl bg-sky-600 text-white text-xs font-bold hover:bg-sky-700 transition-all cursor-pointer flex items-center gap-1.5"
                  >
                    <RefreshCw className="w-3.5 h-3.5" />
                    <span>{t('modals.update.retryCheck')}</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => window.location.reload()}
                    className="px-4 py-2 rounded-xl bg-white border border-slate-300 text-slate-700 text-xs font-semibold hover:bg-slate-100 transition-all cursor-pointer"
                  >
                    <span>{t('modals.update.refreshPage')}</span>
                  </button>
                </div>
              </div>
            )}

            {isApplyFailed && (
              <div className="space-y-3">
                <div className="w-14 h-14 rounded-2xl bg-rose-100 text-rose-600 flex items-center justify-center mx-auto border border-rose-200">
                  <XCircle className="w-7 h-7" />
                </div>
                <div className="space-y-1.5">
                  <h4 className="text-lg font-extrabold text-slate-900">
                    {t('modals.update.applyFailedTitle')}
                  </h4>
                  <p className="text-xs sm:text-sm text-rose-700 max-w-md mx-auto leading-relaxed">
                    {applyingError || t('modals.update.applyFailedDesc')}
                  </p>
                </div>
                <div className="pt-2">
                  <button
                    type="button"
                    onClick={() => setApplyingState('idle')}
                    className="px-4 py-2 rounded-xl bg-slate-200 hover:bg-slate-300 text-slate-800 text-xs font-bold transition-all cursor-pointer"
                  >
                    {t('common.back')}
                  </button>
                </div>
              </div>
            )}
          </div>
        ) : (
          /* REGULAR VIEW: DOWNLOAD PROGRESS & RELEASE NOTES */
          <>
            {/* Download Progress & Status Card */}
            {(downloading || downloadStatus) && (
              <div className="p-4 rounded-xl border bg-slate-50/90 border-slate-200/90 space-y-2.5">
                <div className="flex justify-between items-center text-xs sm:text-sm">
                  <span className="font-semibold text-slate-800 flex items-center gap-2">
                    {downloadStatus?.done && !downloadStatus?.error ? (
                      <>
                        <CheckCircle2 className="w-5 h-5 text-emerald-600" />
                        <span className="text-emerald-700 font-bold">{t('modals.update.downloadVerified')}</span>
                      </>
                    ) : downloadStatus?.error ? (
                      <>
                        <XCircle className="w-5 h-5 text-rose-600" />
                        <span className="text-rose-700 font-bold">{t('modals.update.downloadException')}</span>
                      </>
                    ) : (
                      <>
                        <Download className="w-5 h-5 text-sky-600 animate-bounce" />
                        <span>{t('modals.update.downloadingProgress', { percent: downloadStatus?.percent?.toFixed(1) || 0 })}</span>
                      </>
                    )}
                  </span>
                  {downloadStatus && downloadStatus.total > 0 && (
                    <span className="font-mono text-slate-500 text-xs sm:text-sm font-semibold">
                      {formatBytes(downloadStatus.downloaded)} / {formatBytes(downloadStatus.total)}
                    </span>
                  )}
                </div>

                {/* Progress bar */}
                {!downloadStatus?.done && !downloadStatus?.error && (
                  <div className="w-full bg-slate-200 rounded-full h-2.5 overflow-hidden">
                    <div
                      className="bg-sky-600 h-2.5 rounded-full transition-all duration-300"
                      style={{ width: `${Math.min(100, Math.max(0, downloadStatus?.percent || 0))}%` }}
                    />
                  </div>
                )}

                {downloadStatus?.done && !downloadStatus?.error && downloadStatus.dest && (
                  <div className="space-y-2 pt-0.5">
                    <div className="flex items-center justify-between gap-2 bg-emerald-50/80 p-3 rounded-xl border border-emerald-200/60 flex-wrap">
                      <div className="space-y-0.5 min-w-0 flex-1">
                        <div className="text-[11px] font-semibold text-emerald-800">{t('modals.update.savedLocation')}</div>
                        <p className="text-xs text-emerald-900 break-all font-mono">
                          {downloadStatus.dest}
                        </p>
                      </div>
                      <button
                        type="button"
                        onClick={handleOpenFolder}
                        disabled={openingFolder}
                        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-lg bg-white border border-emerald-300 hover:bg-emerald-100 text-emerald-900 text-xs font-semibold shadow-2xs transition-all cursor-pointer shrink-0 active:scale-95"
                      >
                        {openingFolder ? (
                          <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                        ) : (
                          <FolderOpen className="w-3.5 h-3.5 text-emerald-700" />
                        )}
                        <span>{t('common.openFolder')}</span>
                      </button>
                    </div>

                    {openFolderError && (
                      <p className="text-xs text-rose-600 bg-rose-50/80 p-2.5 rounded-xl border border-rose-200/60">
                        {t('modals.update.openFolderFailed', { error: openFolderError })}
                      </p>
                    )}

                    <p className="text-[11px] text-slate-500 px-1">
                      {t('modals.update.replaceTip')}
                    </p>
                  </div>
                )}

                {downloadStatus?.error && (
                  <p className="text-xs sm:text-sm text-rose-600 break-all bg-rose-50/80 p-3 rounded-xl border border-rose-200/60">
                    {downloadStatus.error}
                  </p>
                )}
              </div>
            )}

            {/* Release Notes Section */}
            {bodyText && (
              <div className="space-y-2">
                <div className="flex items-center justify-between text-slate-500 text-xs sm:text-sm font-semibold px-1">
                  <span>{t('modals.update.changelogHeader')}</span>
                  <span className="text-slate-400 font-mono text-xs">
                    v{latestVersion || currentVersion}
                  </span>
                </div>
                <div className="h-[280px] sm:h-[320px] overflow-y-auto p-4 sm:p-5 bg-slate-50/70 border border-slate-200/90 rounded-2xl shadow-2xs">
                  <ReleaseNotesViewer content={bodyText} />
                </div>
              </div>
            )}
          </>
        )}

        {/* Modal Action Footer */}
        <div className="flex items-center justify-between pt-3 border-t border-slate-100 flex-wrap gap-3">
          <a
            href={releaseUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-2 px-4 py-2.5 rounded-xl bg-slate-50 hover:bg-slate-100 hover:text-slate-900 text-slate-700 font-medium text-sm border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
          >
            <ExternalLink className="w-4 h-4" />
            <span>{t('modals.update.githubBtn')}</span>
          </a>

          <div className="flex items-center gap-3">
            {isRestarting ? (
              <button
                type="button"
                disabled
                className="px-6 py-2.5 rounded-xl bg-slate-200 text-slate-500 text-sm font-bold flex items-center gap-2 cursor-not-allowed"
              >
                <RefreshCw className="w-4 h-4 animate-spin" />
                <span>{t('header.processing')}</span>
              </button>
            ) : !hasNew && !checking ? (
              <button
                type="button"
                onClick={checkUpdates}
                className="px-5 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-slate-800 text-sm font-bold transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
              >
                <RefreshCw className="w-4 h-4" />
                <span>{t('common.refresh')}</span>
              </button>
            ) : hasNew && !checking && (
              <>
                {downloading ? (
                  <button
                    type="button"
                    onClick={handleCancelDownload}
                    className="px-5 py-2.5 rounded-xl bg-slate-100 hover:bg-rose-50 text-rose-600 text-sm font-semibold border border-slate-200 hover:border-rose-200 transition-all cursor-pointer flex items-center gap-2"
                  >
                    <XCircle className="w-4 h-4" />
                    <span>{t('common.cancel')}</span>
                  </button>
                ) : downloadStatus?.done && !downloadStatus?.error ? (
                  <>
                    <button
                      type="button"
                      onClick={handleOpenFolder}
                      disabled={openingFolder}
                      className="px-4 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-slate-800 text-sm font-semibold border border-slate-200 transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                    >
                      {openingFolder ? (
                        <RefreshCw className="w-4 h-4 animate-spin" />
                      ) : (
                        <FolderOpen className="w-4 h-4 text-slate-600" />
                      )}
                      <span>{t('common.openFolder')}</span>
                    </button>
                    <button
                      type="button"
                      onClick={handleApplyUpdate}
                      className="px-6 py-2.5 rounded-xl bg-sky-600 hover:bg-sky-700 active:bg-sky-800 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                    >
                      <ArrowUpCircle className="w-4 h-4" />
                      <span>{t('modals.update.applyBtn')}</span>
                    </button>
                  </>
                ) : downloadStatus?.error ? (
                  <button
                    type="button"
                    onClick={handleStartDownload}
                    className="px-6 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                  >
                    <RefreshCw className="w-4 h-4" />
                    <span>{t('modals.update.downloadBtn')}</span>
                  </button>
                ) : sha256 ? (
                  <button
                    type="button"
                    onClick={handleStartDownload}
                    className="px-6 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                  >
                    <Download className="w-4 h-4" />
                    <span>{t('modals.update.downloadBtn')}</span>
                  </button>
                ) : (
                  <a
                    href={releaseUrl}
                    target="_blank"
                    rel="noreferrer"
                    className="px-6 py-2.5 rounded-xl bg-sky-600 hover:bg-sky-700 active:bg-sky-800 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                  >
                    <ExternalLink className="w-4 h-4" />
                    <span>{t('modals.update.githubBtn')}</span>
                  </a>
                )}
              </>
            )}

            {!isRestarting && (
              <button
                type="button"
                onClick={onClose}
                className="px-5 py-2.5 rounded-xl bg-slate-50 hover:bg-slate-100 hover:text-slate-900 text-slate-700 text-sm font-medium border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
              >
                {t('common.closeEsc')}
              </button>
            )}
          </div>
        </div>
      </div>
    </Modal>
  )
}
