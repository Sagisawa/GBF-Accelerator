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
} from 'lucide-react'
import { checkForUpdate, downloadUpdate, fetchDownloadStatus, cancelDownload, applyDownloadedUpdate } from '../../api'
import { isNewerVersion } from '../../utils/version'

export interface UpdateModalProps {
  isOpen: boolean
  onClose: () => void
  currentVersion?: string
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
}) => {
  const [checking, setChecking] = useState(false)
  const [latestVersion, setLatestVersion] = useState<string>('')
  const [hasUpdate, setHasUpdate] = useState<boolean>(false)
  const [checkError, setCheckError] = useState<string | null>(null)
  const [releaseUrl, setReleaseUrl] = useState<string>('https://github.com/Sagisawa/GBF-Accelerator/releases')
  const [assetDownloadUrl, setAssetDownloadUrl] = useState<string>('')
  const [assetSHA256, setAssetSHA256] = useState<string>('')
  const [bodyText, setBodyText] = useState<string>('')
  const [hasChecked, setHasChecked] = useState(false)

  // Download state
  const [downloading, setDownloading] = useState(false)
  const [applying, setApplying] = useState(false)
  const [downloadProgress, setDownloadProgress] = useState<{
    percent: number
    downloaded: number
    total: number
    dest: string
    done: boolean
    error: string
  } | null>(null)
  const pollTimerRef = useRef<any>(null)

  const stopPolling = () => {
    if (pollTimerRef.current) {
      clearInterval(pollTimerRef.current)
      pollTimerRef.current = null
    }
  }

  const checkUpdates = async () => {
    setChecking(true)
    setCheckError(null)
    try {
      const data = await checkForUpdate()
      if (data?.error) {
        throw new Error(data.error)
      }
      if (data) {
        setHasUpdate(Boolean(data.has_update))
        setLatestVersion(data.latest_version || currentVersion)
        setReleaseUrl(data.release_url || data.html_url || 'https://github.com/Sagisawa/GBF-Accelerator/releases')
        setBodyText(data.release_notes || '暂无详细更新日志。')
        setAssetDownloadUrl(data.asset_download_url || data.download_url || '')
        setAssetSHA256(data.sha256 || '')
        setCheckError(null)
      } else {
        setHasUpdate(false)
        setLatestVersion(currentVersion)
        setBodyText('已连接到当前稳定版。')
        setCheckError(null)
      }
    } catch (backendErr: any) {
      try {
        const res = await fetch('https://api.github.com/repos/Sagisawa/GBF-Accelerator/releases/latest')
        if (res.ok) {
          const data = await res.json()
          const tag = (data.tag_name || '').replace(/^v/, '').trim()
          setLatestVersion(tag)
          setReleaseUrl(data.html_url || 'https://github.com/Sagisawa/GBF-Accelerator/releases')
          setBodyText(data.body || '暂无详细更新日志。')
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
  }

  const handleStartDownload = async () => {
    setDownloading(true)
    setDownloadProgress(null)
    try {
      // Let the control plane re-check the latest release and bind the downloaded
      // archive to its server-side checksum/version before an apply is possible.
      await downloadUpdate('', '', '', latestVersion)
      pollTimerRef.current = setInterval(async () => {
        try {
          const st = await fetchDownloadStatus()
          setDownloadProgress({
            percent: st.percent || 0,
            downloaded: st.downloaded || 0,
            total: st.total || 0,
            dest: st.dest || '',
            done: st.done,
            error: st.error || '',
          })
          if (st.done || st.error || !st.active) {
            setDownloading(false)
            stopPolling()
          }
        } catch {
          stopPolling()
          setDownloading(false)
        }
      }, 800)
    } catch (e: any) {
      setDownloading(false)
      setDownloadProgress({
        percent: 0,
        downloaded: 0,
        total: 0,
        dest: '',
        done: false,
        error: e.message || '启动下载失败',
      })
    }
  }

  const handleApplyUpdate = async () => {
    if (applying || !downloadProgress?.done) return
    setApplying(true)
    try {
      await applyDownloadedUpdate()
      setDownloadProgress((prev) => (prev ? { ...prev, error: '' } : prev))
      window.setTimeout(() => window.location.reload(), 2500)
    } catch (e: any) {
      setApplying(false)
      setDownloadProgress((prev) =>
        prev
          ? { ...prev, error: e?.message || '启动自动更新失败' }
          : {
              percent: 100,
              downloaded: 0,
              total: 0,
              dest: '',
              done: true,
              error: e?.message || '启动自动更新失败',
            }
      )
    }
  }

  const handleCancelDownload = async () => {
    try {
      await cancelDownload()
    } finally {
      stopPolling()
      setDownloading(false)
      setDownloadProgress((prev) => (prev ? { ...prev, error: '用户已取消下载' } : null))
    }
  }

  useEffect(() => {
    if (isOpen) {
      checkUpdates()
    } else {
      stopPolling()
      setDownloading(false)
    }
    return () => stopPolling()
  }, [isOpen])

  const hasNew = Boolean(hasUpdate)

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
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
        {/* High-Impact Unified Version Hero Banner */}
        <div className="p-4 sm:p-5 bg-gradient-to-r from-slate-50 to-slate-100/80 border border-slate-200/90 rounded-2xl flex items-center justify-between flex-wrap gap-4 shadow-2xs">
          <div className="flex items-center gap-3.5">
            <div className={`w-12 h-12 rounded-2xl flex items-center justify-center shrink-0 shadow-2xs ${
              hasNew ? 'bg-amber-500 text-white shadow-amber-500/20' : 'bg-emerald-600 text-white shadow-emerald-500/20'
            }`}>
              {hasNew ? <Sparkles className="w-6 h-6" /> : <CheckCircle2 className="w-6 h-6" />}
            </div>
            <div className="space-y-1">
              <div className="flex items-center gap-2.5 flex-wrap">
                <span className="text-base sm:text-lg font-extrabold text-slate-900 tracking-tight">
                  {hasNew ? `发现新版本 v${latestVersion}` : `当前已是最新版本 (v${currentVersion})`}
                </span>
                {hasNew && (
                  <span className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-amber-100 text-amber-900 border border-amber-300/80">
                    可升级
                  </span>
                )}
              </div>
              <p className="text-xs sm:text-sm text-slate-600 leading-snug">
                {hasNew
                  ? '官方已发布新的性能优化与协议增强，建议升级以获得最佳加速体验。'
                  : '本地运行的核心加速代理服务与静态缓存模块均处于最优状态。'}
              </p>
            </div>
          </div>

          <div className="flex items-center gap-3 bg-white border border-slate-200/90 px-4 py-2.5 rounded-xl shrink-0 shadow-2xs">
            <div className="text-center">
              <div className="text-[11px] text-slate-500 font-medium">当前安装</div>
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

        {/* Error State if Check Failed */}
        {hasChecked && !checking && checkError && (
          <div className="p-4 rounded-xl border bg-rose-50/80 border-rose-200/80 text-rose-900 flex items-start gap-3">
            <AlertCircle className="w-5 h-5 text-rose-600 shrink-0 mt-0.5" />
            <div className="space-y-0.5">
              <span className="font-bold text-sm">检查更新失败</span>
              <p className="text-xs sm:text-sm text-rose-700 leading-relaxed">
                {checkError}。请检查本地网络或上游代理配置，亦可直接访问下方 GitHub 链接。
              </p>
            </div>
          </div>
        )}

        {/* Download Progress Banner */}
        {downloadProgress && (
          <div className="p-4 rounded-xl border bg-slate-50/90 border-slate-200/90 space-y-2.5">
            <div className="flex justify-between items-center text-xs sm:text-sm">
              <span className="font-semibold text-slate-800 flex items-center gap-2">
                {downloadProgress.done ? (
                  <>
                    <CheckCircle2 className="w-5 h-5 text-emerald-600" />
                    <span className="text-emerald-700 font-bold">下载完成并校验通过</span>
                  </>
                ) : downloadProgress.error ? (
                  <>
                    <XCircle className="w-5 h-5 text-rose-600" />
                    <span className="text-rose-700 font-bold">下载遇到异常</span>
                  </>
                ) : (
                  <>
                    <Download className="w-5 h-5 text-sky-600 animate-bounce" />
                    <span>正在通过上游下载更新安装包 ({downloadProgress.percent}%)</span>
                  </>
                )}
              </span>
              {downloadProgress.total > 0 && (
                <span className="font-mono text-slate-500 text-xs sm:text-sm font-semibold">
                  {(downloadProgress.downloaded / 1024 / 1024).toFixed(1)} /{' '}
                  {(downloadProgress.total / 1024 / 1024).toFixed(1)} MB
                </span>
              )}
            </div>

            {/* Progress bar */}
            {!downloadProgress.done && !downloadProgress.error && (
              <div className="w-full bg-slate-200 rounded-full h-2.5 overflow-hidden">
                <div
                  className="bg-sky-600 h-2.5 rounded-full transition-all duration-300"
                  style={{ width: `${Math.min(100, Math.max(0, downloadProgress.percent))}%` }}
                />
              </div>
            )}

            {downloadProgress.done && downloadProgress.dest && (
              <p className="text-xs sm:text-sm text-emerald-700 break-all font-mono bg-emerald-50/80 p-3 rounded-xl border border-emerald-200/60">
                安装包已保存至: {downloadProgress.dest}
              </p>
            )}

            {downloadProgress.error && (
              <p className="text-xs sm:text-sm text-rose-600 break-all bg-rose-50/80 p-3 rounded-xl border border-rose-200/60">
                {downloadProgress.error}
              </p>
            )}
          </div>
        )}

        {/* Generous Release Notes Styled Viewer */}
        {bodyText && (
          <div className="space-y-2">
            <div className="flex items-center justify-between text-slate-500 text-xs sm:text-sm font-semibold px-1">
              <span>详细更新说明 (Release Notes)</span>
              <span className="text-slate-400 font-mono text-xs">v{latestVersion || currentVersion}</span>
            </div>
            <div className="h-[340px] sm:h-[380px] overflow-y-auto p-4 sm:p-5 bg-slate-50/70 border border-slate-200/90 rounded-2xl shadow-2xs">
              <ReleaseNotesViewer content={bodyText} />
            </div>
          </div>
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
            {hasNew && !downloadProgress?.done && (
              downloading ? (
                <button
                  type="button"
                  onClick={handleCancelDownload}
                  className="px-5 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-rose-600 text-sm font-semibold border border-slate-200 transition-all cursor-pointer flex items-center gap-2"
                >
                  <XCircle className="w-4 h-4" />
                  <span>取消下载</span>
                </button>
              ) : (
                <button
                  type="button"
                  onClick={handleStartDownload}
                  disabled={!assetDownloadUrl && !latestVersion}
                  className="px-6 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 disabled:bg-slate-300 disabled:cursor-not-allowed text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
                >
                  <Download className="w-4 h-4" />
                  <span>一键下载新版安装包</span>
                </button>
              )
            )}

            {hasNew && downloadProgress?.done && !downloadProgress?.error && (
              <button
                type="button"
                onClick={handleApplyUpdate}
                disabled={applying}
                className="px-6 py-2.5 rounded-xl bg-sky-600 hover:bg-sky-700 active:bg-sky-800 disabled:bg-slate-300 text-white text-sm font-bold shadow-xs transition-all cursor-pointer flex items-center gap-2 active:scale-[0.98]"
              >
                {applying ? (
                  <RefreshCw className="w-4 h-4 animate-spin" />
                ) : (
                  <ArrowUpCircle className="w-4 h-4" />
                )}
                <span>{applying ? '正在重启更新…' : '立即更新并重启'}</span>
              </button>
            )}

            <button
              type="button"
              onClick={onClose}
              className="px-5 py-2.5 rounded-xl bg-slate-50 hover:bg-slate-100 hover:text-slate-900 text-slate-700 text-sm font-medium border border-slate-200/90 active:scale-[0.98] transition-all cursor-pointer shadow-2xs"
            >
              关闭 (Esc)
            </button>
          </div>
        </div>
      </div>
    </Modal>
  )
}
