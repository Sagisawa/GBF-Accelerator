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
          <Trash2 className="w-4 h-4 text-sys-red" />
          <span>清空静态资源缓存</span>
        </div>
      }
      subtitle="选择清空范围以释放系统存储或重置素材"
      maxWidth="max-w-md"
    >
      <div className="space-y-4">
        {/* Option 1: RAM Only */}
        <div className="p-4 bg-surface-subtle border border-hairline hover:border-hairline-strong rounded-xl transition-colors space-y-3">
          <div className="flex items-start gap-3">
            <div className="w-8 h-8 rounded-lg bg-sys-greenBg border border-sys-green/20 flex items-center justify-center shrink-0 mt-0.5">
              <Cpu className="w-4 h-4 text-sys-green" />
            </div>
            <div className="space-y-1">
              <h4 className="text-xs font-semibold text-label-primary">
                仅清空 RAM 内存热缓存 (温和)
              </h4>
              <p className="text-[11px] text-label-secondary leading-relaxed">
                仅释放物理内存中的热点素材索引，<strong>磁盘缓存完全保留</strong>。游戏继续从 SSD 本地加载，适合长时间挂机后释放内存。
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
        <div className="p-4 bg-sys-redBg/30 border border-sys-red/20 hover:border-sys-red/40 rounded-xl transition-colors space-y-3">
          <div className="flex items-start gap-3">
            <div className="w-8 h-8 rounded-lg bg-sys-redBg border border-sys-red/30 flex items-center justify-center shrink-0 mt-0.5">
              <HardDrive className="w-4 h-4 text-sys-red" />
            </div>
            <div className="space-y-1">
              <h4 className="text-xs font-semibold text-sys-red flex items-center gap-1.5">
                <span>彻底清空全部缓存 (磁盘与内存)</span>
              </h4>
              <p className="text-[11px] text-label-secondary leading-relaxed">
                彻底抹除本地磁盘全部已下载素材（.js, .png, 音频）。<strong>后续游戏首次进入场景将重新从网络下载</strong>。
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

        <div className="flex justify-end pt-1">
          <Button variant="ghost" size="sm" onClick={onClose}>
            取消 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
