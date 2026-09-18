import React, { useState, useEffect } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { RefreshCw, ExternalLink, CheckCircle2, AlertCircle } from 'lucide-react'

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
  const [bodyText, setBodyText] = useState<string>('')
  const [hasChecked, setHasChecked] = useState(false)

  const checkUpdates = async () => {
    setChecking(true)
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
      setBodyText('未能从 GitHub 获取最新版本信息，请检查网络连接或直接访问 Releases 页面。')
    } finally {
      setChecking(false)
      setHasChecked(true)
    }
  }

  useEffect(() => {
    if (isOpen) {
      checkUpdates()
    }
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
                建议前往 GitHub 下载最新发行版压缩包以获得最新的性能优化与稳定性提升。
              </p>
            ) : (
              <p className="text-[11px] text-emerald-700">
                当前运行的核心加速代理服务与静态缓存模块处于最新状态。
              </p>
            )}
          </div>
        )}

        {bodyText && (
          <div className="max-h-32 overflow-y-auto p-2 bg-slate-50 border border-slate-200 rounded text-[11px] text-slate-600 font-mono whitespace-pre-wrap leading-relaxed">
            {bodyText}
          </div>
        )}

        <div className="flex items-center justify-between pt-1">
          <a
            href={releaseUrl}
            target="_blank"
            rel="noreferrer"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-blue-600 hover:bg-blue-700 text-white font-medium text-xs transition-colors"
          >
            <ExternalLink className="w-3.5 h-3.5" />
            <span>打开 GitHub 发行版</span>
          </a>

          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
