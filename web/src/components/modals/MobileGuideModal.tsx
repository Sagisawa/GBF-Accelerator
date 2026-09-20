import React from 'react'
import { RuntimeStatus } from '../../types'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { QRCodeSVG } from 'qrcode.react'
import { Smartphone, Wifi, Copy, Check, AlertCircle } from 'lucide-react'
import { applyConfig, applyFirewallRule, fetchFirewallStatus, APIError, FirewallStatus } from '../../api'

export interface MobileGuideModalProps {
  isOpen: boolean
  onClose: () => void
  status: RuntimeStatus | null
  onConfigUpdated: () => void
  onToast: (msg: string, type?: 'success' | 'info' | 'error') => void
}

export const MobileGuideModal: React.FC<MobileGuideModalProps> = ({
  isOpen,
  onClose,
  status,
  onConfigUpdated,
  onToast,
}) => {
  const [copied, setCopied] = React.useState(false)
  const [firewallStatus, setFirewallStatus] = React.useState<FirewallStatus | null>(null)
  const [firewallLoading, setFirewallLoading] = React.useState(false)
  const [firewallApplying, setFirewallApplying] = React.useState(false)
  const [lanApplying, setLanApplying] = React.useState(false)
  const [firewallError, setFirewallError] = React.useState<string | null>(null)
  const isLanEnabled = Boolean(status?.allow_lan)
  const isProxyRunning = Boolean(status?.proxy_running)
  const host = status?.lan_ip || '192.168.x.x'
  const port = status?.listen_port || 8124
  const canUseMobileAccess = isLanEnabled && isProxyRunning
  const pacUrl = `http://${host}:${port}/proxy.pac`
  const guideUrl = `http://${host}:${port}/`

  const refreshFirewallStatus = React.useCallback(async () => {
    setFirewallLoading(true)
    setFirewallError(null)
    try {
      setFirewallStatus(await fetchFirewallStatus())
    } catch (e: any) {
      setFirewallStatus(null)
      setFirewallError(e?.message || '无法读取 Windows 防火墙状态')
    } finally {
      setFirewallLoading(false)
    }
  }, [])

  React.useEffect(() => {
    if (!isOpen) return
    refreshFirewallStatus()
  }, [isOpen, refreshFirewallStatus])

  const handleApplyFirewall = async () => {
    if (firewallApplying) return
    setFirewallApplying(true)
    setFirewallError(null)
    try {
      const result = await applyFirewallRule()
      setFirewallStatus(result.status || null)
      onToast(
        result.code === 'already_allowed'
          ? 'Windows 防火墙规则已经满足要求'
          : 'Windows 防火墙规则已配置并验证',
        'success'
      )
      await refreshFirewallStatus()
    } catch (e: any) {
      const code = e instanceof APIError ? e.code : e?.code
      if (code === 'user_cancelled') {
        setFirewallError('已取消 UAC 提权，本程序未修改防火墙规则。')
        onToast('已取消防火墙配置', 'info')
      } else {
        setFirewallError(e?.message || '配置 Windows 防火墙失败')
        onToast('配置 Windows 防火墙失败', 'error')
      }
    } finally {
      setFirewallApplying(false)
    }
  }

  const handleEnableLan = async () => {
    if (lanApplying) return
    setLanApplying(true)
    try {
      await applyConfig({ allow_lan: true })
      onToast('已开启局域网共享', 'success')
      onConfigUpdated()
      await refreshFirewallStatus()
    } catch (e: any) {
      onToast(`开启局域网失败: ${e.message}`, 'error')
    } finally {
      setLanApplying(false)
    }
  }

  const handleCopyPac = () => {
    navigator.clipboard.writeText(pacUrl)
    setCopied(true)
    onToast('PAC 地址已复制', 'success')
    setTimeout(() => setCopied(false), 2000)
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <Smartphone className="w-4 h-4 text-apple-blue" />
          <span>移动端跨设备 (iOS / Android) 连接指引</span>
        </div>
      }
      subtitle="在同一局域网 Wi-Fi 下免安装任何客户端，直接享受加速"
      maxWidth="max-w-lg"
    >
      <div className="space-y-4">
        {!isLanEnabled && (
          <div className="p-3.5 bg-amber-50 border border-amber-200 rounded-lg flex items-start gap-3 text-xs text-amber-800">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5 text-amber-600" />
            <div className="space-y-2 flex-1">
              <div>
                <strong>当前尚未开启【允许局域网连接】</strong>：外部设备将无法通过 Wi-Fi 访问本机的加速代理端口。
              </div>
              <Button
                variant="primary"
                size="xs"
                onClick={handleEnableLan}
                loading={lanApplying}
                disabled={lanApplying}
              >
                {lanApplying ? '正在开启...' : '一键开启局域网共享 (Allow LAN)'}
              </Button>
            </div>
          </div>
        )}

        {/* Windows Firewall Status */}
        <div className="p-3.5 bg-slate-50 border border-slate-200 rounded-lg space-y-2.5 text-xs">
          <div className="flex items-center justify-between gap-2">
            <span className="font-semibold text-slate-900">Windows 防火墙</span>
            {firewallLoading ? (
              <span className="text-slate-400">检测中...</span>
            ) : firewallStatus?.allowed ? (
              <span className="text-emerald-700 font-semibold">已放行</span>
            ) : firewallStatus?.supported === false ? (
              <span className="text-slate-400">当前平台无需配置</span>
            ) : firewallStatus ? (
              <span className="text-amber-700 font-semibold">尚未放行</span>
            ) : (
              <span className="text-rose-600 font-semibold">读取失败</span>
            )}
          </div>

          {firewallStatus?.has_public && !firewallStatus.has_private && (
            <div className="p-2.5 bg-amber-50 border border-amber-200 rounded-md text-amber-800 leading-relaxed">
              当前 Windows 网络属于【公用网络】；本程序创建的防火墙规则仅适用于【专用网络】，因此通过该公用网络连接的设备可能无法访问。若手机无法连接，请前往系统设置将当前网络切换为【专用网络】。
            </div>
          )}

          {firewallStatus?.supported !== false && firewallStatus?.allowed && (
            <div className="text-emerald-700 leading-relaxed">
              🛡️ Windows 防火墙：已放行端口 {firewallStatus.port}（仅限专用网络 / 局域网子网）
            </div>
          )}

          {firewallStatus?.supported !== false && firewallStatus && !firewallStatus.allowed && (
            <div className="flex items-center justify-between gap-2 flex-wrap">
              <span className="text-slate-500 leading-relaxed">
                当前端口 {firewallStatus.port} 尚未检测到符合要求的放行规则。
              </span>
              <Button
                variant="primary"
                size="xs"
                onClick={handleApplyFirewall}
                loading={firewallApplying}
              >
                {firewallApplying ? '正在配置...' : '一键配置防火墙'}
              </Button>
            </div>
          )}

          {firewallError && (
            <div className="flex items-center justify-between gap-2 text-rose-700 leading-relaxed">
              <span>{firewallError}</span>
              <Button variant="desktop" size="xs" onClick={refreshFirewallStatus} disabled={firewallLoading || firewallApplying}>
                重试
              </Button>
            </div>
          )}

          {((firewallStatus?.network_categories || []).length > 0) && (
            <div className="text-[11px] text-slate-500 font-mono">
              当前网络类别：{(firewallStatus?.network_categories || []).join(', ')}
            </div>
          )}
        </div>

        {/* QR Code and Instructions */}
        <div className="flex flex-col sm:flex-row items-center gap-4 p-4 bg-slate-50 border border-slate-200 rounded-lg">
          <div className={`bg-white p-2.5 rounded-lg border border-slate-200 shadow-sm shrink-0 ${!canUseMobileAccess ? 'opacity-45' : ''}`}>
            {canUseMobileAccess ? (
              <QRCodeSVG value={guideUrl} size={120} level="M" />
            ) : (
              <div className="w-[120px] h-[120px] flex items-center justify-center text-center text-[11px] leading-relaxed text-slate-400">
                {!isLanEnabled ? '先开启\n局域网共享' : '请先启动\n加速代理服务'}
              </div>
            )}
          </div>

          <div className="space-y-1.5 text-xs text-slate-600">
            <div className="font-bold text-slate-900 flex items-center gap-1.5">
              <Wifi className="w-3.5 h-3.5 text-emerald-600" /> 扫码一键打开手机指引
            </div>
            <p className="leading-relaxed">
              1. 确保手机 / iPad 与电脑连入<strong>同一局域网</strong>；<br />
              2. 在电脑端先开启【允许局域网连接】，并确保加速代理正在运行；<br />
              3. 再用手机相机扫码，或手动打开下方局域网地址；<br />
              4. 打开指引页后，再按页面提示设置 PAC。
            </p>
          </div>
        </div>

        {/* Manual Configuration Block */}
        <div className="p-3.5 bg-slate-50 border border-slate-200 rounded-lg space-y-2 text-xs">
          <div className="flex items-center justify-between text-slate-700">
            <span className="font-semibold text-slate-900">PAC 自动代理 URL (推荐):</span>
            <Button
              variant="desktop"
              size="xs"
              onClick={handleCopyPac}
              icon={copied ? <Check className="w-3 h-3 text-emerald-600" /> : <Copy className="w-3 h-3" />}
            >
              {copied ? '已复制' : '复制'}
            </Button>
          </div>
          <div className="font-mono text-[11px] text-blue-600 bg-white p-2 rounded border border-slate-300 select-all break-all">
            {pacUrl}
          </div>
          <div className="text-[11px] text-slate-500 leading-relaxed">
            {canUseMobileAccess
              ? <>局域网指引地址：<strong className="font-mono text-slate-800">{guideUrl}</strong></>
              : !isLanEnabled
                ? '当前尚未开放局域网监听，二维码和局域网地址暂时不可访问。'
                : '代理服务尚未启动，8124 端口当前不会接受移动端连接。'}
          </div>

          <div className="text-[11px] text-slate-500 pt-1 flex items-center justify-between">
            <span>手动代理主机: <strong className="font-mono text-slate-800">{host}</strong></span>
            <span>端口: <strong className="font-mono text-slate-800">{port}</strong></span>
          </div>
        </div>

        <div className="flex justify-end pt-1">
          <Button variant="desktop" size="sm" onClick={onClose}>
            关闭指引 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}

