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

  React.useEffect(() => {
    if (typeof config.ram_cache_max_mb === 'number') {
      setRamMb(config.ram_cache_max_mb)
    }
  }, [config.ram_cache_max_mb])

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
    <section className="space-y-2 select-none">
      <div className="flex items-center justify-between px-1">
        <h3 className="text-[11px] font-semibold text-label-secondary uppercase tracking-wider">
          进阶系统调优
        </h3>
      </div>

      {/* Apple Inset Grouped Container */}
      <div className="bg-[#1c1c1e] border border-white/[0.08] rounded-2xl divide-y divide-white/[0.06] overflow-hidden transition-all duration-200">
        {/* Accordion Trigger Header */}
        <button
          type="button"
          onClick={() => setIsOpen(!isOpen)}
          className="w-full px-4 sm:px-5 py-3.5 flex items-center justify-between text-left hover:bg-white/[0.04] transition-colors"
        >
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-xl bg-purple-500/15 text-purple-400 flex items-center justify-center shrink-0">
              <Sliders className="w-4 h-4" />
            </div>
            <div>
              <div className="text-sm font-medium text-white tracking-tight">
                更多性能调优与 PAC 脚本
              </div>
              <div className="text-xs text-label-secondary mt-0.5">
                RAM 上限 {ramMb} MB · 预加载 {enablePrefetch ? '开启' : '关闭'} · 浏览器 304 协商
              </div>
            </div>
          </div>

          <ChevronDown
            className={`w-4 h-4 text-label-secondary transition-transform duration-200 ${
              isOpen ? 'rotate-180' : ''
            }`}
          />
        </button>

        {/* Collapsible Options */}
        {isOpen && (
          <div className="divide-y divide-white/[0.06] animate-in fade-in duration-150">
            {/* Row 1: RAM Slider */}
            <div className="p-4 sm:p-5 space-y-3">
              <div className="flex justify-between items-center">
                <div>
                  <div className="text-sm font-medium text-white tracking-tight">
                    RAM 物理内存热缓存上限
                  </div>
                  <div className="text-xs text-label-secondary mt-0.5">
                    热点素材常驻 RAM 物理内存实现微秒级零 I/O 读取，超出自动置换
                  </div>
                </div>
                <span className="text-xs font-bold font-mono px-2.5 py-1 rounded-full bg-apple-green/15 text-apple-green tnum">
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
                onKeyUp={(e) => handleRamChange(Number((e.target as HTMLInputElement).value))}
                className="w-full h-1.5 accent-apple-green cursor-pointer"
              />
            </div>

            {/* Row 2: Prefetch Switch */}
            <div className="px-4 sm:px-5 py-3.5 flex items-center justify-between gap-4">
              <div className="pr-4">
                <div className="text-sm font-medium text-white tracking-tight">
                  场景素材智能平滑预加载 (Prefetch)
                </div>
                <div className="text-xs text-label-secondary mt-0.5 leading-relaxed">
                  后台轻量解析场景引用的素材并带平滑避让预拉取，大幅降低切副本或首充卡顿
                </div>
              </div>

              <Switch
                checked={enablePrefetch}
                onChange={handleTogglePrefetch}
                color="blue"
              />
            </div>

            {/* Row 3: Browser Cache Switch */}
            <div className="px-4 sm:px-5 py-3.5 flex items-center justify-between gap-4">
              <div className="pr-4">
                <div className="text-sm font-medium text-white tracking-tight">
                  浏览器本地协商缓存 (Browser 304 Cache)
                </div>
                <div className="text-xs text-label-secondary mt-0.5 leading-relaxed">
                  允许浏览器对已缓存的静态资源进行 304 快速协商，降低本地代理网络传输
                </div>
              </div>

              <Switch
                checked={enableBrowserCache}
                onChange={handleToggleBrowserCache}
                color="blue"
              />
            </div>

            {/* Row 4: PAC Script */}
            <div className="px-4 sm:px-5 py-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
              <div>
                <div className="text-sm font-medium text-white tracking-tight">
                  PAC 自动分流脚本地址
                </div>
                <div className="text-xs font-mono text-label-secondary mt-0.5 select-all">
                  {pacUrl}
                </div>
              </div>

              <Button
                variant="secondary"
                size="sm"
                onClick={handleCopyPac}
                icon={copied ? <Check className="w-3.5 h-3.5 text-apple-green" /> : <Copy className="w-3.5 h-3.5" />}
                className="self-start sm:self-auto shrink-0"
              >
                {copied ? '已复制' : '复制脚本地址'}
              </Button>
            </div>
          </div>
        )}
      </div>
    </section>
  )
}
