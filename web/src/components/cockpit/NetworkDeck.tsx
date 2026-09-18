import React, { useState, useEffect } from 'react'
import { RuntimeStatus } from '../../types'
import { Input } from '../common/Input'
import { Button } from '../common/Button'
import { Switch } from '../common/Switch'
import { Network, Smartphone, Wifi, ShieldCheck } from 'lucide-react'
import { applyConfig } from '../../api'

export interface NetworkDeckProps {
  status: RuntimeStatus | null
  config: Record<string, any>
  onConfigUpdated: () => void
  onOpenMobileGuide: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const NetworkDeck: React.FC<NetworkDeckProps> = ({
  status,
  config,
  onConfigUpdated,
  onOpenMobileGuide,
  onToast,
}) => {
  const currentPort = config.listen_port ?? status?.listen_port ?? 8124
  const allowLan = Boolean(config.allow_lan ?? status?.allow_lan)

  const [portInput, setPortInput] = useState<string>(String(currentPort))
  const [busy, setBusy] = useState<boolean>(false)

  useEffect(() => {
    if (currentPort) {
      setPortInput(String(currentPort))
    }
  }, [currentPort])

  const isPortDirty = portInput.trim() !== String(currentPort)

  const handleApplyPort = async (p?: number) => {
    const portVal = p ?? parseInt(portInput.trim(), 10)
    if (isNaN(portVal) || portVal <= 0 || portVal > 65535) {
      onToast('请输入有效的端口号 (1-65535)', 'error')
      return
    }
    setBusy(true)
    try {
      await applyConfig({ listen_port: portVal, port: portVal })
      onToast(`监听端口已更新为 ${portVal} (重启生效)`, 'success')
      onConfigUpdated()
    } catch (e: any) {
      onToast(`设置端口失败: ${e.message}`, 'error')
    } finally {
      setBusy(false)
    }
  }

  const handleToggleLan = async (checked: boolean) => {
    try {
      await applyConfig({ allow_lan: checked })
      onToast(checked ? '局域网共享已开启 (0.0.0.0)' : '局域网共享已关闭 (127.0.0.1)', 'info')
      onConfigUpdated()
    } catch (e: any) {
      onToast(`切换局域网失败: ${e.message}`, 'error')
    }
  }

  return (
    <section id="network" className="space-y-2 select-none scroll-mt-24">
      <div className="flex items-center justify-between px-1">
        <h3 className="text-xs font-semibold text-neutral-400 uppercase tracking-wider">
          监听端口与设备共享
        </h3>
        <span className="text-xs font-mono text-apple-green flex items-center gap-1.5">
          <ShieldCheck className="w-3.5 h-3.5" />
          CA 根证书内建就绪
        </span>
      </div>

      {/* Apple Inset Grouped Container */}
      <div className="bg-[#1c1c1e] border border-white/[0.08] rounded-2xl divide-y divide-white/[0.06] overflow-hidden shadow-apple">
        {/* Row 1: Port Configuration */}
        <div className="px-5 py-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-xl bg-apple-blue/15 text-apple-blue flex items-center justify-center shrink-0">
              <Network className="w-4 h-4" />
            </div>
            <div>
              <div className="text-sm font-semibold text-white tracking-tight">
                本地代理监听端口
              </div>
              <div className="text-xs text-label-secondary mt-0.5 leading-relaxed">
                电脑端浏览器代理请指定为 <code className="font-mono text-white">127.0.0.1:{currentPort}</code>
              </div>
            </div>
          </div>

          <div className="flex items-center gap-2 self-start sm:self-auto shrink-0">
            <div className="w-24">
              <Input
                value={portInput}
                onChange={(e) => setPortInput(e.target.value)}
                placeholder="8124"
                mono
              />
            </div>
            {isPortDirty && (
              <Button
                variant="apple"
                size="sm"
                onClick={() => handleApplyPort()}
                loading={busy}
              >
                应用
              </Button>
            )}
            {portInput !== '8124' && (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => {
                  setPortInput('8124')
                  handleApplyPort(8124)
                }}
              >
                默认
              </Button>
            )}
          </div>
        </div>

        {/* Row 2: LAN Switch */}
        <div className="px-5 py-4 flex items-center justify-between gap-4">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-xl bg-apple-green/15 text-apple-green flex items-center justify-center shrink-0">
              <Wifi className="w-4 h-4" />
            </div>
            <div>
              <div className="text-sm font-semibold text-white tracking-tight">
                允许局域网设备连接 (Allow LAN)
              </div>
              <div className="text-xs text-label-secondary mt-0.5 leading-relaxed">
                {status?.lan_ip
                  ? `已开放同一 Wi-Fi 局域网连入，地址: ${status.lan_ip}:${status.listen_port}`
                  : '开启后将监听绑定至 0.0.0.0，供同一局域网下的 iPhone、iPad 或 Android 共享'}
              </div>
            </div>
          </div>

          <Switch
            checked={allowLan}
            onChange={handleToggleLan}
            color="green"
          />
        </div>

        {/* Row 3: Mobile Guide Sheet Trigger */}
        <div className="px-5 py-4 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
          <div className="flex items-center gap-3">
            <div className="w-8 h-8 rounded-xl bg-apple-red/15 text-apple-red flex items-center justify-center shrink-0">
              <Smartphone className="w-4 h-4" />
            </div>
            <div>
              <div className="text-sm font-semibold text-white tracking-tight">
                移动设备一键扫码免安装连接
              </div>
              <div className="text-xs text-label-secondary mt-0.5 leading-relaxed">
                手机相机扫码即可一键安装证书，并配置 PAC 自动代理实现平滑分流
              </div>
            </div>
          </div>

          <Button
            variant="secondary"
            size="sm"
            onClick={onOpenMobileGuide}
            icon={<Smartphone className="w-3.5 h-3.5 text-apple-red" />}
            className="self-start sm:self-auto shrink-0"
          >
            扫码指引
          </Button>
        </div>
      </div>
    </section>
  )
}
