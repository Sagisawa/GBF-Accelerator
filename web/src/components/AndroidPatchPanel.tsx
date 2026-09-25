import React, { useState, useEffect, useRef, useCallback } from 'react'
import {
  Smartphone,
  UploadCloud,
  CheckCircle2,
  AlertTriangle,
  AlertCircle,
  FolderOpen,
  Play,
  Terminal,
  RefreshCw,
  FileBox,
  ShieldCheck,
  Cpu,
  ChevronDown,
  ChevronUp,
  Download,
  Zap,
  Trash2,
  Layers,
  HardDrive,
  Globe,
  Settings2,
  Archive,
} from 'lucide-react'
import {
  AndroidEnvStatus,
  AndroidComponentDownloadProgress,
  AndroidPackageInspection,
  AndroidPatchProgress,
  AdbDevice,
  AdbProbeAppResponse,
  AdbInstallResponse,
} from '../types'
import {
  fetchAndroidEnv,
  inspectAndroidPackage,
  uploadAndroidPackage,
  startAndroidPatch,
  fetchAndroidPatchStatus,
  openPatchOutputFolder,
  openPatchBackupFolder,
  installAllAndroidEnv,
  uninstallAllAndroidEnv,
  fetchAndroidComponentDownloadStatus,
  cancelAndroidComponentDownload,
  fetchAdbDevices,
  probeDeviceApp,
  extractDeviceApp,
  installToDevice,
  downloadAdbPlatformTools,
} from '../api'

interface AndroidPatchPanelProps {
  showToast: (msg: string, type: 'success' | 'error' | 'info') => void
}

