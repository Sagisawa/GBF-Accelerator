import React, { useState, useEffect, useRef } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { RefreshCw, ExternalLink, CheckCircle2, AlertCircle, Download, XCircle } from 'lucide-react'
import { checkForUpdate, downloadUpdate, fetchDownloadStatus, cancelDownload } from '../../api'

export interface UpdateModalProps {
  isOpen: boolean
  onClose: () => void
  currentVersion?: string
}

export const UpdateModal: React.FC<UpdateModalProps> = ({
  isOpen,
  onClose,
  currentVersion = '1.8.0',
}) => {
  const [checking, setChecking] = useState(false)
  const [latestVersion, setLatestVersion] = useState<string>('')
  const [releaseUrl, setReleaseUrl] = useState<string>('https://github.com/Sagisawa/GBF-Accelerator/releases')
  const [assetDownloadUrl, setAssetDownloadUrl] = useState<string>('')
  const [bodyText, setBodyText] = useState<string>('')
  const [hasChecked, setHasChecked] = useState(false)

  // Download state
  const [downloading, setDownloading] = useState(false)
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
    try {
      const data = await checkForUpdate()
      if (data) {
        setLatestVersion(data.latest_version || currentVersion)
        setReleaseUrl(data.release_url || 'https://github.com/Sagisawa/GBF-Accelerator/releases')
        setBodyText(data.release_notes || '暂无详细更新日志。')
        setAssetDownloadUrl(data.asset_download_url || '')
      } else {
        setLatestVersion(currentVersion)
        setBodyText('已连接到当前稳定版。')
      }
    } catch {
      // Fallback: direct browser fetch to GitHub
      try {
        const res = await fetch('https://api.github.com/repos/Sagisawa/GBF-Accelerator/releases/latest')
        if (res.ok) {
          const data = await res.json()
          const tag = (data.tag_name || '').replace(/^v/, '')
          setLatestVersion(tag)
          setReleaseUrl(data.html_url || 'https://github.com/Sagisawa/GBF-Accelerator/releases')
          setBodyText(data.body || '暂无详细更新日志。')
        } else {
          setLatestVersion(currentVersion)
          setBodyText('已连接到当前稳定版。')
        }
      } catch {
        setLatestVersion(currentVersion)
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
      await downloadUpdate(assetDownloadUrl)
      // Start polling status
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

  const hasNew = latestVersion && latestVersion !== currentVersion

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <RefreshCw className="w-4 h-4 text-blue-600" />
          <span>检查软件版本更新</span>
        </div>
      }
      subtitle="对比本地版本与 GitHub Releases 最新正式版本"
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2">
          <div className="flex items-center justify-between">
            <span className="font-semibold text-slate-700">当前安装版本：</span>
            <span className="font-mono font-bold text-slate-900 bg-white px-2 py-0.5 rounded border border-slate-200">
              v{currentVersion}
            </span>
          </div>

          <div className="flex items-center justify-between">
            <span className="font-semibold text-slate-700">云端最新版本：</span>
            {checking ? (
              <span className="flex items-center gap-1 text-slate-500">
                <RefreshCw className="w-3 h-3 animate-spin" />
                <span>查询中...</span>
              </span>
            ) : (
              <span className="font-mono font-bold text-blue-600 bg-white px-2 py-0.5 rounded border border-slate-200">
                {latestVersion ? `v${latestVersion}` : '--'}
              </span>
            )}
          </div>
        </div>

        {hasChecked && !checking && (
          <div
            className={`p-3 rounded-lg border ${
              hasNew
                ? 'bg-amber-50 border-amber-200 text-amber-900'
                : 'bg-emerald-50 border-emerald-200 text-emerald-900'
            } space-y-1.5`}
          >
            <div className="font-bold flex items-center gap-1.5">
              {hasNew ? (
                <>
                  <AlertCircle className="w-4 h-4 text-amber-600" />
                  <span>发现新版本 v{latestVersion}</span>
                </>
              ) : (
                <>
                  <CheckCircle2 className="w-4 h-4 text-emerald-600" />
                  <span>当前已是最新版本 (v{currentVersion})</span>
                </>
              )}
            </div>
            {hasNew ? (
              <p className="text-[11px] text-amber-800">
                建议升级以获得最新的性能优化、协议修复与稳定性提升。
              </p>
            ) : (
              <p className="text-[11px] text-emerald-700">
                当前运行的核心加速代理服务与静态缓存模块处于最新状态。
              </p>
            )}
          </div>
        )}

        {/* Download Progress / Result Banner */}
        {downloadProgress && (
          <div className="p-3 rounded-lg border bg-slate-50 border-slate-200 space-y-2">
            <div className="flex justify-between items-center text-[11px]">
              <span className="font-semibold text-slate-800">
                {downloadProgress.done
                  ? '✅ 下载完成并校验通过'
                  : downloadProgress.error
                  ? '❌ 下载遇到异常'
                  : `⬇️ 正在通过上游下载中 (${downloadProgress.percent}%)`}
              </span>
              {downloadProgress.total > 0 && (
                <span className="font-mono text-slate-500">
                  {(downloadProgress.downloaded / 1024 / 1024).toFixed(1)} /{' '}
                  {(downloadProgress.total / 1024 / 1024).toFixed(1)} MB
                </span>
              )}
            </div>

            {/* Progress bar */}
            {!downloadProgress.done && !downloadProgress.error && (
              <div className="w-full bg-slate-200 rounded-full h-1.5 overflow-hidden">
                <div
                  className="bg-blue-600 h-1.5 rounded-full transition-all duration-300"
                  style={{ width: `${Math.min(100, Math.max(0, downloadProgress.percent))}%` }}
                />
              </div>
            )}

            {downloadProgress.done && downloadProgress.dest && (
              <p className="text-[11px] text-emerald-700 break-all font-mono">
                文件已保存至: {downloadProgress.dest}
              </p>
            )}

            {downloadProgress.error && (
              <p className="text-[11px] text-red-600 break-all">
                {downloadProgress.error}
              </p>
            )}
          </div>
        )}

        {bodyText && (
          <div className="max-h-32 overflow-y-auto p-2 bg-slate-50 border border-slate-200 rounded text-[11px] text-slate-600 font-mono whitespace-pre-wrap leading-relaxed">
            {bodyText}
          </div>
        )}

        <div className="flex items-center justify-between pt-1 flex-wrap gap-2">
          <div className="flex items-center gap-2">
            <a
              href={releaseUrl}
              target="_blank"
              rel="noreferrer"
              className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-slate-100 hover:bg-slate-200 text-slate-700 font-medium text-xs border border-slate-300 transition-colors"
            >
              <ExternalLink className="w-3.5 h-3.5" />
              <span>GitHub</span>
            </a>

            {hasNew && !downloadProgress?.done && (
              downloading ? (
                <Button
                  variant="desktop"
                  size="sm"
                  onClick={handleCancelDownload}
                  icon={<XCircle className="w-3.5 h-3.5 text-red-600" />}
                >
                  取消下载
                </Button>
              ) : (
                <Button
                  variant="primary"
                  size="sm"
                  onClick={handleStartDownload}
                  icon={<Download className="w-3.5 h-3.5" />}
                >
                  一键下载新版
                </Button>
              )
            )}
          </div>

          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
