import React, { useState } from 'react'
import { RuntimeStatus } from '../../types'
import { Switch } from '../common/Switch'
import { Button } from '../common/Button'
import { ChevronDown, Sliders, Copy, Check } from 'lucide-react'
import { applyConfig } from '../../api'

export interface AdvancedDisclosureProps {
  status: RuntimeStatus | null
  config: Record<string, any>
  onConfigUpdated: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const AdvancedDisclosure: React.FC<AdvancedDisclosureProps> = ({
  status,
  config,
  onConfigUpdated,
  onToast,
}) => {
  const [isOpen, setIsOpen] = useState<boolean>(false)
  const [ramMb, setRamMb] = useState<number>(config.ram_cache_max_mb || 256)
  const [copied, setCopied] = useState<boolean>(false)

  const enablePrefetch = config.enable_prefetch ?? true
  const enableBrowserCache = config.enable_browser_cache ?? true
  const port = config.listen_port ?? status?.listen_port ?? 8124
  const pacUrl = `http://127.0.0.1:${port}/proxy.pac`

  const handleRamChange = async (val: number) => {
    setRamMb(val)
    try {
      await applyConfig({ ram_cache_max_mb: val })
      onConfigUpdated()
    } catch (e: any) {
      onToast(`保存 RAM 上限失败: ${e.message}`, 'error')
    }
  }

  const handleTogglePrefetch = async (checked: boolean) => {
    try {
      await applyConfig({ enable_prefetch: checked })
      onToast(checked ? '场景素材智能预加载已开启' : '场景素材预加载已关闭', 'info')
      onConfigUpdated()
    } catch (e: any) {
      onToast(`设置预加载失败: ${e.message}`, 'error')
    }
  }

  const handleToggleBrowserCache = async (checked: boolean) => {
    try {
      await applyConfig({ enable_browser_cache: checked })
      onToast(checked ? '浏览器本地缓存协商已开启' : '浏览器协商已关闭', 'info')
      onConfigUpdated()
    } catch (e: any) {
      onToast(`设置浏览器缓存失败: ${e.message}`, 'error')
    }
  }

  const handleCopyPac = () => {
    navigator.clipboard.writeText(pacUrl)
    setCopied(true)
    onToast('PAC 自动配置脚本地址已复制到剪贴板', 'success')
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <div className="bg-surface border border-hairline rounded-xl shadow-specular overflow-hidden transition-all duration-200">
      {/* Header Button */}
      <button
        type="button"
        onClick={() => setIsOpen(!isOpen)}
        className="w-full px-5 py-3.5 flex items-center justify-between text-left hover:bg-surface-elevated transition-colors"
      >
        <div className="flex items-center gap-2">
          <Sliders className="w-4 h-4 text-sys-blue" />
          <span className="text-sm font-medium text-label-primary">
            更多系统调优与高级选项
          </span>
          <span className="text-xs text-label-tertiary">
            (RAM 上限 {ramMb} MB / 预加载 {enablePrefetch ? '开启' : '关闭'} / PAC 分流)
          </span>
        </div>

        <ChevronDown
          className={`w-4 h-4 text-label-tertiary transition-transform duration-200 ${
            isOpen ? 'rotate-180' : ''
          }`}
        />
      </button>

      {/* Collapsible Content */}
      {isOpen && (
        <div className="px-5 pb-5 pt-2 border-t border-hairline space-y-4 animate-in fade-in duration-150">
          {/* RAM Cache Slider */}
          <div className="p-3 bg-surface-subtle rounded-xl border border-hairline space-y-2">
            <div className="flex justify-between items-center">
              <div>
                <div className="text-xs font-semibold text-label-primary">
                  RAM 物理内存热缓存上限
                </div>
                <div className="text-[11px] text-label-secondary mt-0.5">
                  热点素材驻留 RAM 内存实现微秒级零 I/O 读取，超出自动温和置换
                </div>
              </div>
              <span className="text-sm font-bold font-mono text-sys-green tnum">
                {ramMb} MB
              </span>
            </div>
            <input
              type="range"
              min={64}
              max={1024}
              step={64}
              value={ramMb}
              onChange={(e) => setRamMb(Number(e.target.value))}
              onMouseUp={(e) => handleRamChange(Number((e.target as HTMLInputElement).value))}
              onTouchEnd={(e) => handleRamChange(Number((e.target as HTMLInputElement).value))}
              className="w-full h-1.5 accent-sys-green cursor-pointer"
            />
          </div>

          {/* Prefetch Switch */}
          <Switch
            checked={enablePrefetch}
            onChange={handleTogglePrefetch}
            label="场景素材智能平滑预加载 (Prefetch)"
            description="在后台轻量解析场景引用的素材并带平滑避让预拉取，大幅降低切副本或首充卡顿"
            color="blue"
          />

          {/* Browser Cache Switch */}
          <Switch
            checked={enableBrowserCache}
            onChange={handleToggleBrowserCache}
            label="浏览器本地协商缓存 (Browser 304 Cache)"
            description="允许浏览器对已缓存的静态资源进行 304 快速协商，降低本地代理网络传输"
            color="blue"
          />

          {/* PAC script link */}
          <div className="p-3 bg-surface-subtle rounded-xl border border-hairline flex flex-col sm:flex-row sm:items-center justify-between gap-2">
            <div>
              <div className="text-xs font-semibold text-label-primary">
                PAC 自动分流脚本地址
              </div>
              <div className="text-[11px] font-mono text-label-secondary mt-0.5 select-all">
                {pacUrl}
              </div>
            </div>
            <Button
              variant="secondary"
              size="xs"
              onClick={handleCopyPac}
              icon={copied ? <Check className="w-3 h-3 text-sys-green" /> : <Copy className="w-3 h-3" />}
            >
              {copied ? '已复制' : '复制地址'}
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}