const formatBytes = (bytes?: number) => {
  if (!bytes || bytes <= 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

export const AndroidPatchPanel: React.FC<AndroidPatchPanelProps> = ({ showToast }) => {
  // Environment state
  const [envStatus, setEnvStatus] = useState<AndroidEnvStatus | null>(null)
  const [loadingEnv, setLoadingEnv] = useState<boolean>(false)
  const [isInstallingAll, setIsInstallingAll] = useState<boolean>(false)
  const [isUninstallingAll, setIsUninstallingAll] = useState<boolean>(false)

  // Component Download state
  const [downloadProgress, setDownloadProgress] = useState<AndroidComponentDownloadProgress | null>(null)

  // Input & Inspection state
  const [inputMode, setInputMode] = useState<'device' | 'upload' | 'path'>('device')
  const [manualPath, setManualPath] = useState<string>('')
  const [outputDir, setOutputDir] = useState<string>('output_patched')
  const [inspectedPkg, setInspectedPkg] = useState<AndroidPackageInspection | null>(null)
  const [isUploading, setIsUploading] = useState<boolean>(false)
  const [uploadPercent, setUploadPercent] = useState<number>(0)
  const [isInspecting, setIsInspecting] = useState<boolean>(false)

  // Custom Launcher Label & Backup state
  const [enableCustomLabel, setEnableCustomLabel] = useState<boolean>(false)
  const [customAppLabel, setCustomAppLabel] = useState<string>('')
  const [autoBackup, setAutoBackup] = useState<boolean>(true)

  // ADB & Device state
  const [adbDevices, setAdbDevices] = useState<AdbDevice[]>([])
  const [selectedDevice, setSelectedDevice] = useState<string>('')
  const [deviceApp, setDeviceApp] = useState<AdbProbeAppResponse | null>(null)
  const [isExtracting, setIsExtracting] = useState<boolean>(false)
  const [isInstalling, setIsInstalling] = useState<boolean>(false)
  const [installResult, setInstallResult] = useState<AdbInstallResponse | null>(null)
  const [showUninstallModal, setShowUninstallModal] = useState<boolean>(false)
  const [isDownloadingAdb, setIsDownloadingAdb] = useState<boolean>(false)

  // Drag-and-drop state
  const [isDragging, setIsDragging] = useState<boolean>(false)
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Patch Job state
  const [patchStatus, setPatchStatus] = useState<AndroidPatchProgress | null>(null)
  const [isPatchStarting, setIsPatchStarting] = useState<boolean>(false)
  const [showLogs, setShowLogs] = useState<boolean>(true)
  const logTerminalRef = useRef<HTMLDivElement>(null)

  // Sync suggested launcher label when inspectedPkg changes
  useEffect(() => {
    if (inspectedPkg?.is_official_skyleap) {
      setCustomAppLabel('SkyLeap 加速版')
    } else if (inspectedPkg?.package_name) {
      setCustomAppLabel('浏览器加速版')
    }
  }, [inspectedPkg])

  // Load Environment on mount
  const checkEnv = useCallback(async () => {
    setLoadingEnv(true)
    try {
      const data = await fetchAndroidEnv()
      setEnvStatus(data)
      if (data.download?.active) {
        setDownloadProgress(data.download)
      }
    } catch (e: any) {
      showToast(`检测环境失败: ${e.message}`, 'error')
    } finally {
      setLoadingEnv(false)
    }
  }, [showToast])

  useEffect(() => {
    checkEnv()
  }, [checkEnv])

  // Poll component download progress if active
  useEffect(() => {
    let timer: number | null = null
    const isDlActive = Boolean(downloadProgress?.active || envStatus?.download?.active)
    if (!isDlActive) return

    const pollDl = async () => {
      try {
        const res = await fetchAndroidComponentDownloadStatus()
        setDownloadProgress(res.progress)
        if (res.active) {
          timer = window.setTimeout(pollDl, 500)
        } else {
          if (res.progress.done) {
            showToast('Android Patch 组件下载完成并通过校验', 'success')
            checkEnv()
          } else if (res.progress.stage === 'error') {
            showToast(`组件下载失败: ${res.progress.error || '未知错误'}`, 'error')
            checkEnv()
          }
        }
      } catch {
        // quiet error
      }
    }

    pollDl()

    return () => {
      if (timer) window.clearTimeout(timer)
    }
  }, [downloadProgress?.active, envStatus?.download?.active, checkEnv, showToast])

  const handleCancelDownload = async () => {
    try {
      await cancelAndroidComponentDownload()
      showToast('已取消下载', 'info')
      setDownloadProgress(null)
      await checkEnv()
    } catch (e: any) {
      showToast(`取消失败: ${e.message}`, 'error')
    }
  }

  const handleInstallAllEnv = async () => {
    setIsInstallingAll(true)
    try {
      await installAllAndroidEnv()
      showToast('已启动全套 Android 工具链与运行环境安装...', 'info')
      setDownloadProgress({
        active: true,
        percent: 0,
        stage: 'downloading',
      })
    } catch (e: any) {
      showToast(`一键安装全环境失败: ${e.message}`, 'error')
    } finally {
      setIsInstallingAll(false)
    }
  }

  const handleUninstallAllEnv = async () => {
    if (!window.confirm('确定要卸载全套 Android 工具链与 JRE 运行环境并彻底清理本地占用磁盘空间吗？')) {
      return
    }
    setIsUninstallingAll(true)
    try {
      const res = await uninstallAllAndroidEnv()
      showToast(res.message || '已成功卸载全套环境并释放磁盘空间', 'success')
      await checkEnv()
    } catch (e: any) {
      showToast(`卸载失败: ${e.message}`, 'error')
    } finally {
      setIsUninstallingAll(false)
    }
  }

  const handleOpenBackup = async () => {
    try {
      const res = await openPatchBackupFolder()
      showToast(`已打开备份目录: ${res.path || 'backups'}`, 'info')
    } catch (e: any) {
      showToast(`打开备份目录失败: ${e.message}`, 'error')
    }
  }

  // Refresh ADB devices
  const refreshDevices = useCallback(async () => {
    try {
      const res = await fetchAdbDevices()
      if (res.ok) {
        setAdbDevices(res.devices || [])
        const activeDev = res.devices?.find(d => d.state === 'device')
        if (activeDev) {
          if (!selectedDevice || !res.devices.some(d => d.serial === selectedDevice)) {
            setSelectedDevice(activeDev.serial)
          }
        } else if (res.devices && res.devices.length > 0) {
          if (!selectedDevice || !res.devices.some(d => d.serial === selectedDevice)) {
            setSelectedDevice(res.devices[0].serial)
          }
        } else {
          setSelectedDevice('')
          setDeviceApp(null)
        }
      }
    } catch {
      // quiet error
    }
  }, [selectedDevice])

  // Probe app on selected device
  useEffect(() => {
    if (!selectedDevice) {
      setDeviceApp(null)
      return
    }
    const dev = adbDevices.find(d => d.serial === selectedDevice)
    if (dev && dev.state === 'device') {
      const targetPkg = inspectedPkg?.package_name || 'com.dena.skyleap'
      probeDeviceApp(selectedDevice, targetPkg)
        .then(setDeviceApp)
        .catch(() => setDeviceApp(null))
    } else {
      setDeviceApp(null)
    }
  }, [selectedDevice, adbDevices, inspectedPkg])

  // Auto poll devices every 3s
  useEffect(() => {
    refreshDevices()
    const interval = window.setInterval(refreshDevices, 3000)
    return () => window.clearInterval(interval)
  }, [refreshDevices])

  const handleExtractFromDevice = async () => {
    if (!selectedDevice) return
    setIsExtracting(true)
    showToast('正在从手机提取 SkyLeap 安装包与分包组件...', 'info')
    try {
      const result = await extractDeviceApp(selectedDevice, 'com.dena.skyleap')
      setInspectedPkg(result)
      showToast(`提取成功！SkyLeap 版本 ${result.version_name} (${result.total_apks} 个分包已就绪)`, 'success')
    } catch (e: any) {
      showToast(`从手机提取失败: ${e.message}`, 'error')
    } finally {
      setIsExtracting(false)
    }
  }

  const handleInstallToDevice = async (forceUninstall: boolean = false) => {
    if (!selectedDevice) {
      showToast('未选择目标设备', 'error')
      return
    }
    setIsInstalling(true)
    setInstallResult(null)
    setShowUninstallModal(false)

    const targetPkg = inspectedPkg?.package_name || 'com.dena.skyleap'

    try {
      if (forceUninstall) {
        showToast('正在通过 ADB 卸载原版并重新安装补丁版...', 'info')
      } else {
        showToast('正在推送安装到手机...', 'info')
      }

      const res = await installToDevice(selectedDevice, targetPkg, forceUninstall)
      setInstallResult(res)

      if (res.signature_mismatch) {
        setShowUninstallModal(true)
        showToast('检测到官方签名冲突，需先卸载手机上的旧版后再安装', 'info')
      } else if (res.ok) {
        showToast('🎉 安装成功！加速补丁版已部署至手机', 'success')
      } else {
        showToast(`安装失败: ${res.error || '未知错误'}`, 'error')
      }
    } catch (e: any) {
      showToast(`安装失败: ${e.message}`, 'error')
    } finally {
      setIsInstalling(false)
    }
  }

  const handleDownloadAdb = async () => {
    setIsDownloadingAdb(true)
    showToast('正在下载轻量 ADB 平台工具...', 'info')
    try {
      const res = await downloadAdbPlatformTools()
      if (res.ok) {
        showToast('ADB 工具下载并解压成功', 'success')
        await checkEnv()
        await refreshDevices()
      } else {
        showToast(`下载 ADB 失败: ${res.message || '未知错误'}`, 'error')
      }
    } catch (e: any) {
      showToast(`下载 ADB 失败: ${e.message}`, 'error')
    } finally {
      setIsDownloadingAdb(false)
    }
  }

  // Poll patch status when job is active
  useEffect(() => {
    let timer: number | null = null
    let active = true

    const poll = async () => {
      try {
        const status = await fetchAndroidPatchStatus()
        if (active) {
          setPatchStatus(status)
          if (status.running) {
            timer = window.setTimeout(poll, 600)
          }
        }
      } catch {
        if (active && patchStatus?.running) {
          timer = window.setTimeout(poll, 1200)
        }
      }
    }

    if (patchStatus?.running) {
      poll()
    }

    return () => {
      active = false
      if (timer) window.clearTimeout(timer)
    }
  }, [patchStatus?.running])

  // Initial patch status load on mount
  useEffect(() => {
    fetchAndroidPatchStatus().then(setPatchStatus).catch(() => {})
  }, [])

  // Auto scroll logs
  useEffect(() => {
    if (logTerminalRef.current) {
      logTerminalRef.current.scrollTop = logTerminalRef.current.scrollHeight
    }
  }, [patchStatus?.logs])

  // Drag and drop handlers
  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDragging(true)
  }

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDragging(false)
  }

  const handleDrop = async (e: React.DragEvent) => {
    e.preventDefault()
    e.stopPropagation()
    setIsDragging(false)

    const files = e.dataTransfer.files
    if (files && files.length > 0) {
      await handleFileUpload(files[0])
    }
  }

  const handleFileSelect = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (files && files.length > 0) {
      await handleFileUpload(files[0])
    }
  }

  const handleFileUpload = async (file: File) => {
    const ext = file.name.substring(file.name.lastIndexOf('.')).toLowerCase()
    if (!['.apk', '.apks', '.xapk', '.zip'].includes(ext)) {
      showToast(`不支持的文件格式 ${ext}，请提供 .apk、.apks 或 .xapk`, 'error')
      return
    }

    setIsUploading(true)
    setUploadPercent(0)
    try {
      showToast(`正在导入 ${file.name}...`, 'info')
      const result = await uploadAndroidPackage(file, (percent) => {
        setUploadPercent(percent)
      })
      setInspectedPkg(result)
      showToast(`解析成功: ${result.package_name} (v${result.version_name})`, 'success')
    } catch (e: any) {
      showToast(`上传解析失败: ${e.message}`, 'error')
    } finally {
      setIsUploading(false)
    }
  }

  const handleInspectManualPath = async () => {
    const path = manualPath.trim()
    if (!path) {
      showToast('请输入有效的 APK / APKS 路径', 'error')
      return
    }

    setIsInspecting(true)
    try {
      const result = await inspectAndroidPackage(path)
      setInspectedPkg(result)
      showToast(`解析成功: ${result.package_name} (v${result.version_name})`, 'success')
    } catch (e: any) {
      showToast(`解析失败: ${e.message}`, 'error')
    } finally {
      setIsInspecting(false)
    }
  }

  const handleStartPatch = async () => {
    if (!inspectedPkg) {
      showToast('请先选择或拖入要处理的安装包', 'error')
      return
    }

    if (!envStatus?.components_verified) {
      showToast('Android Patch 组件尚未就绪或已损坏，请先下载并启用组件', 'error')
      return
    }

    if (patchStatus?.running) {
      showToast('已有处理任务在运行中，请等待完成', 'info')
      return
    }

    const finalAppLabel = enableCustomLabel ? customAppLabel.trim() : ''

    setIsPatchStarting(true)
    try {
      await startAndroidPatch(inspectedPkg.file_path, outputDir, finalAppLabel, autoBackup)
      showToast('处理任务已启动', 'success')
      setPatchStatus({
        ok: true,
        running: true,
        stage: 1,
        stage_text: 'Checking environment & toolchain...',
        progress: 0.1,
        logs: [
          `Patch requested for: ${inspectedPkg.base_input_name}`,
          ...(finalAppLabel ? [`Custom launcher label: ${finalAppLabel}`] : []),
          `Auto backup enabled: ${autoBackup}`,
        ],
        error: '',
        done: false,
      })
    } catch (e: any) {
      showToast(`启动失败: ${e.message}`, 'error')
    } finally {
      setIsPatchStarting(false)
    }
  }

  const handleOpenOutput = async () => {
    try {
      const res = await openPatchOutputFolder()
      showToast(`已打开目录: ${res.path || outputDir}`, 'info')
    } catch (e: any) {
      showToast(`打开目录失败: ${e.message}`, 'error')
    }
  }

  const isReady = Boolean(envStatus?.ready)
  const isRunning = Boolean(patchStatus?.running)
  const isCorrupted = Boolean(envStatus?.components_corrupted)
  const isComponentsVerified = Boolean(envStatus?.components_verified)
  const isDownloadActive = Boolean(downloadProgress?.active || envStatus?.download?.active)
  const progressPercent = Math.round((patchStatus?.progress ?? 0) * 100)

  const patchResult = patchStatus?.result
  const isResultSplit = Boolean(patchResult?.is_split ?? patchResult?.IsSplit)
  const resultTotalBytes = Number(patchResult?.total_bytes ?? patchResult?.TotalBytes ?? 0)
  const resultSingleApk = patchResult?.single_apk ?? patchResult?.SingleApk ?? ''
  const resultSplitDir = patchResult?.split_dir ?? patchResult?.SplitDir ?? ''
  const resultApksArchive = patchResult?.apks_archive ?? patchResult?.ApksArchive ?? ''
  const resultTotalApks = Number(patchResult?.total_apks ?? patchResult?.TotalApks ?? 0)
  const resultTotalMb = (resultTotalBytes / (1024 * 1024)).toFixed(2)
  const resultGeneratedFile = isResultSplit ? (resultApksArchive || resultSplitDir) : resultSingleApk

  return (
    <div className="w-full flex flex-col gap-4 sm:gap-5">
      {/* 1. Header Banner & Target Browser Guidance */}
      <div className="bg-gradient-to-r from-sky-900 via-indigo-950 to-slate-900 text-white rounded-2xl p-5 sm:p-6 shadow-md border border-sky-800/40 relative overflow-hidden">
        <div className="absolute right-0 top-0 bottom-0 w-1/3 bg-radial from-sky-400/10 to-transparent pointer-events-none" />
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
          <div className="space-y-1.5 max-w-2xl">
            <div className="flex items-center gap-2">
              <span className="p-1.5 rounded-lg bg-sky-500/20 text-sky-300 border border-sky-400/30">
                <Smartphone className="w-5 h-5" />
              </span>
              <h1 className="text-lg sm:text-xl font-bold tracking-tight">
                Android 浏览器免 Root 全内置补丁 (All-in-One)
              </h1>
              <span className="text-xs font-mono font-semibold px-2 py-0.5 rounded-full bg-sky-500/20 text-sky-200 border border-sky-400/30">
                All-in-One v2.0
              </span>
            </div>
            <p className="text-xs sm:text-sm text-slate-300 leading-relaxed">
              <strong>建议使用标准系统 WebView 浏览器（优先推荐 DeNA SkyLeap）</strong>，亦支持任意系统 WebView 浏览器或多开分身。
              本工具将 Go 原生加速核心直接内嵌至浏览器安装包，安装即用。免 Root、零悬浮球干扰、无需手机后台额外常驻独立 App。
            </p>
          </div>

          <div className="flex items-center gap-2 self-end sm:self-center shrink-0">
            <button
              type="button"
              onClick={handleOpenBackup}
              className="px-3.5 py-2 rounded-xl bg-white/10 hover:bg-white/15 active:bg-white/20 text-white text-xs sm:text-sm font-semibold border border-white/20 transition-all flex items-center gap-1.5 cursor-pointer"
              title="浏览已备份的原版安装包"
            >
              <Archive className="w-3.5 h-3.5" />
              <span>原版备份</span>
            </button>
            <button
              type="button"
              onClick={checkEnv}
              disabled={loadingEnv}
              className="px-3.5 py-2 rounded-xl bg-white/10 hover:bg-white/15 active:bg-white/20 text-white text-xs sm:text-sm font-semibold border border-white/20 transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
            >
              <RefreshCw className={`w-3.5 h-3.5 ${loadingEnv ? 'animate-spin' : ''}`} />
              <span>检测依赖</span>
            </button>
          </div>
        </div>
      </div>

      {/* 2. Full Environment & Tools Management Card */}
      <div className="rounded-xl border border-indigo-100 bg-gradient-to-r from-indigo-50/70 via-slate-50 to-sky-50/60 p-4 sm:p-5 shadow-2xs">
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 pb-3 border-b border-indigo-100/80 mb-3.5">
          <div className="flex items-center gap-2.5">
            <div className="w-9 h-9 rounded-xl bg-indigo-600 text-white flex items-center justify-center shrink-0 shadow-2xs">
              <Layers className="w-5 h-5" />
            </div>
            <div>
              <div className="flex items-center gap-2">
                <h2 className="text-sm sm:text-base font-bold text-slate-800">
                  全套环境组件与运行库管理
                </h2>
                <span
                  className={`text-[11px] font-bold px-2 py-0.5 rounded-full border ${
                    envStatus?.full_env_ready
                      ? 'bg-emerald-50 text-emerald-700 border-emerald-200'
                      : envStatus?.ready
                      ? 'bg-sky-50 text-sky-700 border-sky-200'
                      : 'bg-amber-50 text-amber-700 border-amber-200'
                  }`}
                >
                  {envStatus?.full_env_ready ? '全套环境齐备' : envStatus?.ready ? '核心就绪 (缺ADB)' : '环境待配置'}
                </span>
              </div>
              <p className="text-xs text-slate-600 mt-0.5">
                包含 LSPatch 核心、Xposed 全内置模块、轻量 ADB 平台工具与免配置 Java 21+ 运行环境 (100% 官方源校验)。
              </p>
            </div>
          </div>

          <div className="flex items-center gap-2 shrink-0 self-end sm:self-center">
            <button
              type="button"
              onClick={handleInstallAllEnv}
              disabled={isInstallingAll || isDownloadActive}
              className="px-3.5 py-2 rounded-xl bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-xs sm:text-sm font-bold shadow-2xs transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50 select-none"
            >
              <Zap className="w-3.5 h-3.5 fill-current" />
              <span>一键安装全环境</span>
            </button>
            <button
              type="button"
              onClick={handleUninstallAllEnv}
              disabled={isUninstallingAll || isDownloadActive}
              className="px-3.5 py-2 rounded-xl bg-white hover:bg-rose-50 text-rose-700 hover:text-rose-800 text-xs sm:text-sm font-semibold border border-rose-200 transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50 select-none"
              title="安全停止 ADB 并彻底清理本地下载的工具与 JRE 缓存"
            >
              <Trash2 className="w-3.5 h-3.5" />
              <span>一键卸载全环境</span>
            </button>
          </div>
        </div>

        {/* Disk Space & Backup Info */}
        <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2 text-xs text-slate-600 bg-white/70 p-3 rounded-lg border border-slate-200/80 mb-3">
          <div className="flex items-center gap-2">
            <HardDrive className="w-4 h-4 text-slate-500 shrink-0" />
            <span>已占用工具链与运行库空间：</span>
            <span className="font-mono font-bold text-slate-800">
              {formatBytes(envStatus?.tools_disk_bytes)}
            </span>
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              onClick={handleOpenBackup}
              className="text-xs text-indigo-700 hover:text-indigo-900 font-semibold underline flex items-center gap-1 cursor-pointer"
            >
              <Archive className="w-3.5 h-3.5" />
              <span>查看本地原始 APK 备份目录 (backups/)</span>
            </button>
          </div>
        </div>

        {/* Download Progress Bar if Active */}
        {isDownloadActive && (
          <div className="space-y-2 mb-3 bg-white p-3.5 rounded-lg border border-indigo-200 shadow-2xs">
            <div className="flex items-center justify-between text-xs">
              <span className="font-semibold text-slate-700">
                {downloadProgress?.stage === 'verifying' ? '正在执行 SHA-256 完整性校验...' : '环境组件传输中...'}
                {downloadProgress?.current_file ? ` (${downloadProgress.current_file})` : ''}
              </span>
              <span className="font-mono font-bold text-indigo-700">
                {Math.round(downloadProgress?.percent || 0)}%
              </span>
            </div>
            <div className="w-full bg-slate-200/80 rounded-full h-2 overflow-hidden">
              <div
                className="bg-indigo-600 h-2 rounded-full transition-all duration-200"
                style={{ width: `${Math.round(downloadProgress?.percent || 0)}%` }}
              />
            </div>
            <div className="flex items-center justify-between text-[11px] text-slate-500 font-mono">
              <span>
                {formatBytes(downloadProgress?.downloaded_bytes)} / {formatBytes(downloadProgress?.total_bytes)}
              </span>
              <div className="flex items-center gap-3">
                {downloadProgress?.speed_bytes_sec && downloadProgress.speed_bytes_sec > 0 ? (
                  <span>{formatBytes(downloadProgress.speed_bytes_sec)}/s</span>
                ) : null}
                <button
                  type="button"
                  onClick={handleCancelDownload}
                  className="text-rose-600 hover:text-rose-800 font-semibold cursor-pointer underline"
                >
                  取消
                </button>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* 3. Preset Mobile Acceleration Policy Notice */}
      <div className="bg-slate-50 border border-slate-200/90 rounded-xl p-4 sm:p-5 space-y-2.5">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <Settings2 className="w-4 h-4 text-sky-600" />
            <h2 className="text-sm sm:text-base font-bold text-slate-800">
              内嵌加速核心预设策略 (轻量无感、零侵入)
            </h2>
          </div>
          <span className="text-[11px] font-semibold text-slate-500 font-mono bg-white px-2 py-0.5 rounded border border-slate-200">
            内置守护线程: 127.0.0.1:8124 / 8125
          </span>
        </div>
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-2.5 pt-1 text-xs">
          <div className="p-2.5 rounded-lg bg-white border border-slate-200/80 flex flex-col gap-1">
            <div className="font-bold text-slate-800 flex items-center justify-between">
              <span>RAM 缓存</span>
              <span className="text-emerald-700 font-mono font-semibold">0 MB (已关闭)</span>
            </div>
            <div className="text-[11px] text-slate-500 leading-relaxed">
              移动端避免占用多任务 RAM，静态素材全量持久化于应用私有磁盘缓存。
            </div>
          </div>
          <div className="p-2.5 rounded-lg bg-white border border-slate-200/80 flex flex-col gap-1">
            <div className="font-bold text-slate-800 flex items-center justify-between">
              <span>预取调度 (Prefetch)</span>
              <span className="text-slate-700 font-mono font-semibold">关闭 (动态避让)</span>
            </div>
            <div className="text-[11px] text-slate-500 leading-relaxed">
              严格禁用后台预取，节约手机蜂窝网络流量与电量，前台战斗网络绝对优先。
            </div>
          </div>
          <div className="p-2.5 rounded-lg bg-white border border-slate-200/80 flex flex-col gap-1">
            <div className="font-bold text-slate-800 flex items-center justify-between">
              <span>出站网络策略</span>
              <span className="text-sky-700 font-mono font-semibold">直连透明转发</span>
            </div>
            <div className="text-[11px] text-slate-500 leading-relaxed">
              直连 Cygames 官方 API，业务语义 100% 透明穿透，零自定义 Header 污染。
            </div>
          </div>
          <div className="p-2.5 rounded-lg bg-white border border-slate-200/80 flex flex-col gap-1">
            <div className="font-bold text-slate-800 flex items-center justify-between">
              <span>管理控制台</span>
              <span className="text-indigo-700 font-mono font-semibold">:8125 无悬浮窗</span>
            </div>
            <div className="text-[11px] text-slate-500 leading-relaxed">
              游戏画面无悬浮窗打扰；在浏览器内访问 <code>127.0.0.1:8125</code> 即可查看控制面。
            </div>
          </div>
        </div>
      </div>

      {/* 4. Environment Probe Card */}
      <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-5">
        <div className="flex items-center justify-between pb-3 border-b border-slate-100 mb-3.5">
          <div className="flex items-center gap-2">
            <Cpu className="w-4 h-4 text-sky-600" />
            <h2 className="text-sm sm:text-base font-bold text-slate-800">
              运行环境与依赖就绪检查
            </h2>
          </div>
          <span
            className={`text-xs font-bold px-2.5 py-0.5 rounded-full border ${
              isReady && isComponentsVerified
                ? 'bg-emerald-50 text-emerald-700 border-emerald-200'
                : 'bg-amber-50 text-amber-800 border-amber-200'
            }`}
          >
            {isReady && isComponentsVerified ? '核心依赖齐备 · 可正常制作' : '环境未齐备 · 点击上方一键安装'}
          </span>
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-3">
          {/* Java Runtime */}
          <div className="p-3 rounded-lg border border-slate-200 bg-slate-50/60 flex flex-col justify-between gap-1.5">
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-slate-600">Java 21+ 运行环境</span>
              {envStatus?.java?.found ? (
                <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              ) : (
                <AlertCircle className="w-4 h-4 text-rose-500" />
              )}
            </div>
            <div className="text-xs font-mono text-slate-800 truncate" title={envStatus?.java?.path || ''}>
              {envStatus?.java?.found ? envStatus.java.version || '已检测到 Java' : '未找到 Java 21+'}
            </div>
            {!envStatus?.java?.found && (
              <span className="text-[11px] text-rose-600">
                可点击上方「一键安装全环境」免配置自动就绪。
              </span>
            )}
          </div>

          {/* LSPatch Jar */}
          <div className="p-3 rounded-lg border border-slate-200 bg-slate-50/60 flex flex-col justify-between gap-1.5">
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-slate-600">LSPatch 核心</span>
              {envStatus?.lspatch?.verified ? (
                <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              ) : isCorrupted ? (
                <AlertCircle className="w-4 h-4 text-rose-500" />
              ) : (
                <AlertCircle className="w-4 h-4 text-amber-500" />
              )}
            </div>
            <div className="text-xs font-mono text-slate-800 truncate" title={envStatus?.lspatch?.path || ''}>
              {envStatus?.lspatch?.verified
                ? `v1.2 (Build 487) · 校验通过`
                : isCorrupted
                ? 'SHA-256 不匹配 (损坏)'
                : '未下载 (首次使用需下载)'}
            </div>
            {envStatus?.lspatch?.verified && (
              <span className="text-[11px] text-emerald-700">固定版本，已防篡改校验</span>
            )}
          </div>

          {/* SkyLeapModule APK */}
          <div className="p-3 rounded-lg border border-slate-200 bg-slate-50/60 flex flex-col justify-between gap-1.5">
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-slate-600">All-in-One 全内置模块</span>
              {envStatus?.module?.verified ? (
                <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              ) : isCorrupted ? (
                <AlertCircle className="w-4 h-4 text-rose-500" />
              ) : (
                <AlertCircle className="w-4 h-4 text-amber-500" />
              )}
            </div>
            <div className="text-xs font-mono text-slate-800 truncate" title={envStatus?.module?.path || ''}>
              {envStatus?.module?.verified
                ? '内置 Go 核心与 WebView 挂钩'
                : isCorrupted
                ? '哈希不匹配 (已损坏)'
                : '未下载 (首次使用需下载)'}
            </div>
            {envStatus?.module?.verified && (
              <span className="text-[11px] text-slate-500 truncate" title={envStatus.module.path}>
                {envStatus.module.path.split(/[\\/]/).pop()}
              </span>
            )}
          </div>

          {/* ADB Debug Bridge */}
          <div className="p-3 rounded-lg border border-slate-200 bg-slate-50/60 flex flex-col justify-between gap-1.5">
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-slate-600">ADB 手机调试通信</span>
              {envStatus?.adb?.found ? (
                <CheckCircle2 className="w-4 h-4 text-emerald-600" />
              ) : (
                <AlertCircle className="w-4 h-4 text-amber-500" />
              )}
            </div>
            <div className="text-xs font-mono text-slate-800 truncate" title={envStatus?.adb?.path || ''}>
              {envStatus?.adb?.found
                ? envStatus.adb.version || '已检测到 ADB'
                : '未找到 ADB'}
            </div>
            {envStatus?.adb?.found ? (
              <span className="text-[11px] text-emerald-700">
                {adbDevices.filter(d => d.state === 'device').length > 0
                  ? `已连接 ${adbDevices.filter(d => d.state === 'device').length} 台设备`
                  : '未连接手机 (插上即用)'}
              </span>
            ) : (
              <button
                type="button"
                onClick={handleDownloadAdb}
                disabled={isDownloadingAdb}
                className="text-[11px] text-indigo-700 hover:text-indigo-900 underline font-medium text-left cursor-pointer"
              >
                {isDownloadingAdb ? '正在下载平台工具...' : '一键下载轻量 ADB (约 2.8MB)'}
              </button>
            )}
          </div>
        </div>
      </div>

      {/* 5. Input & Package Selection */}
      <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-5">
        <div className="flex items-center justify-between pb-3 border-b border-slate-100 mb-4">
          <div className="flex items-center gap-2">
            <FileBox className="w-4 h-4 text-indigo-600" />
            <h2 className="text-sm sm:text-base font-bold text-slate-800">
              选择官方安装包 (APK / Split APK / APKS / XAPK)
            </h2>
          </div>
          <div className="flex items-center gap-1 text-xs">
            <button
              type="button"
              onClick={() => setInputMode('device')}
              className={`px-2.5 py-1 rounded-lg font-medium transition-all flex items-center gap-1 ${
                inputMode === 'device'
                  ? 'bg-indigo-50 text-indigo-700 font-bold border border-indigo-200'
                  : 'text-slate-500 hover:text-slate-800'
              }`}
            >
              <Smartphone className="w-3.5 h-3.5" />
              <span>从手机提取</span>
              {adbDevices.filter(d => d.state === 'device').length > 0 && (
                <span className="w-2 h-2 rounded-full bg-emerald-500 animate-pulse" />
              )}
            </button>
            <button
              type="button"
              onClick={() => setInputMode('upload')}
              className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
                inputMode === 'upload'
                  ? 'bg-indigo-50 text-indigo-700 font-bold border border-indigo-200'
                  : 'text-slate-500 hover:text-slate-800'
              }`}
            >
              拖拽上传
            </button>
            <button
              type="button"
              onClick={() => setInputMode('path')}
              className={`px-2.5 py-1 rounded-lg font-medium transition-all ${
                inputMode === 'path'
                  ? 'bg-indigo-50 text-indigo-700 font-bold border border-indigo-200'
                  : 'text-slate-500 hover:text-slate-800'
              }`}
            >
              本地路径
            </button>
          </div>
        </div>

        {/* Device Extract Mode */}
        {inputMode === 'device' && (
          <div className="border border-slate-200 bg-slate-50/60 rounded-xl p-4 sm:p-5 space-y-4">
            <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-3 border-b border-slate-200/80">
              <div className="flex items-center gap-2.5">
                <div className="w-9 h-9 rounded-lg bg-indigo-100 text-indigo-600 flex items-center justify-center shrink-0">
                  <Smartphone className="w-5 h-5" />
                </div>
                <div>
                  <div className="text-xs font-semibold text-slate-500">已连接手机设备</div>
                  {adbDevices.length === 0 ? (
                    <div className="text-sm font-bold text-slate-700">未检测到 USB 调试设备</div>
                  ) : (
                    <div className="flex items-center gap-2">
                      <select
                        value={selectedDevice}
                        onChange={(e) => setSelectedDevice(e.target.value)}
                        className="text-sm font-bold text-slate-900 bg-white border border-slate-300 rounded px-2 py-0.5 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                      >
                        {adbDevices.map((d) => (
                          <option key={d.serial} value={d.serial}>
                            {d.model} ({d.serial}) {d.state !== 'device' ? `[${d.state}]` : ''}
                          </option>
                        ))}
                      </select>
                      <span className="text-[11px] px-2 py-0.5 rounded-full font-bold bg-emerald-100 text-emerald-800">
                        在线就绪
                      </span>
                    </div>
                  )}
                </div>
              </div>
              <button
                type="button"
                onClick={refreshDevices}
                className="px-2.5 py-1 text-xs text-slate-600 hover:text-slate-900 border border-slate-200 rounded-lg bg-white flex items-center gap-1.5 self-start sm:self-center cursor-pointer shadow-2xs hover:bg-slate-50"
              >
                <RefreshCw className="w-3.5 h-3.5" />
                <span>刷新设备</span>
              </button>
            </div>

            {adbDevices.length === 0 ? (
              <div className="py-4 text-center space-y-2">
                <p className="text-xs text-slate-600 max-w-md mx-auto">
                  请使用 USB 数据线将 Android 手机连接到电脑，并确保手机已开启【开发者选项 → USB 调试】。
                </p>
                <div className="text-[11px] text-slate-400">
                  若手机弹出“是否允许 USB 调试”提示，请勾选“始终允许”并点击确认。
                </div>
                {!envStatus?.adb?.found && (
                  <div className="pt-2">
                    <button
                      type="button"
                      onClick={handleDownloadAdb}
                      disabled={isDownloadingAdb}
                      className="px-3 py-1.5 rounded-lg bg-indigo-600 hover:bg-indigo-700 text-white text-xs font-semibold inline-flex items-center gap-1.5 shadow-2xs"
                    >
                      <Download className="w-3.5 h-3.5" />
                      <span>{isDownloadingAdb ? '正在下载平台工具...' : '一键下载轻量 ADB 工具 (约 2.8 MB)'}</span>
                    </button>
                  </div>
                )}
              </div>
            ) : (
              <div className="space-y-3">
                {deviceApp?.installed ? (
                  <div className="bg-white border border-emerald-200/90 rounded-lg p-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                    <div className="space-y-1">
                      <div className="flex items-center gap-2">
                        <span className="font-bold text-slate-900 text-sm">SkyLeap 浏览器</span>
                        <span className="text-[11px] font-mono px-1.5 py-0.5 rounded bg-slate-100 text-slate-700 font-semibold">
                          v{deviceApp.version_name || '已安装'}
                        </span>
                        <span className="text-[11px] text-emerald-700 font-medium">
                          ({deviceApp.total_apks} 个分包组件)
                        </span>
                      </div>
                      <div className="text-xs text-slate-500 font-mono">
                        包名: {deviceApp.package_name}
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={handleExtractFromDevice}
                      disabled={isExtracting}
                      className="px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-700 text-white text-xs font-bold flex items-center justify-center gap-1.5 shadow-2xs transition-all cursor-pointer self-start sm:self-center disabled:opacity-50"
                    >
                      {isExtracting ? (
                        <>
                          <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                          <span>正在从手机提取...</span>
                        </>
                      ) : (
                        <>
                          <Zap className="w-3.5 h-3.5 fill-current" />
                          <span>从手机一键提取并载入</span>
                        </>
                      )}
                    </button>
                  </div>
                ) : (
                  <div className="bg-amber-50 border border-amber-200 rounded-lg p-3 text-xs text-amber-900 flex items-start gap-2">
                    <AlertTriangle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                    <div className="space-y-1">
                      <div className="font-semibold">未在所选手机中检测到官方 SkyLeap 浏览器</div>
                      <div className="text-amber-800 text-[11px]">
                        请先在手机上安装官方 SkyLeap，或切换到上方【拖拽上传】标签页直接导入本地下载好的安装包。
                      </div>
                    </div>
                  </div>
                )}
              </div>
            )}
          </div>
        )}

        {/* Upload Mode Dropzone */}
        {inputMode === 'upload' && (
          <div
            onDragOver={handleDragOver}
            onDragLeave={handleDragLeave}
            onDrop={handleDrop}
            onClick={() => fileInputRef.current?.click()}
            className={`border-2 border-dashed rounded-xl p-8 sm:p-10 text-center cursor-pointer transition-all ${
              isDragging
                ? 'border-indigo-500 bg-indigo-50/60 scale-[1.01]'
                : 'border-slate-300 hover:border-indigo-400 bg-slate-50/50 hover:bg-slate-50'
            }`}
          >
            <input
              ref={fileInputRef}
              type="file"
              accept=".apk,.apks,.xapk,.zip"
              className="hidden"
              onChange={handleFileSelect}
            />
            <div className="flex flex-col items-center justify-center gap-2.5">
              <div className="w-12 h-12 rounded-full bg-indigo-100 text-indigo-600 flex items-center justify-center shadow-2xs">
                <UploadCloud className="w-6 h-6" />
              </div>
              <div className="space-y-1">
                <div className="text-sm font-bold text-slate-800">
                  {isUploading
                    ? `正在上传导入 (${uploadPercent}%)...`
                    : '将官方浏览器安装包拖入此处，或点击浏览文件'}
                </div>
                <div className="text-xs text-slate-500">
                  优先推荐 SkyLeap 官方安装包；支持单 APK、Split APK 压缩包、.apks 以及 .xapk 格式 (最大 256MB)
                </div>
              </div>
              {isUploading && (
                <div className="w-48 bg-slate-200 rounded-full h-1.5 overflow-hidden mt-2">
                  <div
                    className="bg-indigo-600 h-1.5 rounded-full transition-all duration-150"
                    style={{ width: `${uploadPercent}%` }}
                  />
                </div>
              )}
            </div>
          </div>
        )}

        {/* Path Input Mode */}
        {inputMode === 'path' && (
          <div className="space-y-3">
            <label className="text-xs text-slate-600 font-medium block">
              输入电脑上 APK / APKS 文件的绝对路径或工作区相对路径：
            </label>
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={manualPath}
                onChange={(e) => setManualPath(e.target.value)}
                placeholder="例如: C:\Users\Downloads\com.dena.skyleap.apks"
                className="flex-1 bg-slate-50 border border-slate-200 rounded-lg px-3 py-2 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500 transition-all"
              />
              <button
                type="button"
                onClick={handleInspectManualPath}
                disabled={isInspecting || !manualPath.trim()}
                className="px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-xs sm:text-sm font-semibold transition-all shadow-2xs disabled:opacity-50 cursor-pointer shrink-0"
              >
                {isInspecting ? '解析中...' : '解析安装包'}
              </button>
            </div>
          </div>
        )}

        {/* Inspected Package Card */}
        {inspectedPkg && (
          <div className="mt-4 p-4 rounded-xl border border-indigo-100 bg-indigo-50/40 flex flex-col gap-3 animate-in fade-in duration-200">
            <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-2 pb-2 border-b border-indigo-100/70">
              <div className="space-y-1 min-w-0">
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="text-xs font-bold text-slate-900 font-mono">
                    {inspectedPkg.package_name}
                  </span>
                  <span className="text-xs font-semibold px-2 py-0.5 rounded-full bg-slate-200/80 text-slate-700">
                    v{inspectedPkg.version_name}
                  </span>
                  {inspectedPkg.is_official_skyleap ? (
                    <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-emerald-100 text-emerald-800 border border-emerald-200 inline-flex items-center gap-1">
                      <CheckCircle2 className="w-3 h-3 text-emerald-600" />
                      <span>官方 SkyLeap 认证</span>
                    </span>
                  ) : (
                    <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-sky-100 text-sky-800 border border-sky-200 inline-flex items-center gap-1">
                      <Globe className="w-3 h-3 text-sky-600" />
                      <span>通用系统 WebView 浏览器</span>
                    </span>
                  )}
                </div>
                <div className="text-xs text-slate-600 flex items-center gap-2 flex-wrap">
                  <span>
                    结构: {inspectedPkg.is_split ? `Split APK (${inspectedPkg.total_apks} 个子文件)` : '单 APK 文件'}
                  </span>
                  <span>·</span>
                  <span className="truncate max-w-xs sm:max-w-md" title={inspectedPkg.file_path}>
                    文件: {inspectedPkg.base_input_name}
                  </span>
                </div>
              </div>

              <div className="shrink-0 flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => setInspectedPkg(null)}
                  className="text-xs text-slate-500 hover:text-slate-800 px-2 py-1 rounded transition-colors"
                >
                  清除
                </button>
              </div>
            </div>

            {/* Custom App Launcher Label Setting */}
            <div className="bg-white/80 p-3 rounded-lg border border-slate-200 space-y-2">
              <div className="flex items-center justify-between">
                <label className="flex items-center gap-2 cursor-pointer select-none">
                  <input
                    type="checkbox"
                    checked={enableCustomLabel}
                    onChange={(e) => setEnableCustomLabel(e.target.checked)}
                    className="w-4 h-4 rounded text-indigo-600 focus:ring-indigo-500 border-slate-300"
                  />
                  <span className="text-xs font-bold text-slate-800">
                    自定义手机桌面显示名称（如：SkyLeap 加速版）
                  </span>
                </label>
                <span className="text-[11px] text-slate-500 font-medium">可选</span>
              </div>

              {enableCustomLabel && (
                <div className="pt-1.5 space-y-1.5 animate-in fade-in duration-150">
                  <div className="flex items-center gap-2">
                    <span className="text-xs font-medium text-slate-600 shrink-0">桌面显示名称：</span>
                    <input
                      type="text"
                      value={customAppLabel}
                      onChange={(e) => setCustomAppLabel(e.target.value)}
                      placeholder="例如: SkyLeap 加速版"
                      className="flex-1 bg-slate-50 border border-slate-200 rounded px-2 py-1 text-xs text-slate-800 focus:bg-white focus:outline-none focus:ring-1 focus:ring-indigo-500"
                    />
                  </div>
                  <p className="text-[11px] text-slate-500 leading-relaxed">
                    💡 提示：在手机桌面上显示自定义应用名称，方便在桌面上与普通浏览器进行视觉区分。
                  </p>
                </div>
              )}
            </div>

            {/* Auto Backup Setting */}
            <div className="bg-white/80 p-3 rounded-lg border border-slate-200 flex items-center justify-between">
              <label className="flex items-center gap-2 cursor-pointer select-none">
                <input
                  type="checkbox"
                  checked={autoBackup}
                  onChange={(e) => setAutoBackup(e.target.checked)}
                  className="w-4 h-4 rounded text-indigo-600 focus:ring-indigo-500 border-slate-300"
                />
                <span className="text-xs font-medium text-slate-700">
                  自动备份原版安装包至 <code>backups/</code> 目录
                </span>
              </label>
              <button
                type="button"
                onClick={handleOpenBackup}
                className="text-xs text-indigo-700 hover:text-indigo-900 underline font-medium cursor-pointer"
              >
                打开备份目录
              </button>
            </div>
          </div>
        )}

        {/* Patch Options & Run Button */}
        <div className="mt-5 pt-4 border-t border-slate-100 flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
          <div className="flex items-center gap-2 flex-1 max-w-md">
            <label className="text-xs font-medium text-slate-600 shrink-0">输出目录：</label>
            <input
              type="text"
              value={outputDir}
              onChange={(e) => setOutputDir(e.target.value)}
              disabled={isRunning}
              className="flex-1 bg-slate-50 border border-slate-200 rounded-lg px-2.5 py-1.5 text-xs font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-1 focus:ring-sky-500"
            />
          </div>

          <div className="flex flex-col sm:flex-row items-end sm:items-center gap-2">
            {!isComponentsVerified && (
              <span className="text-[11px] text-amber-700 font-medium">
                {isCorrupted ? '组件损坏，需重新下载' : '需先下载并启用组件'}
              </span>
            )}
            <button
              type="button"
              disabled={!isReady || !isComponentsVerified || !inspectedPkg || isRunning || isPatchStarting}
              onClick={handleStartPatch}
              className="px-6 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-xs sm:text-sm font-bold shadow-xs transition-all flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed select-none"
            >
              {isRunning ? (
                <>
                  <RefreshCw className="w-4 h-4 animate-spin" />
                  <span>处理中 ({progressPercent}%)...</span>
                </>
              ) : (
                <>
                  <Play className="w-4 h-4 fill-current" />
                  <span>开始制作全内置补丁</span>
                </>
              )}
            </button>
          </div>
        </div>
      </div>

      {/* 6. Patch Progress & Terminal Card */}
      {(isRunning || patchStatus?.done) && (
        <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-5">
          <div className="flex items-center justify-between pb-3 border-b border-slate-100 mb-3.5">
            <div className="flex items-center gap-2">
              <Terminal className="w-4 h-4 text-slate-700" />
              <h2 className="text-sm sm:text-base font-bold text-slate-800">
                处理进度与执行日志
              </h2>
            </div>
            <div className="flex items-center gap-2">
              {patchStatus?.done && !patchStatus.error && (
                <button
                  type="button"
                  onClick={handleOpenOutput}
                  className="px-3 py-1 rounded-lg bg-emerald-50 hover:bg-emerald-100 text-emerald-800 border border-emerald-200 text-xs font-semibold flex items-center gap-1.5 transition-colors cursor-pointer"
                >
                  <FolderOpen className="w-3.5 h-3.5" />
                  <span>打开产物文件夹</span>
                </button>
              )}
              <button
                type="button"
                onClick={() => setShowLogs(!showLogs)}
                className="text-slate-500 hover:text-slate-800 p-1 rounded"
              >
                {showLogs ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
              </button>
            </div>
          </div>

          {/* Progress Bar & Stage Indicator */}
          <div className="space-y-2 mb-4">
            <div className="flex items-center justify-between text-xs">
              <span className="font-semibold text-slate-700">
                {patchStatus?.stage_text || (isRunning ? '正在处理...' : '处理就绪')}
              </span>
              <span className="font-mono font-bold text-indigo-700">{progressPercent}%</span>
            </div>
            <div className="w-full bg-slate-100 rounded-full h-2.5 overflow-hidden">
              <div
                className={`h-2.5 rounded-full transition-all duration-300 ${
                  patchStatus?.error
                    ? 'bg-rose-500'
                    : patchStatus?.done
                    ? 'bg-emerald-500'
                    : 'bg-indigo-600'
                }`}
                style={{ width: `${progressPercent}%` }}
              />
            </div>

            {/* Stages overview */}
            <div className="grid grid-cols-5 gap-1.5 text-[11px] text-center pt-1 font-medium">
              {[
                '1. 环境工具',
                '2. 解析与备份',
                '3. LSPatch 注入',
                '4. 组织产物',
                '5. 完整性校验',
              ].map((name, i) => {
                const stageNum = i + 1
                const curStage = patchStatus?.stage ?? 0
                const isPassed = curStage > stageNum || (curStage === 5 && patchStatus?.done)
                const isCurrent = curStage === stageNum && isRunning

                return (
                  <div
                    key={name}
                    className={`py-1 px-1 rounded border text-[11px] truncate ${
                      isPassed
                        ? 'bg-emerald-50 text-emerald-800 border-emerald-200'
                        : isCurrent
                        ? 'bg-indigo-50 text-indigo-800 border-indigo-300 font-bold animate-pulse'
                        : 'bg-slate-50 text-slate-400 border-slate-200'
                    }`}
                  >
                    {name}
                  </div>
                )
              })}
            </div>
          </div>

          {/* Result Output Card if Done */}
          {patchStatus?.done && !patchStatus.error && patchResult && (
            <div className="mb-4 p-4 rounded-xl bg-emerald-50/80 border border-emerald-200 text-xs text-emerald-950 space-y-2">
              <div className="flex items-center gap-2 font-bold text-emerald-900 text-sm">
                <CheckCircle2 className="w-4 h-4 text-emerald-600" />
                <span>处理完成！已生成全内置加速产物 (无需独立 App)</span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-slate-700 pt-1">
                <div>
                  <span className="font-semibold text-slate-900">输出类型：</span>
                  <span>{isResultSplit ? `Split APK 套件${resultTotalApks > 0 ? ` (${resultTotalApks} 个分包)` : ''}` : '单 APK 安装包'}</span>
                </div>
                <div>
                  <span className="font-semibold text-slate-900">总大小：</span>
                  <span>{resultTotalMb} MB</span>
                </div>
                <div className="sm:col-span-2 truncate">
                  <span className="font-semibold text-slate-900">生成文件：</span>
                  <span className="font-mono text-slate-800" title={resultGeneratedFile}>
                    {resultGeneratedFile}
                  </span>
                </div>
                {patchResult?.backup_path && (
                  <div className="sm:col-span-2 truncate text-slate-500">
                    <span className="font-semibold text-slate-700">原版备份：</span>
                    <span className="font-mono" title={patchResult.backup_path}>
                      {patchResult.backup_path}
                    </span>
                  </div>
                )}
              </div>

              {/* Installation guidance */}
              <div className="pt-2 border-t border-emerald-200/60 text-slate-600 space-y-2">
                <div className="font-bold text-slate-800">在手机上的安装与多开共存：</div>
                <div className="bg-sky-50 border border-sky-200 rounded-lg p-3 text-[11px] text-sky-950 space-y-1.5 leading-relaxed">
                  <div className="font-bold text-sky-900 flex items-center gap-1.5">
                    <Smartphone className="w-3.5 h-3.5 text-sky-700" />
                    <span>如何与手机上的官方正版共存？</span>
                  </div>
                  <div>• <strong>系统双开 / 应用分身：</strong>现代 Android 手机（小米、华为、三星、OPPO、vivo 等）均支持在系统设置中开启「应用双开 / 应用分身」，安装补丁版后直接开启双开即可拥有两套独立数据与账号。</div>
                  <div>• <strong>修补通用浏览器：</strong>亦可直接选择修补另一款轻量基于系统 WebView 的浏览器（如 Via、Kiwi、X浏览器等），由于包名不同，天然与官方 SkyLeap 完美共存。</div>
                  <div>• <strong>原版数据安全：</strong>原版安装包已自动备份至 <code>backups/</code> 目录。若直接覆盖安装官方 SkyLeap，由于签名变更 Android 会提示冲突，点击下方「一键安装到手机」可智能一键先卸载再安装。</div>
                </div>
                <ul className="list-disc pl-4 space-y-0.5 pt-1">
                  <li>
                    <span className="font-semibold">一键安装:</span> 若手机已连接电脑并开启 USB 调试，直接点击下方「一键安装到手机」自动推入安装。
                  </li>
                  <li>
                    <span className="font-semibold">手机直接安装:</span> {isResultSplit ? '将生成的 .apks 传至手机，使用 SAI (Split APKs Installer) 安装。' : '将生成的 APK 传至手机并点击安装。'}
                  </li>
                  <li>
                    <span className="font-semibold">开始使用:</span> 安装完成后直接在手机上打开补丁版浏览器即可，内置加速核心随浏览器启动自动开启，在浏览器访问 <code>http://127.0.0.1:8125</code> 可查看控制台。
                  </li>
                </ul>
              </div>

              {/* One-Click Install to Phone */}
              {adbDevices.filter(d => d.state === 'device').length > 0 && (
                <div className="pt-3 border-t border-emerald-200/60 space-y-2">
                  <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                    <div className="space-y-0.5">
                      <div className="font-bold text-slate-800 text-xs flex items-center gap-1.5">
                        <Smartphone className="w-3.5 h-3.5 text-indigo-600" />
                        <span>检测到已连接手机：{adbDevices.find(d => d.serial === selectedDevice)?.model || selectedDevice}</span>
                      </div>
                      <div className="text-[11px] text-slate-500">
                        无需手动传包与安装 SAI，可通过 ADB 直接一键推送到手机完成部署。
                      </div>
                    </div>
                    <button
                      type="button"
                      onClick={() => handleInstallToDevice(false)}
                      disabled={isInstalling}
                      className="px-4 py-2 rounded-lg bg-emerald-600 hover:bg-emerald-700 text-white font-bold text-xs flex items-center justify-center gap-1.5 shadow-2xs transition-all cursor-pointer self-start sm:self-center disabled:opacity-50"
                    >
                      {isInstalling ? (
                        <>
                          <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                          <span>正在安装至手机...</span>
                        </>
                      ) : (
                        <>
                          <Zap className="w-3.5 h-3.5 fill-current" />
                          <span>一键安装到手机</span>
                        </>
                      )}
                    </button>
                  </div>

                  {/* Signature Conflict Modal/Prompt */}
                  {showUninstallModal && (
                    <div className="bg-amber-50 border border-amber-300 rounded-xl p-3 text-xs text-amber-950 space-y-2">
                      <div className="flex items-start gap-2">
                        <AlertTriangle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                        <div className="space-y-1">
                          <div className="font-bold text-amber-900">
                            检测到官方原版签名冲突 (INSTALL_FAILED_UPDATE_INCOMPATIBLE)
                          </div>
                          <div className="text-[11px] text-amber-800 leading-relaxed">
                            手机上的 SkyLeap 是官方开发者签名，而注入补丁后的应用使用的是调试签名。Android 安全机制要求覆盖安装时签名必须一致。
                          </div>
                          <div className="text-[11px] font-bold text-rose-700 bg-rose-50 border border-rose-200 p-2 rounded">
                            ⚠️ 重要提示：需先卸载手机上的旧版 SkyLeap。请务必确认您的 GBF 游戏账号已绑定 Mobage/邮箱/推特，以免游客数据丢失！
                          </div>
                        </div>
                      </div>
                      <div className="flex items-center justify-end gap-2 pt-1">
                        <button
                          type="button"
                          onClick={() => setShowUninstallModal(false)}
                          className="px-3 py-1.5 rounded-lg border border-slate-300 text-slate-700 hover:bg-slate-100 text-xs font-medium cursor-pointer"
                        >
                          取消
                        </button>
                        <button
                          type="button"
                          onClick={() => handleInstallToDevice(true)}
                          disabled={isInstalling}
                          className="px-3.5 py-1.5 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-bold flex items-center gap-1.5 cursor-pointer shadow-2xs"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                          <span>确认先卸载旧版，再自动安装补丁版</span>
                        </button>
                      </div>
                    </div>
                  )}

                  {installResult?.ok && (
                    <div className="bg-emerald-100/70 border border-emerald-300 rounded-lg p-2.5 text-xs text-emerald-900 flex items-center gap-2">
                      <CheckCircle2 className="w-4 h-4 text-emerald-600 shrink-0" />
                      <span className="font-medium">🎉 安装成功！应用已成功部署至手机，可在手机桌面上直接打开开始游戏。</span>
                    </div>
                  )}
                </div>
              )}
            </div>
          )}

          {/* Error Banner */}
          {patchStatus?.error && (
            <div className="mb-4 p-3 rounded-xl bg-rose-50 border border-rose-200 text-xs text-rose-800 flex items-start gap-2">
              <AlertCircle className="w-4 h-4 text-rose-600 shrink-0 mt-0.5" />
              <div>
                <span className="font-bold">处理失败：</span>
                <span>{patchStatus.error}</span>
              </div>
            </div>
          )}

          {/* Terminal Logs */}
          {showLogs && (
            <div
              ref={logTerminalRef}
              className="bg-slate-950 text-slate-200 p-3.5 rounded-xl font-mono text-xs max-h-60 overflow-y-auto space-y-1 border border-slate-800 select-text"
            >
              {(patchStatus?.logs || []).map((line, idx) => (
                <div key={idx} className="leading-relaxed whitespace-pre-wrap break-all">
                  {line}
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* 7. User Notices & Security Disclaimers */}
      <div className="bg-slate-50 border border-slate-200/90 rounded-xl p-4 sm:p-5 text-xs text-slate-600 space-y-2">
        <div className="flex items-center gap-1.5 font-bold text-slate-800">
          <ShieldCheck className="w-4 h-4 text-indigo-600" />
          <span>使用须知与安全治理规范</span>
        </div>
        <ul className="list-disc pl-4 space-y-1 leading-relaxed text-slate-600">
          <li>
            <strong className="text-slate-700">浏览器支持与推荐：</strong> 推荐使用标准系统 WebView 浏览器（优先推荐 DeNA SkyLeap），亦兼容各类基于系统 WebView 打造的浏览器或分身多开工具。
          </li>
          <li>
            <strong className="text-slate-700">全内置自包含架构：</strong> 补丁将 Go 加速核心内嵌于 APK 内部，应用启动时主进程会自动拉起本地代理与缓存服务，退出时自动终止，手机桌面无需再安装或常驻独立的加速器宿主 App。
          </li>
          <li>
            <strong className="text-slate-700">共存分身与多开：</strong> 勾选「更换包名」后，补丁包将作为独立应用安装，可与官方原版 SkyLeap 完美共存，无需卸载原版，账号相互隔离。
          </li>
          <li>
            <strong className="text-slate-700">100% 本地运算与合规：</strong> 所有解包、注入与签名过程完全在您的本地电脑完成，绝不向任何外部服务器上传安装包；本工具不分发、不篡改官方 SkyLeap 原始字节，用户需自行通过官方渠道获取安装包。
          </li>
        </ul>
      </div>
    </div>
  )
}
