import React, { useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { Trash2, Cpu, HardDrive } from 'lucide-react'
import { clearCache } from '../../api'

export interface ClearCacheModalProps {
  isOpen: boolean
  onClose: () => void
  onRefresh: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const ClearCacheModal: React.FC<ClearCacheModalProps> = ({
  isOpen,
  onClose,
  onRefresh,
  onToast,
}) => {
  const [busyRam, setBusyRam] = useState(false)
  const [busyAll, setBusyAll] = useState(false)

  const handleClearRam = async () => {
    setBusyRam(true)
    try {
      await clearCache(true)
      onToast('RAM 内存热缓存已成功清空 (磁盘文件不受影响)', 'success')
      onRefresh()
      onClose()
    } catch (e: any) {
      onToast(`清空 RAM 失败: ${e.message}`, 'error')
    } finally {
      setBusyRam(false)
    }
  }

  const handleClearAll = async () => {
    setBusyAll(true)
    try {
      const res = await clearCache(false)
      const count = res.disk_files_deleted ?? 0
      onToast(`全部缓存已清空，成功清除磁盘素材 ${count} 个`, 'success')
      onRefresh()
      onClose()
    } catch (e: any) {
      onToast(`清空全部缓存失败: ${e.message}`, 'error')
    } finally {
      setBusyAll(false)
    }
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Trash2 className="w-4 h-4 text-apple-red" />
          <span>清空静态资源缓存</span>
        </div>
      }
      subtitle="选择清空范围以释放存储空间或重置素材"
      maxWidth="max-w-md"
    >
      <div className="space-y-3.5">
        {/* Option 1: RAM Only */}
        <div className="p-4 bg-black/40 border border-white/[0.08] hover:border-white/[0.16] rounded-2xl transition-all space-y-3">
          <div className="flex items-start gap-3">
            <div className="w-8 h-8 rounded-xl bg-apple-green/15 text-apple-green flex items-center justify-center shrink-0 mt-0.5">
              <Cpu className="w-4 h-4" />
            </div>
            <div className="space-y-1">
              <h4 className="text-xs font-semibold text-white">
                仅清空 RAM 内存热缓存 (温和)
              </h4>
              <p className="text-[11px] text-label-secondary leading-relaxed">
                仅释放物理内存中的热点素材索引，<strong>SSD 磁盘缓存完全保留</strong>。游戏继续从本地极速加载，适合长时间运行后释放系统内存。
              </p>
            </div>
          </div>

          <div className="flex justify-end">
            <Button
              variant="secondary"
              size="sm"
              loading={busyRam}
              onClick={handleClearRam}
            >
              清空 RAM 缓存
            </Button>
          </div>
        </div>

        {/* Option 2: Full Disk Clear */}
        <div className="p-4 bg-apple-red/10 border border-apple-red/30 hover:border-apple-red/50 rounded-2xl transition-all space-y-3">
          <div className="flex items-start gap-3">
            <div className="w-8 h-8 rounded-xl bg-apple-red/20 text-apple-red flex items-center justify-center shrink-0 mt-0.5">
              <HardDrive className="w-4 h-4" />
            </div>
            <div className="space-y-1">
              <h4 className="text-xs font-semibold text-apple-red flex items-center gap-1.5">
                <span>彻底抹除全部静态缓存 (磁盘与内存)</span>
              </h4>
              <p className="text-[11px] text-label-secondary leading-relaxed">
                彻底抹除本地磁盘全部已下载素材（JS、PNG、音频等）。<strong>后续游戏场景将重新从上游网络拉取</strong>。
              </p>
            </div>
          </div>

          <div className="flex justify-end">
            <Button
              variant="danger"
              size="sm"
              loading={busyAll}
              onClick={handleClearAll}
            >
              彻底清空全部缓存
            </Button>
          </div>
        </div>

        <div className="flex justify-end pt-2">
          <Button variant="ghost" size="sm" onClick={onClose}>
            取消 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
