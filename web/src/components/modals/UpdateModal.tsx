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
  RotateCcw,
} from 'lucide-react'
import {
  checkForUpdate,
  downloadUpdate,
  fetchDownloadStatus,
  cancelDownload,
  applyDownloadedUpdate,
} from '../../api'
import { UpdateDownloadStatus } from '../../types'
import { isNewerVersion } from '../../utils/version'

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
  currentVersion = '2.0.0',
  onUpdateSuccess,
}) => {
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
        setBodyText(data.release_notes || '暂无详细更新日志。')
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
          setBodyText(data.body || '暂无详细更新日志。')
          setPublishedAt(data.published_at ? data.published_at.slice(0, 10) : '')
          setHasUpdate(isNewerVersion(tag, currentVersion))
          setCheckError(null)
        } else {
          setHasUpdate(false)
          setLatestVersion('')
          setCheckError(backendErr?.message || `GitHub API 响应异常 (HTTP ${res.status})`)
          setBodyText('未能获取最新版本信息，请检查网络连接或直接访问 Releases 页面。')
        }
      } catch (browserErr: any) {
        setHasUpdate(false)
        setLatestVersion('')
        setCheckError(backendErr?.message || browserErr?.message || '网络请求失败，未能连接到 GitHub Releases')
        setBodyText('未能获取最新版本信息，请检查网络连接或直接访问 Releases 页面。')
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
        error: e.message || '启动下载任务失败',
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
          ? { ...prev, error: '用户已取消下载', active: false }
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
              error: '用户已取消下载',
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
      setApplyingError(e?.message || '启动自动更新失败')
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
          <span className="text-base sm:text-lg font-bold text-slate-900">软件版本检查与更新</span>
        </div>
      }
      subtitle="对比本地引擎版本与 GitHub Releases 最新正式发布"
      maxWidth="max-w-[760px]"
    >
      <div className="space-y-4 text-sm text-slate-700">
        {/* Version Status Hero Banner */}
        <div className="p-4 sm:p-5 bg-gradient-to-r from-slate-50 to-slate-100/80 border border-slate-200/90 rounded-2xl flex items-center justify-between flex-wrap gap-4 shadow-2xs">
          <div className="flex items-center gap-3.5">
            <div
              className={`w-12 h-12 rounded-2xl flex items-center justify-center shrink-0 shadow-2xs ${
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
            <div className="space-y-1">
              <div className="flex items-center gap-2.5 flex-wrap">
                <span className="text-base sm:text-lg font-extrabold text-slate-900 tracking-tight">
                  {checking
                    ? '正在检查新版本...'
                    : hasNew
                    ? `发现新版本 v${latestVersion}`
                    : `当前已是最新版本 (v${currentVersion})`}
                </span>
                {hasNew && !checking && (
                  <span className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-amber-100 text-amber-900 border border-amber-300/80">
                    可升级
                  </span>
                )}
                {!hasNew && hasChecked && !checking && (
                  <span className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-emerald-100 text-emerald-900 border border-emerald-300/80">
                    最新稳定版
                  </span>
                )}
              </div>
              <p className="text-xs sm:text-sm text-slate-600 leading-snug">
                {checking
                  ? '正在连接 GitHub 获取最新版本信息与安全指纹...'
                  : hasNew
                  ? releaseTitle || '官方已发布新的性能优化与协议增强，建议升级以获得最佳加速体验。'
                  : '本地运行的核心加速代理服务与静态缓存模块均处于最优状态。'}
              </p>
              {publishedAt && hasNew && !checking && (
                <p className="text-[11px] text-slate-500">发布日期: {publishedAt}</p>
              )}
            </div>
          </div>

          <div className="flex items-center gap-3 bg-white border border-slate-200/90 px-4 py-2.5 rounded-xl shrink-0 shadow-2xs">
            <div className="text-center">
              <div className="text-[11px] text-slate-500 font-medium">当前运行</div>
              <div className="font-mono text-sm sm:text-base font-bold text-slate-700">v{currentVersion}</div>
            </div>
            <span className="text-slate-300 font-bold text-sm">➔</span>
            <div className="text-center">
              <div className="text-[11px] text-slate-500 font-medium">云端最新</div>
              {checking ? (
                <div className="flex items-center justify-center gap-1 text-xs text-slate-400 font-mono">
                  <RefreshCw className="w-3 h-3 animate-spin text-sky-500" />
                  <span>查询中</span>
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
                  <span className="font-bold text-sky-950">官方 SHA-256 完整性指纹校验已就绪</span>
                  <span className="font-mono text-[11px] bg-white px-2 py-0.5 rounded border border-sky-200 text-sky-800">
                    {sha256.length > 20 ? `${sha256.slice(0, 16)}...${sha256.slice(-8)}` : sha256}
                  </span>
                </div>
                <p className="text-sky-700 leading-relaxed text-xs">
                  自动更新程序将在下载后自动比对 SHA-256 校验和并验证安装包归档完整性，杜绝篡改与不完整下载。
                </p>
              </div>
            </div>
          ) : (
            <div className="p-3.5 rounded-xl border bg-amber-50/80 border-amber-200/80 text-amber-900 flex items-start gap-3">
              <ShieldAlert className="w-5 h-5 text-amber-600 shrink-0 mt-0.5" />
              <div className="space-y-1 text-xs sm:text-sm">
                <span className="font-bold text-amber-950">未检测到官方 SHA-256 指纹</span>
                <p className="text-amber-800 leading-relaxed text-xs">
                  出于工程安全治理要求，自动应用更新严格限定拥有 SHA-256 校验值的版本。当前 Release 暂未声明哈希，一键自动更新已被保护性限制，建议直接访问下方 GitHub Releases 页面手动下载。
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
              <span className="font-bold text-sm">检查更新失败</span>
              <p className="text-xs sm:text-sm text-rose-700 leading-relaxed">
                {checkError}。请检查本地网络或上游代理配置，亦可直接通过下方链接手动下载。
              </p>
              <button
                type="button"
                onClick={checkUpdates}
                className="mt-2 inline-flex items-center gap-1.5 px-3 py-1 rounded-lg bg-white border border-rose-300 text-rose-800 text-xs font-semibold hover:bg-rose-100 transition-all cursor-pointer"
              >
                <RefreshCw className="w-3.5 h-3.5" />
                <span>重新检查</span>
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
                    正在应用更新并重启服务...
                  </h4>
                  <p className="text-xs sm:text-sm text-slate-600 max-w-md mx-auto leading-relaxed">
                    更新助手已启动，正在解压安装包并安全替换程序文件。主服务即将自动重新连接，请稍候。
                  </p>
                </div>
                <div className="inline-flex items-center gap-2 px-3.5 py-1.5 rounded-full bg-sky-50 border border-sky-200/80 text-sky-800 text-xs font-mono font-medium">
                  <span className="w-2 h-2 rounded-full bg-sky-500 animate-ping" />
                  <span>等待服务就绪 (第 {reconnectAttempt}/30 次检测)...</span>
                </div>
                <p className="text-[11px] text-slate-500 pt-1">
                  提示：更新期间请勿强制关闭程序。若操作系统弹出安全或防火墙确认，请选择允许。
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
                    更新完成！程序已重新就绪
                  </h4>
                  <p className="text-xs sm:text-sm text-emerald-700">
                    核心引擎与静态缓存模块已成功升级，页面即将自动刷新...
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
                    服务重启等待超时
                  </h4>
                  <p className="text-xs sm:text-sm text-slate-600 max-w-md mx-auto leading-relaxed">
                    超过 45 秒未能自动连通服务。更新程序可能已完成替换但受系统杀毒软件阻滞，或正在后台完成自启动。
                  </p>
                </div>
                <div className="flex items-center justify-center gap-3 pt-2">
                  <button
                    type="button"
                    onClick={() => startReconnectPolling(latestVersion)}
                    className="px-4 py-2 rounded-xl bg-sky-600 text-white text-xs font-bold hover:bg-sky-700 transition-all cursor-pointer flex items-center gap-1.5"
                  >
                    <RefreshCw className="w-3.5 h-3.5" />
                    <span>再次尝试检测</span>
                  </button>
                  <button
                    type="button"
                    onClick={() => window.location.reload()}
                    className="px-4 py-2 rounded-xl bg-white border border-slate-300 text-slate-700 text-xs font-semibold hover:bg-slate-100 transition-all cursor-pointer"
                  >
                    <span>刷新页面</span>
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
                    启动更新失败
                  </h4>
                  <p className="text-xs sm:text-sm text-rose-700 max-w-md mx-auto leading-relaxed">
                    {applyingError || '应用更新过程中遇到未知错误，请检查日志或手动下载。'}
                  </p>
                </div>
                <div className="pt-2">
                  <button
                    type="button"
                    onClick={() => setApplyingState('idle')}
                    className="px-4 py-2 rounded-xl bg-slate-200 hover:bg-slate-300 text-slate-800 text-xs font-bold transition-all cursor-pointer"
                  >
                    返回
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
                        <span className="text-emerald-700 font-bold">下载完成，归档与 SHA-256 校验通过</span>
                      </>
                    ) : downloadStatus?.error ? (
                      <>
                        <XCircle className="w-5 h-5 text-rose-600" />
                        <span className="text-rose-700 font-bold">下载或校验遇到异常</span>
                      </>
                    ) : (
                      <>
                        <Download className="w-5 h-5 text-sky-600 animate-bounce" />
                        <span>正在通过上游下载更新安装包 ({downloadStatus?.percent?.toFixed(1) || 0}%)</span>
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
                  <div className="space-y-1">
                    <p className="text-xs text-emerald-700 break-all font-mono bg-emerald-50/80 p-2.5 rounded-xl border border-emerald-200/60">
                      安装包已就绪: {downloadStatus.dest}
                    </p>
                    <p className="text-[11px] text-slate-500 px-1">
                      点击下方『立即更新并重启』将启动更新助手完成文件平滑替换并自动重启服务。
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
                  <span>详细更新说明 (Release Notes)</span>
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
            <span>前往 GitHub Release 网页</span>
          </a>

          <div className="flex items-center gap-3">
            {isRestarting ? (
              <button
                type="button"
                disabled
                className="px-6 py-2.5 rounded-xl bg-slate-200 text-slate-500 text-sm font-bold flex items-center gap-2 cursor-not-allowed"
              >
                <RefreshCw className="w-4 h-4 animate-spin" />
                <span>正在重启服务…</span>
              </button>
            ) : !hasNew && !checking ? (
              <button
                type="button"
                onClick={checkUpdates}
                className="px-5 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-slate-800 text-sm font-bold transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
              >
                <RefreshCw className="w-4 h-4" />
                <span>重新检查</span>
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
                    <span>取消下载</span>
                  </button>
                ) : downloadStatus?.done && !downloadStatus?.error ? (
                  <>
                    <button
                      type="button"
                      onClick={handleStartDownload}
                      className="px-4 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-slate-700 text-sm font-semibold transition-all cursor-pointer flex items-center gap-1.5"
                    >
                      <RotateCcw className="w-3.5 h-3.5" />
                      <span>重新下载</span>
                    </button>
                    <button
                      type="button"
                      onClick={handleApplyUpdate}
                      className="px-6 py-2.5 rounded-xl bg-sky-600 hover:bg-sky-700 active:bg-sky-800 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                    >
                      <ArrowUpCircle className="w-4 h-4" />
                      <span>立即更新并重启</span>
                    </button>
                  </>
                ) : downloadStatus?.error ? (
                  <button
                    type="button"
                    onClick={handleStartDownload}
                    className="px-6 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                  >
                    <RefreshCw className="w-4 h-4" />
                    <span>重试下载安装包</span>
                  </button>
                ) : sha256 ? (
                  <button
                    type="button"
                    onClick={handleStartDownload}
                    className="px-6 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                  >
                    <Download className="w-4 h-4" />
                    <span>一键下载新版安装包</span>
                  </button>
                ) : (
                  <a
                    href={releaseUrl}
                    target="_blank"
                    rel="noreferrer"
                    className="px-6 py-2.5 rounded-xl bg-sky-600 hover:bg-sky-700 active:bg-sky-800 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                  >
                    <ExternalLink className="w-4 h-4" />
                    <span>前往网页手动下载</span>
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
                关闭 (Esc)
              </button>
            )}
          </div>
        </div>
      </div>
    </Modal>
  )
}
