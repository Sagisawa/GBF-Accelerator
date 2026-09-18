import React, { useState, useEffect } from 'react'
import { RuntimeStatus } from '../../types'
import { Input } from '../common/Input'
import { Button } from '../common/Button'
import { Switch } from '../common/Switch'
import { Badge } from '../common/Badge'
import { Network, Smartphone } from 'lucide-react'
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
      onToast(`监听端口已更新为 ${portVal} (重启代理生效)`, 'success')
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
    <div className="bg-surface border border-hairline rounded-xl p-5 shadow-specular space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-2">
          <Network className="w-4 h-4 text-sys-blue" />
          <h3 className="text-sm font-semibold text-label-primary tracking-tight">
            监听端口、局域网与证书 (Network & LAN)
          </h3>
        </div>

        <div className="flex items-center gap-2">
          <Badge variant="green" dot mono>
            CA 证书内建就绪
          </Badge>
        </div>
      </div>

      {/* Port Configuration & Mobile Entry */}
      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        {/* Port Input */}
        <div className="space-y-2">
          <div className="text-xs text-label-secondary font-medium">本地监听端口</div>
          <div className="flex items-center gap-2">
            <div className="w-32">
              <Input
                value={portInput}
                onChange={(e) => setPortInput(e.target.value)}
                placeholder="8124"
                mono
              />
            </div>
            {isPortDirty && (
              <Button
                variant="primary"
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
                恢复默认
              </Button>
            )}
          </div>
          <p className="text-[11px] text-label-tertiary">
            浏览器或系统代理需指向 <code className="font-mono text-label-secondary">127.0.0.1:{currentPort}</code>
          </p>
        </div>

        {/* Mobile Setup Quick Button */}
        <div className="space-y-2 flex flex-col justify-between">
          <div className="text-xs text-label-secondary font-medium">移动端跨设备连接</div>
          <div>
            <Button
              variant="secondary"
              size="md"
              onClick={onOpenMobileGuide}
              icon={<Smartphone className="w-4 h-4 text-sys-blue" />}
              className="w-full sm:w-auto"
            >
              📱 移动端连接指引 / 二维码
            </Button>
          </div>
          <p className="text-[11px] text-label-tertiary">
            同 Wi-Fi 环境下扫码即可自动配置手机 / iPad
          </p>
        </div>
      </div>

      {/* LAN Toggle */}
      <div className="pt-2 border-t border-hairline">
        <Switch
          checked={allowLan}
          onChange={handleToggleLan}
          label="允许局域网其他设备连接 (Allow LAN)"
          description={
            status?.lan_ip
              ? `已开放同一 Wi-Fi 下的设备连入，局域网 IP: ${status.lan_ip}:${status.listen_port}`
              : '开启后将代理监听绑定至 0.0.0.0，供同一局域网下的手机或平板共享'
          }
          color="blue"
        />
      </div>
    </div>
  )
}
