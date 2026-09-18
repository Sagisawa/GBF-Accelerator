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
    <section className="space-y-2 select-none">
      <div className="flex items-center justify-between px-1">
        <h3 className="text-[11px] font-semibold text-label-secondary uppercase tracking-wider">
          本地存储与缓存
        </h3>
        <span className="text-[11px] font-mono text-label-secondary tnum">
          RAM 热缓存: <strong className="text-apple-green font-medium">{ramMb} MB</strong> / {ramMaxMb} MB ({ramItems} 项)
        </span>
      </div>

      {/* Apple Inset Grouped Container */}
      <div className="bg-[#1c1c1e] border border-white/[0.08] rounded-2xl divide-y divide-white/[0.06] overflow-hidden">
        {/* Row 1: Cache Directory Input */}
        <div className="p-4 sm:p-5 space-y-3">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-xl bg-apple-blue/15 text-apple-blue flex items-center justify-center shrink-0">
              <HardDrive className="w-4 h-4" />
            </div>
            <div>
              <div className="text-sm font-medium text-white tracking-tight">
                静态缓存物理路径
              </div>
              <div className="text-xs text-label-secondary mt-0.5">
                存储碧蓝幻想上游 CDN 原始素材，支持自动探测或指定本地盘符
              </div>
            </div>
          </div>

          <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2 pt-1">
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
                  variant="apple"
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
                浏览
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={handleOpenFolder}
                icon={<ExternalLink className="w-3.5 h-3.5" />}
              >
                打开目录
              </Button>
            </div>
          </div>
        </div>

        {/* Row 2: Apple Segmented Presets */}
        <div className="px-4 sm:px-5 py-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-2.5">
          <div className="text-xs text-label-secondary">
            常用缓存路径快速切换
          </div>

          <div className="inline-flex p-1 rounded-xl bg-black/40 border border-white/[0.06] self-start sm:self-auto gap-1">
            {presets.map((p) => {
              const isSelected = pathInput === p.path
              return (
                <button
                  key={p.path}
                  type="button"
                  onClick={() => {
                    setPathInput(p.path)
                    handleSaveDir(p.path)
                  }}
                  className={`px-3 py-1 rounded-lg text-xs font-medium transition-all ${
                    isSelected
                      ? 'bg-white/[0.14] text-white shadow-sm font-semibold'
                      : 'text-label-secondary hover:text-white'
                  }`}
                >
                  {p.name}
                </button>
              )
            })}
          </div>
        </div>

        {/* Row 3: Maintenance & Slimming */}
        <div className="px-4 sm:px-5 py-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
          <div>
            <div className="text-sm font-medium text-white tracking-tight">
              存储健康体检与瘦身
            </div>
            <div className="text-xs text-label-secondary mt-0.5">
              扫描修复 0 字节损坏文件，或安全清理旧版本历史活动素材
            </div>
          </div>

          <div className="flex items-center gap-2 self-start sm:self-auto shrink-0">
            <Button
              variant="secondary"
              size="sm"
              onClick={handleAudit}
              loading={busy}
              icon={<Stethoscope className="w-3.5 h-3.5 text-apple-blue" />}
            >
              一键体检
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={handleSlim}
              loading={busy}
              icon={<Sparkles className="w-3.5 h-3.5 text-apple-amber" />}
            >
              安全瘦身
            </Button>
          </div>
        </div>
      </div>
    </section>
  )
}
