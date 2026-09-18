import React, { useState } from 'react'
import { Modal } from '../common/Modal'
import { Button } from '../common/Button'
import { ShieldCheck, Download, Copy, Check } from 'lucide-react'

export interface CaCertModalProps {
  isOpen: boolean
  onClose: () => void
  isInstalled: boolean
  fingerprint?: string
  actionType: 'install' | 'uninstall'
}

export const CaCertModal: React.FC<CaCertModalProps> = ({
  isOpen,
  onClose,
  isInstalled,
  fingerprint = '8D:7A:35:CA:F9:19:9B:C5:EE:3B:B4:0D:F9:41:15:F4:F3:57:12:65:94:3D:8B:14:D4:0A:43:19:20:B7:5E:75',
  actionType,
}) => {
  const [copied, setCopied] = useState(false)
  const caDownloadUrl = `/ca.crt`

  const handleCopyFp = () => {
    navigator.clipboard.writeText(fingerprint)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }

  if (actionType === 'uninstall') {
    return (
      <Modal
        isOpen={isOpen}
        onClose={onClose}
        title={
          <div className="flex items-center gap-2">
            <ShieldCheck className="w-4 h-4 text-red-600" />
            <span>注销 / 卸载 HTTPS 根证书指引</span>
          </div>
        }
        subtitle="从系统受信任根证书库中完全移除 GBF 加速器自签根证书"
        maxWidth="max-w-md"
      >
        <div className="space-y-3.5 text-xs text-slate-700">
          <div className="p-3 bg-red-50/70 border border-red-200 rounded-lg space-y-1.5 text-slate-700 leading-relaxed">
            <div className="font-bold text-red-800">卸载说明：</div>
            <p>
              若需在系统证书库中移除根证书：
            </p>
            <ol className="list-decimal list-inside space-y-1 text-slate-600 text-[11px]">
              <li>按 <strong>Win + R</strong> 打开运行窗口，输入 <strong>certmgr.msc</strong> 回车；</li>
              <li>展开【受信任的根证书颁发机构】 -&gt; 【证书】；</li>
              <li>找到名为 <strong>GBF Local Accelerator Root CA</strong> 的证书，右键点击【删除】；</li>
              <li>macOS 用户可打开【钥匙串访问】搜索 GBF CA 并删除。</li>
            </ol>
          </div>

          <div className="flex justify-end pt-1">
            <Button variant="desktop" size="sm" onClick={onClose}>
              我知道了 (Esc)
            </Button>
          </div>
        </div>
      </Modal>
    )
  }

  return (
    <Modal
      isOpen={isOpen}
      onClose={onClose}
      title={
        <div className="flex items-center gap-2">
          <ShieldCheck className="w-4 h-4 text-emerald-600" />
          <span>HTTPS 根证书安装与信任指引</span>
        </div>
      }
      subtitle="游戏静态资源 (Akamai CDN) 本地解析与极速加速必需组件"
      maxWidth="max-w-lg"
    >
      <div className="space-y-3.5 text-xs text-slate-700">
        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2">
          <div className="flex items-center justify-between">
            <span className="font-bold text-slate-800">当前信任状态：</span>
            <span className={`font-bold ${isInstalled ? 'text-emerald-600' : 'text-red-600'}`}>
              {isInstalled ? '已信任 (正常工作)' : '未安装信任'}
            </span>
          </div>

          <div className="space-y-1">
            <div className="flex items-center justify-between">
              <span className="text-slate-500 font-mono text-[11px]">SHA-256 唯一指纹：</span>
              <Button
                variant="desktop"
                size="xs"
                onClick={handleCopyFp}
                icon={copied ? <Check className="w-3 h-3 text-emerald-600" /> : <Copy className="w-3 h-3" />}
              >
                {copied ? '已复制' : '复制指纹'}
              </Button>
            </div>
            <div className="bg-white p-2 rounded border border-slate-200 font-mono text-[10px] text-slate-600 break-all select-all">
              {fingerprint}
            </div>
          </div>
        </div>

        <div className="p-3 bg-slate-50 border border-slate-200 rounded-lg space-y-2 leading-relaxed">
          <div className="font-bold text-slate-900">快速安装方式：</div>
          <div className="space-y-1.5 text-slate-600 text-[11px]">
            <div>
              <strong>方式 1（一键自动脚本）：</strong>
              直接运行本加速器根目录下的 <code className="font-mono bg-white px-1.5 py-0.5 border border-slate-200 rounded text-blue-600">install_ca.bat</code>（macOS 为 <code className="font-mono bg-white px-1.5 py-0.5 border border-slate-200 rounded text-blue-600">./install_ca.sh</code>）即可自动导入并设置受信任。
            </div>
            <div>
              <strong>方式 2（手动下载证书）：</strong>
              点击下方按钮下载证书文件，双击打开 -&gt; 点击【安装证书】 -&gt; 存储位置选择【当前用户】或【本地计算机】 -&gt; 选择【将所有的证书都放入下列存储】 -&gt; 浏览选择【受信任的根证书颁发机构】 -&gt; 完成。
            </div>
            <div>
              <strong>Firefox 浏览器特别说明：</strong>
              Firefox 拥有独立证书库，需在 Firefox【设置】 -&gt; 【隐私与安全】 -&gt; 【证书】 -&gt; 【查看证书】 -&gt; 【证书颁发机构】中导入下载的 <code className="font-mono">ca.crt</code>。
            </div>
          </div>
        </div>

        <div className="flex items-center justify-between pt-1">
          <a
            href={caDownloadUrl}
            download="gbf_ca.crt"
            className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded bg-blue-600 hover:bg-blue-700 text-white font-medium text-xs transition-colors"
          >
            <Download className="w-3.5 h-3.5" />
            <span>下载根证书 (ca.crt)</span>
          </a>

          <Button variant="desktop" size="sm" onClick={onClose}>
            完成 (Esc)
          </Button>
        </div>
      </div>
    </Modal>
  )
}
