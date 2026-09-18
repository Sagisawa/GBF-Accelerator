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
      onToast('缓存自检与自愈任务已在后台启动 (扫描损坏与0字节文件)', 'info')
      setTimeout(onRefresh, 1500)
    } catch (e: any) {
      onToast(`体检启动失败: ${e.message}`, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleSlim = async () => {
    if (!confirm('确认安全精简历史旧版本缓存？仅保留最新活跃版本，不影响游戏正常素材。')) {
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
    { name: 'D 盘默认目录', path: 'D:\\acgpower\\cache\\gbf\\https' },
    { name: 'C 盘默认目录', path: 'C:\\acgpower\\cache\\gbf\\https' },
  ]

  const isPresetSelected = (pPath: string) => {
    const normInput = pathInput.trim().toLowerCase().replace(/\//g, '\\')
    const normPreset = pPath.trim().toLowerCase().replace(/\//g, '\\')
    return normInput === normPreset
  }

  const ramItems = status?.cache.ram_items ?? cacheStats?.ram_items ?? 0
  const ramMb = status?.cache.ram_mb ?? cacheStats?.ram_mb ?? 0
  const ramMaxMb = cacheStats?.ram_max_mb ?? 256

  return (
    <section id="cache" className="space-y-2 select-none scroll-mt-24">
      <div className="flex items-center justify-between px-1">
        <h3 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">
          本地存储与缓存
        </h3>
        <span className="text-xs font-mono text-label-secondary tnum">
          RAM 内存驻留: <strong className="text-apple-green font-medium">{ramMb} MB</strong> / {ramMaxMb} MB ({ramItems.toLocaleString()} 项)
        </span>
      </div>

      {/* Apple Inset Grouped Container */}
      <div className="bg-[#1c1c1e] border border-white/[0.08] rounded-2xl divide-y divide-white/[0.06] overflow-hidden shadow-apple">
        {/* Row 1: Cache Directory Input */}
        <div className="p-5 space-y-3">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-xl bg-apple-blue/15 text-apple-blue flex items-center justify-center shrink-0">
              <HardDrive className="w-4 h-4" />
            </div>
            <div>
              <div className="text-sm font-semibold text-white tracking-tight">
                静态资源缓存目录
              </div>
              <div className="text-xs text-label-secondary mt-0.5 leading-relaxed">
                存放游戏原始资源，支持智能自动探测 ACGP 缓存或手动指定本地磁盘目录
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

            <div className="flex items-center gap-2 shrink-0 flex-wrap">
              {isDirty && (
                <Button
                  variant="apple"
                  size="sm"
                  onClick={() => handleSaveDir()}
                  loading={busy}
                  icon={<Save className="w-3.5 h-3.5" />}
                >
                  保存路径
                </Button>
              )}
              <Button
                variant="secondary"
                size="sm"
                onClick={handleBrowse}
                icon={<FolderOpen className="w-3.5 h-3.5" />}
              >
                浏览目录
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={handleOpenFolder}
                icon={<ExternalLink className="w-3.5 h-3.5" />}
              >
                打开文件夹
              </Button>
            </div>
          </div>
        </div>

        {/* Row 2: Apple Segmented Presets */}
        <div className="px-5 py-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-2.5">
          <div className="text-xs text-label-secondary">
            常用缓存目录快速选择
          </div>

          <div className="inline-flex p-1 rounded-xl bg-black/40 border border-white/[0.06] self-start sm:self-auto gap-1">
            {presets.map((p) => {
              const isSelected = isPresetSelected(p.path)
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
                      ? 'bg-white/[0.16] text-white shadow-sm font-semibold'
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
        <div className="px-5 py-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
          <div>
            <div className="text-sm font-semibold text-white tracking-tight">
              存储健康体检与历史瘦身
            </div>
            <div className="text-xs text-label-secondary mt-0.5 leading-relaxed">
              扫描修复 0 字节损坏文件，或安全清理旧版本过期素材释放磁盘空间
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
              一键体检自愈
            </Button>
            <Button
              variant="secondary"
              size="sm"
              onClick={handleSlim}
              loading={busy}
              icon={<Sparkles className="w-3.5 h-3.5 text-apple-amber" />}
            >
              历史版本瘦身
            </Button>
          </div>
        </div>
      </div>
    </section>
  )
}
