import React, { useState, useEffect } from 'react'
import { RuntimeStatus, CacheStats } from '../../types'
import { Input } from '../common/Input'
import { Button } from '../common/Button'
import { HardDrive, FolderOpen, ExternalLink, Stethoscope, Sparkles, Save } from 'lucide-react'
import { browseDirectory, openCacheFolder, auditCache, slimCache, applyConfig } from '../../api'

export interface CacheDeckProps {
  status: RuntimeStatus | null
  cacheStats: CacheStats | null
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const CacheDeck: React.FC<CacheDeckProps> = ({
  status,
  cacheStats,
  onRefresh,
  onToast,
}) => {
  const currentDir = status?.cache.cache_dir || cacheStats?.cache_base || ''
  const [pathInput, setPathInput] = useState<string>(currentDir)
  const [busy, setBusy] = useState<boolean>(false)

  useEffect(() => {
    if (currentDir && (!pathInput || !isDirty)) {
      setPathInput(currentDir)
    }
  }, [currentDir])

  const isDirty = pathInput.trim() !== currentDir.trim() && pathInput.trim() !== ''

  const handleSaveDir = async (newPath?: string) => {
    const targetPath = (newPath ?? pathInput).trim()
    if (!targetPath) return
    setBusy(true)
    try {
      await applyConfig({ cache_dir: targetPath })
      onToast(`缓存路径已更新: ${targetPath}`, 'success')
      onRefresh()
    } catch (e: any) {
      onToast(`保存失败: ${e.message}`, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleBrowse = async () => {
    try {
      const chosen = await browseDirectory()
      if (chosen && chosen.trim()) {
        setPathInput(chosen.trim())
      }
    } catch (e: any) {
      onToast(`调起系统目录选择失败: ${e.message}`, 'error')
    }
  }

  const handleOpenFolder = async () => {
    try {
      await openCacheFolder()
      onToast('已在系统文件管理器中打开缓存目录', 'info')
    } catch (e: any) {
      onToast(`打开目录失败: ${e.message}`, 'error')
    }
  }

  const handleAudit = async () => {
    setBusy(true)
    try {
      await auditCache()
      onToast('缓存自检与自愈任务已在后台启动 (扫描损坏/0字节文件)', 'info')
      setTimeout(onRefresh, 1500)
    } catch (e: any) {
      onToast(`体检启动失败: ${e.message}`, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleSlim = async () => {
    if (!confirm('确认安全精简历史过期版本缓存？仅保留最新活跃版本，不影响游戏正常素材。')) {
      return
    }
    setBusy(true)
    try {
      await slimCache(8)
      onToast('历史旧版本安全瘦身任务已在后台启动', 'info')
      setTimeout(onRefresh, 1500)
    } catch (e: any) {
      onToast(`瘦身启动失败: ${e.message}`, 'error')
    } finally {
      setBusy(false)
    }
  }

  const presets = [
    { name: '自动探测 (auto)', path: 'auto' },
    { name: 'D:\\acgpower...', path: 'D:\\acgpower\\cache\\gbf\\https' },
    { name: 'C:\\acgpower...', path: 'C:\\acgpower\\cache\\gbf\\https' },
  ]

  const ramItems = status?.cache.ram_items ?? cacheStats?.ram_items ?? 0
  const ramMb = status?.cache.ram_mb ?? cacheStats?.ram_mb ?? 0
  const ramMaxMb = cacheStats?.ram_max_mb ?? 256

  return (
    <div className="bg-surface border border-hairline rounded-xl p-5 shadow-specular space-y-4">
      {/* Title & Stats */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <HardDrive className="w-4 h-4 text-sys-blue" />
          <h3 className="text-sm font-semibold text-label-primary tracking-tight">
            本地静态缓存 (Cache Storage)
          </h3>
        </div>

        <div className="text-xs text-label-secondary font-mono tnum">
          RAM 热缓存: <strong className="text-sys-green font-medium">{ramMb} MB</strong> / {ramMaxMb} MB ({ramItems} 项)
        </div>
      </div>

      {/* Path Input & Actions */}
      <div className="space-y-2">
        <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2">
          <div className="flex-1">
            <Input
              value={pathInput}
              onChange={(e) => setPathInput(e.target.value)}
              placeholder="例如: D:\acgpower\cache\gbf\https 或 auto"
              mono
            />
          </div>

          <div className="flex items-center gap-1.5 shrink-0 flex-wrap">
            {isDirty && (
              <Button
                variant="primary"
                size="sm"
                onClick={() => handleSaveDir()}
                loading={busy}
                icon={<Save className="w-3.5 h-3.5" />}
              >
                保存
              </Button>
            )}
            <Button
              variant="secondary"
              size="sm"
              onClick={handleBrowse}
              icon={<FolderOpen className="w-3.5 h-3.5" />}
            >
              浏览...
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={handleOpenFolder}
              icon={<ExternalLink className="w-3.5 h-3.5" />}
            >
              打开目录
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={handleAudit}
              loading={busy}
              icon={<Stethoscope className="w-3.5 h-3.5" />}
            >
              一键体检
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={handleSlim}
              loading={busy}
              icon={<Sparkles className="w-3.5 h-3.5" />}
            >
              安全瘦身
            </Button>
          </div>
        </div>

        {/* Quick Presets */}
        <div className="flex items-center gap-2 text-xs pt-1">
          <span className="text-label-tertiary">快速预设:</span>
          <div className="flex items-center gap-1.5 flex-wrap">
            {presets.map((p) => (
              <button
                key={p.path}
                type="button"
                onClick={() => {
                  setPathInput(p.path)
                  handleSaveDir(p.path)
                }}
                className="px-2 py-0.5 rounded text-[11px] font-mono bg-surface-subtle hover:bg-surface-active text-label-secondary hover:text-label-primary border border-hairline transition-colors"
              >
                {p.name}
              </button>
            ))}
          </div>
        </div>
      </div>
    </div>
  )
}
