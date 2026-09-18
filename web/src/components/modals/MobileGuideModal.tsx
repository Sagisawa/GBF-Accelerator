import React from 'react'
import { RuntimeStatus } from '../../types'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { QRCodeSVG } from 'qrcode.react'
import { Smartphone, Wifi, Copy, Check, AlertCircle } from 'lucide-react'
import { applyConfig } from '../../api'

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
  const isLanEnabled = Boolean(status?.allow_lan)
  const host = status?.lan_ip || '192.168.x.x'
  const port = status?.listen_port || 8124
  const pacUrl = `http://${host}:${port}/proxy.pac`
  const guideUrl = `http://${host}:${port}/`

  const handleEnableLan = async () => {
    try {
      await applyConfig({ allow_lan: true })
      onToast('已开启局域网共享', 'success')
      onConfigUpdated()
    } catch (e: any) {
      onToast(`开启局域网失败: ${e.message}`, 'error')
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
          <div className="p-3.5 bg-apple-amber/10 border border-apple-amber/30 rounded-2xl flex items-start gap-3 text-xs text-apple-amber">
            <AlertCircle className="w-4 h-4 shrink-0 mt-0.5" />
            <div className="space-y-2 flex-1">
              <div>
                <strong>当前尚未开启【允许局域网连接】</strong>：外部设备将无法通过 Wi-Fi 访问本机的加速代理端口。
              </div>
              <Button
                variant="apple"
                size="xs"
                onClick={handleEnableLan}
              >
                一键开启局域网共享 (Allow LAN)
              </Button>
            </div>
          </div>
        )}

        {/* QR Code and Instructions */}
        <div className="flex flex-col sm:flex-row items-center gap-5 p-4 bg-black/40 border border-white/[0.08] rounded-2xl">
          <div className="bg-white p-3 rounded-2xl shadow-apple shrink-0">
            <QRCodeSVG value={guideUrl} size={124} level="M" />
          </div>

          <div className="space-y-1.5 text-xs text-label-secondary">
            <div className="font-semibold text-white flex items-center gap-1.5">
              <Wifi className="w-3.5 h-3.5 text-apple-green" /> 扫码一键打开手机指引
            </div>
            <p className="leading-relaxed">
              1. 确保手机 / iPad 与电脑连入<strong>同一 Wi-Fi 网络</strong>；<br />
              2. 手机自带相机扫码打开指引页，一键下载并信任根证书；<br />
              3. 在 Wi-Fi 代理设置中选择【自动】并填入下方 PAC 地址：
            </p>
          </div>
        </div>

        {/* Manual Configuration Block */}
        <div className="p-4 bg-black/40 border border-white/[0.08] rounded-2xl space-y-2.5 text-xs">
          <div className="flex items-center justify-between text-label-secondary">
            <span className="font-medium text-white">PAC 自动代理 URL (推荐):</span>
            <Button
              variant="ghost"
              size="xs"
              onClick={handleCopyPac}
              icon={copied ? <Check className="w-3 h-3 text-apple-green" /> : <Copy className="w-3 h-3" />}
            >
              {copied ? '已复制' : '复制'}
            </Button>
          </div>
          <div className="font-mono text-[12px] text-apple-blue bg-black/60 p-2.5 rounded-xl border border-white/[0.06] select-all break-all">
            {pacUrl}
          </div>

          <div className="text-[11px] text-label-secondary pt-1 flex items-center justify-between">
            <span>手动代理主机: <strong className="font-mono text-white">{host}</strong></span>
            <span>端口: <strong className="font-mono text-white">{port}</strong></span>
          </div>
        </div>

        <div className="flex justify-end pt-2">
          <Button variant="secondary" size="sm" onClick={onClose}>
            关闭指引 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
