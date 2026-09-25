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
  ChevronDown,
  ChevronUp,
  Download,
  Zap,
  Trash2,
  Layers,
  HardDrive,
  Globe,
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
  DeviceBrowserItem,
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
  fetchDeviceBrowsers,
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

  // Backup state
  const [autoBackup, setAutoBackup] = useState<boolean>(true)

  // ADB & Device state
  const [adbDevices, setAdbDevices] = useState<AdbDevice[]>([])
  const [selectedDevice, setSelectedDevice] = useState<string>('')
  const [deviceBrowsers, setDeviceBrowsers] = useState<DeviceBrowserItem[]>([])
  const [isLoadingBrowsers, setIsLoadingBrowsers] = useState<boolean>(false)
  const [targetExtractPackage, setTargetExtractPackage] = useState<string>('com.dena.skyleap')
  const [isCustomExtractPackage, setIsCustomExtractPackage] = useState<boolean>(false)
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
        const nextDevices = res.devices || []
        setAdbDevices(prev => {
          if (prev.length === nextDevices.length && prev.every((d, i) => d.serial === nextDevices[i].serial && d.state === nextDevices[i].state)) {
            return prev
          }
          return nextDevices
        })
        const activeDev = nextDevices.find(d => d.state === 'device')
        if (activeDev) {
          if (!selectedDevice || !nextDevices.some(d => d.serial === selectedDevice)) {
            setSelectedDevice(activeDev.serial)
          }
        } else if (nextDevices.length > 0) {
          if (!selectedDevice || !nextDevices.some(d => d.serial === selectedDevice)) {
            setSelectedDevice(nextDevices[0].serial)
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

  // Query installed browsers when selectedDevice connects
  useEffect(() => {
    if (!selectedDevice) {
      setDeviceBrowsers([])
      return
    }
    setIsLoadingBrowsers(true)
    fetchDeviceBrowsers(selectedDevice)
      .then((res) => {
        if (res.ok && res.browsers && res.browsers.length > 0) {
          setDeviceBrowsers(res.browsers)
          // Only auto-select default browser if current target is not custom and not installed on this device
          setTargetExtractPackage(prev => {
            if (isCustomExtractPackage && prev) {
              return prev
            }
            const currentInstalled = res.browsers.find(b => b.package_name === prev && b.is_installed)
            if (currentInstalled) {
              return prev // Preserve user selection!
            }
            const installedSkyLeap = res.browsers.find(b => b.is_skyleap && b.is_installed)
            if (installedSkyLeap) {
              return installedSkyLeap.package_name
            }
            const firstInstalled = res.browsers.find(b => b.is_installed)
            if (firstInstalled) {
              return firstInstalled.package_name
            }
            return prev
          })
        }
      })
      .catch((e) => console.error('Failed to list device browsers:', e))
      .finally(() => setIsLoadingBrowsers(false))
  }, [selectedDevice, isCustomExtractPackage])

  // Probe app on selected device
  useEffect(() => {
    if (!selectedDevice) {
      setDeviceApp(null)
      return
    }
    const dev = adbDevices.find(d => d.serial === selectedDevice)
    if (dev && dev.state === 'device') {
      const targetPkg = targetExtractPackage.trim() || inspectedPkg?.package_name || 'com.dena.skyleap'
      probeDeviceApp(selectedDevice, targetPkg)
        .then(setDeviceApp)
        .catch(() => setDeviceApp(null))
    } else {
      setDeviceApp(null)
    }
  }, [selectedDevice, adbDevices, targetExtractPackage, inspectedPkg])

  // Auto poll devices every 3s
  useEffect(() => {
    refreshDevices()
    const interval = window.setInterval(refreshDevices, 3000)
    return () => window.clearInterval(interval)
  }, [refreshDevices])

  const handleExtractFromDevice = async () => {
    if (!selectedDevice) return
    setIsExtracting(true)
    const pkg = targetExtractPackage.trim() || 'com.dena.skyleap'
    const appLabel = pkg === 'com.dena.skyleap' ? 'SkyLeap' : pkg
    showToast(`正在从手机提取 ${appLabel} 安装包与分包组件...`, 'info')
    try {
      const result = await extractDeviceApp(selectedDevice, pkg)
      setInspectedPkg(result)
      if (result.is_system_webview === false) {
        showToast(`提取完成，但检测到该应用为独立内核浏览器 (${result.engine_desc || '非系统 WebView'})，GBF 补丁暂不支持`, 'error')
      } else {
        showToast(`提取成功！${appLabel} 版本 ${result.version_name || '已就绪'} (${result.total_apks} 个分包已就绪)`, 'success')
      }
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

    const targetPkg = inspectedPkg?.package_name || targetExtractPackage.trim() || 'com.dena.skyleap'

    // When doing regular install (not forceUninstall after signature mismatch modal),
    // check if the app is already installed on the phone to prompt for overwrite confirmation.
    if (!forceUninstall) {
      try {
        let isInstalled = false
        let installedVer = ''
        if (deviceApp && deviceApp.package_name === targetPkg) {
          isInstalled = Boolean(deviceApp.installed)
          installedVer = deviceApp.version_name || ''
        } else {
          const probe = await probeDeviceApp(selectedDevice, targetPkg)
          isInstalled = Boolean(probe.installed)
          installedVer = probe.version_name || ''
        }

        if (isInstalled) {
          const appName = targetPkg === 'com.dena.skyleap' ? 'SkyLeap 浏览器' : targetPkg
          const verInfo = installedVer ? ` (版本: v${installedVer})` : ''
          const confirmMsg = `检测到手机上已安装此应用【${appName}】${verInfo}。\n\n覆盖安装将使用新制作的加速补丁版替换手机上的原应用。\n是否确认覆盖安装？`
          if (!window.confirm(confirmMsg)) {
            return
          }
        }
      } catch (err) {
        console.warn('Pre-install app probe check error:', err)
      }
    }

    setIsInstalling(true)
    setInstallResult(null)
    setShowUninstallModal(false)

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
      if (result.is_system_webview === false) {
        showToast(`检测到该安装包为独立内核浏览器 (${result.engine_desc || '非系统 WebView'})，GBF 补丁暂不支持`, 'error')
      } else {
        showToast(`解析成功: ${result.package_name} (v${result.version_name})`, 'success')
      }
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
      if (result.is_system_webview === false) {
        showToast(`检测到该安装包为独立内核浏览器 (${result.engine_desc || '非系统 WebView'})，GBF 补丁暂不支持`, 'error')
      } else {
        showToast(`解析成功: ${result.package_name} (v${result.version_name})`, 'success')
      }
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

    if (inspectedPkg.is_system_webview === false) {
      showToast(inspectedPkg.unsupported_reason || '不支持该浏览器内核，GBF 补丁仅支持系统 WebView 浏览器', 'error')
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

    setIsPatchStarting(true)
    try {
      await startAndroidPatch(inspectedPkg.file_path, outputDir, autoBackup)
      showToast('处理任务已启动', 'success')
      setPatchStatus({
        ok: true,
        running: true,
        stage: 1,
        stage_text: 'Checking environment & toolchain...',
        progress: 0.1,
        logs: [
          `Patch requested for: ${inspectedPkg.base_input_name}`,
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
  const isEnvironmentReady = isReady && isComponentsVerified

  // Component-specific installation status in the portable tools environment
  const lspatchComp = envStatus?.components?.find(c => c.id === 'lspatch')
  const isLspatchInstalled = Boolean(lspatchComp?.installed && lspatchComp?.verified)

  const moduleComp = envStatus?.components?.find(c => c.id === 'module')
  const isModuleInstalled = Boolean(moduleComp?.installed && moduleComp?.verified)

  const toolsDir = (envStatus?.tools_dir || '').toLowerCase().replace(/\\/g, '/')
  const adbPath = (envStatus?.adb?.path || '').toLowerCase().replace(/\\/g, '/')
  const isInternalAdb = Boolean(envStatus?.adb?.found && toolsDir && adbPath.startsWith(toolsDir))
  const isAdbFound = Boolean(envStatus?.adb?.found)

  const javaPath = (envStatus?.java?.path || '').toLowerCase().replace(/\\/g, '/')
  const isPortableJava = Boolean(envStatus?.java?.found && (javaPath.includes('/jre/') || javaPath.includes('/tools/jre/')) && !javaPath.includes('android studio') && !javaPath.includes('program files'))
  const isJavaFound = Boolean(envStatus?.java?.found)

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

  // Unified 3-stage adaptive button states
  const selectedDeviceBrowser = deviceBrowsers.find(b => b.package_name === (targetExtractPackage.trim() || 'com.dena.skyleap'))
  const isSelectedBrowserUnsupported = inputMode === 'device' && selectedDeviceBrowser?.is_system_webview === false
  const isExtractStage =
    inputMode === 'device' &&
    Boolean(selectedDevice) &&
    Boolean(deviceApp?.installed) &&
    (!inspectedPkg || inspectedPkg.package_name !== (targetExtractPackage.trim() || 'com.dena.skyleap')) &&
    !isRunning &&
    !(patchStatus?.done && !patchStatus?.error && patchResult)
  const isPatchDoneStage = Boolean(patchStatus?.done && !patchStatus?.error && patchResult)

  return (
    <div className="w-full flex flex-col gap-4 sm:gap-5">
      {/* 1. Unified All-in-One Header & Environment Card */}
      <div className="bg-gradient-to-r from-sky-900 via-indigo-950 to-slate-900 text-white rounded-2xl p-5 sm:p-6 shadow-md border border-sky-800/40 relative overflow-hidden">
        <div className="absolute right-0 top-0 bottom-0 w-1/3 bg-radial from-sky-400/10 to-transparent pointer-events-none" />
        
        {/* Top: Title & Badges & Quick Actions */}
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 pb-4 border-b border-white/10">
          <div className="space-y-1.5 max-w-2xl">
            <div className="flex items-center gap-2 flex-wrap">
              <span className="p-1.5 rounded-lg bg-sky-500/20 text-sky-300 border border-sky-400/30">
                <Smartphone className="w-5 h-5" />
              </span>
              <h1 className="text-lg sm:text-xl font-bold tracking-tight">
                Android 浏览器免 Root 全内置补丁
              </h1>
              <span className="text-xs font-mono font-semibold px-2 py-0.5 rounded-full bg-sky-500/20 text-sky-200 border border-sky-400/30">
                All-in-One v2.0
              </span>
              <span
                className={`text-xs font-semibold px-2.5 py-0.5 rounded-full border ${
                  isEnvironmentReady
                    ? 'bg-emerald-500/20 text-emerald-300 border-emerald-400/30'
                    : 'bg-amber-500/20 text-amber-200 border-amber-400/30'
                }`}
              >
                {isEnvironmentReady ? '● 补丁环境齐备' : '○ 缺少环境依赖'}
              </span>
            </div>
            <p className="text-xs sm:text-sm text-slate-300 leading-relaxed">
              将 Go 原生加速核心直接内嵌至浏览器安装包，免 Root、安装即用。优先推荐 DeNA SkyLeap，亦支持任意系统 WebView 浏览器。
            </p>
          </div>

          <div className="flex items-center gap-2 self-end sm:self-center shrink-0 flex-wrap">
            <button
              type="button"
              onClick={handleOpenBackup}
              className="px-3 py-1.5 rounded-xl bg-white/10 hover:bg-white/15 active:bg-white/20 text-white text-xs font-semibold border border-white/20 transition-all flex items-center gap-1.5 cursor-pointer"
              title="浏览已备份的原版安装包目录"
            >
              <Archive className="w-3.5 h-3.5" />
              <span>原版备份</span>
            </button>
            <button
              type="button"
              onClick={checkEnv}
              disabled={loadingEnv}
              className="px-3 py-1.5 rounded-xl bg-white/10 hover:bg-white/15 active:bg-white/20 text-white text-xs font-semibold border border-white/20 transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
              title="重新检测运行库与工具链"
            >
              <RefreshCw className={`w-3.5 h-3.5 ${loadingEnv ? 'animate-spin' : ''}`} />
              <span>检测依赖</span>
            </button>
            {isEnvironmentReady ? (
              <button
                type="button"
                onClick={handleUninstallAllEnv}
                disabled={isUninstallingAll || isDownloadActive}
                className="px-3 py-1.5 rounded-xl bg-rose-500/20 hover:bg-rose-500/30 text-rose-200 text-xs font-semibold border border-rose-400/30 transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
                title="安全停止 ADB 并彻底清理本地下载的工具与 JRE 缓存以释放磁盘"
              >
                <Trash2 className="w-3.5 h-3.5" />
                <span>一键卸载环境</span>
              </button>
            ) : (
              <button
                type="button"
                onClick={handleInstallAllEnv}
                disabled={isInstallingAll || isDownloadActive}
                className="px-3.5 py-1.5 rounded-xl bg-indigo-500 hover:bg-indigo-600 active:bg-indigo-700 text-white text-xs font-bold shadow-sm transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50 select-none"
              >
                <Zap className="w-3.5 h-3.5 fill-current" />
                <span>一键安装全环境</span>
              </button>
            )}
          </div>
        </div>

        {/* Bottom Bar: Environment summary info & component pill status */}
        <div className="pt-3.5 flex flex-col md:flex-row items-start md:items-center justify-between gap-3 text-xs">
          <div className="flex items-center gap-4 flex-wrap text-slate-300">
            {envStatus?.tools_disk_bytes ? (
              <div className="flex items-center gap-1.5">
                <HardDrive className="w-3.5 h-3.5 text-sky-400" />
                <span>工具链占用:</span>
                <span className="font-mono text-white font-semibold">
                  {formatBytes(envStatus?.tools_disk_bytes)}
                </span>
              </div>
            ) : null}
            {adbDevices.filter(d => d.state === 'device').length > 0 ? (
              <div className="flex items-center gap-1.5 text-emerald-300 font-medium">
                <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
                <span>已连接 {adbDevices.filter(d => d.state === 'device').length} 台手机设备</span>
              </div>
            ) : (
              <div className="flex items-center gap-1.5 text-slate-400">
                <Smartphone className="w-3.5 h-3.5" />
                <span>USB 调试手机未连接 (插上即用)</span>
              </div>
            )}
          </div>

          {/* Quick status pills for components */}
          <div className="flex items-center gap-2 flex-wrap text-[11px] font-mono">
            {/* Java 21+ */}
            <span className={`px-2 py-0.5 rounded border flex items-center gap-1 ${
              isPortableJava
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : isJavaFound
                  ? 'bg-sky-500/10 border-sky-500/30 text-sky-300'
                  : 'bg-white/5 border-white/10 text-slate-400'
            }`}>
              {isJavaFound ? '✓' : '✗'} Java 21+{!isPortableJava && isJavaFound ? ' (系统)' : ''}
            </span>

            {/* LSPatch */}
            <span className={`px-2 py-0.5 rounded border flex items-center gap-1 ${
              isLspatchInstalled
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : 'bg-white/5 border-white/10 text-slate-400'
            }`}>
              {isLspatchInstalled ? '✓' : '✗'} LSPatch
            </span>

            {/* 内置模块 */}
            <span className={`px-2 py-0.5 rounded border flex items-center gap-1 ${
              isModuleInstalled
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : 'bg-white/5 border-white/10 text-slate-400'
            }`}>
              {isModuleInstalled ? '✓' : '✗'} 内置模块
            </span>

            {/* ADB */}
            <span className={`px-2 py-0.5 rounded border flex items-center gap-1 ${
              isInternalAdb
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : isAdbFound
                  ? 'bg-sky-500/10 border-sky-500/30 text-sky-300'
                  : 'bg-white/5 border-white/10 text-slate-400'
            }`}>
              {isAdbFound ? '✓' : '✗'} ADB{!isInternalAdb && isAdbFound ? ' (系统)' : ''}
            </span>
          </div>
        </div>

        {/* Download Progress Bar if Active */}
        {isDownloadActive && (
          <div className="mt-3.5 pt-3.5 border-t border-white/10 space-y-2">
            <div className="flex items-center justify-between text-xs text-sky-200">
              <span className="font-semibold">
                {downloadProgress?.stage === 'verifying' ? '正在执行 SHA-256 完整性校验...' : '环境组件传输中...'}
                {downloadProgress?.current_file ? ` (${downloadProgress.current_file})` : ''}
              </span>
              <span className="font-mono font-bold text-white">
                {Math.round(downloadProgress?.percent || 0)}%
              </span>
            </div>
            <div className="w-full bg-black/40 rounded-full h-2 overflow-hidden border border-white/10">
              <div
                className="bg-sky-400 h-2 rounded-full transition-all duration-200"
                style={{ width: `${Math.round(downloadProgress?.percent || 0)}%` }}
              />
            </div>
            <div className="flex items-center justify-between text-[11px] text-slate-400 font-mono">
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
                  className="text-rose-400 hover:text-rose-300 font-semibold cursor-pointer underline"
                >
                  取消
                </button>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Conditional: If Environment is NOT ready, display clean onboarding card */}
      {!isEnvironmentReady ? (
        <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-8 sm:p-10 text-center space-y-4">
          <div className="w-14 h-14 rounded-2xl bg-indigo-50 text-indigo-600 flex items-center justify-center mx-auto shadow-2xs">
            <Layers className="w-7 h-7" />
          </div>
          <div className="space-y-1.5 max-w-lg mx-auto">
            <h3 className="text-base sm:text-lg font-bold text-slate-800">
              请先就绪 Android 补丁制作环境
            </h3>
            <p className="text-xs sm:text-sm text-slate-500 leading-relaxed">
              制作全内置补丁需要依赖 LSPatch 核心、内置 Xposed 模块以及免配置的轻量 Java 21+ 运行库。
              点击下方【一键安装全环境】，系统将全自动完成静默下载与校验，环境齐备后将自动展开制作面板。
            </p>
          </div>
          <div className="pt-2">
            <button
              type="button"
              onClick={handleInstallAllEnv}
              disabled={isInstallingAll || isDownloadActive}
              className="px-6 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-sm font-bold shadow-sm transition-all inline-flex items-center gap-2 cursor-pointer disabled:opacity-50 select-none"
            >
              <Zap className="w-4 h-4 fill-current" />
              <span>{isDownloadActive ? '环境组件下载中...' : '一键安装全环境 (约 120 MB)'}</span>
            </button>
          </div>
        </div>
      ) : (
        <>

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
                {/* Target App Selector */}
                <div className="bg-white border border-slate-200/90 rounded-lg p-3 space-y-2">
                  <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-1 text-xs">
                    <span className="font-bold text-slate-800 flex items-center gap-1.5">
                      <span>待提取的目标浏览器应用：</span>
                      {isLoadingBrowsers && (
                        <span className="text-[11px] text-indigo-600 font-normal animate-pulse">
                          (正在扫描手机已安装应用...)
                        </span>
                      )}
                    </span>
                    <span className="text-[11px] text-slate-500">
                      自动探测手机已安装浏览器，优先推荐 SkyLeap，支持一键选取
                    </span>
                  </div>
                  <div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2">
                    <select
                      value={isCustomExtractPackage ? '__custom__' : targetExtractPackage}
                      onChange={(e) => {
                        if (e.target.value === '__custom__') {
                          setIsCustomExtractPackage(true)
                        } else {
                          setIsCustomExtractPackage(false)
                          setTargetExtractPackage(e.target.value)
                        }
                      }}
                      className="text-xs font-semibold text-slate-800 bg-slate-50 border border-slate-300 rounded-lg px-2.5 py-1.5 focus:outline-none focus:ring-1 focus:ring-indigo-500 cursor-pointer flex-1"
                    >
                      {deviceBrowsers.length > 0 ? (
                        <>
                          {deviceBrowsers.some(b => b.is_installed && b.is_system_webview !== false) && (
                            <optgroup label="✅ 手机已安装的系统 WebView 浏览器 (支持加速)">
                              {deviceBrowsers.filter(b => b.is_installed && b.is_system_webview !== false).map(b => (
                                <option key={b.package_name} value={b.package_name}>
                                  {b.is_skyleap ? '⭐ ' : '🌐 '}{b.label}
                                </option>
                              ))}
                            </optgroup>
                          )}
                          {deviceBrowsers.some(b => b.is_installed && b.is_system_webview === false) && (
                            <optgroup label="⚠️ 手机已安装的独立内核浏览器 (暂不支持)">
                              {deviceBrowsers.filter(b => b.is_installed && b.is_system_webview === false).map(b => (
                                <option key={b.package_name} value={b.package_name}>
                                  🚫 {b.label}
                                </option>
                              ))}
                            </optgroup>
                          )}
                          {deviceBrowsers.some(b => !b.is_installed) && (
                            <optgroup label="🌐 其它常见浏览器 (未检测到安装)">
                              {deviceBrowsers.filter(b => !b.is_installed).map(b => (
                                <option key={b.package_name} value={b.package_name}>
                                  {b.is_system_webview !== false ? '🌐 ' : '🚫 '}{b.label}
                                </option>
                              ))}
                            </optgroup>
                          )}
                        </>
                      ) : (
                        <>
                          <option value="com.dena.skyleap">⭐ DeNA SkyLeap (官方推荐 · 系统 WebView)</option>
                          <option value="mark.via.gp">🌐 Via 浏览器 (系统 WebView · 支持)</option>
                          <option value="com.android.chrome">🚫 Google Chrome (独立 Chromium · 暂不支持)</option>
                          <option value="com.kiwibrowser.browser">🚫 Kiwi Browser (独立 Chromium · 暂不支持)</option>
                          <option value="com.microsoft.emmx">🚫 Microsoft Edge (独立 Chromium · 暂不支持)</option>
                          <option value="com.brave.browser">🚫 Brave (独立 Chromium · 暂不支持)</option>
                        </>
                      )}
                      <option value="__custom__">✍️ 自定义指定其它包名...</option>
                    </select>
                    {isCustomExtractPackage && (
                      <input
                        type="text"
                        value={targetExtractPackage}
                        onChange={(e) => setTargetExtractPackage(e.target.value.trim())}
                        placeholder="输入手机中已安装的应用包名，如 com.example.browser"
                        className="text-xs font-mono text-slate-800 bg-white border border-indigo-300 rounded-lg px-2.5 py-1.5 flex-1 focus:outline-none focus:ring-1 focus:ring-indigo-500"
                      />
                    )}
                  </div>
                  {(() => {
                    const sel = deviceBrowsers.find(b => b.package_name === targetExtractPackage)
                    if (sel && sel.is_system_webview === false) {
                      return (
                        <div className="bg-amber-50 border border-amber-200 rounded-lg p-2.5 text-xs text-amber-900 flex items-start gap-2 mt-1">
                          <AlertTriangle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                          <div className="text-[11px] leading-relaxed">
                            <strong>暂不支持该浏览器：</strong>
                            <span>{sel.label} 采用独立 Chromium / Gecko 内核。GBF 补丁专为基于 Android 系统原生 WebView 的浏览器设计（优先推荐 SkyLeap 或 Via）。</span>
                          </div>
                        </div>
                      )
                    }
                    return null
                  })()}
                </div>

                {deviceApp?.installed ? (
                  <div className="bg-white border border-emerald-200/90 rounded-lg p-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                    <div className="space-y-1">
                      <div className="flex items-center gap-2">
                        <span className="font-bold text-slate-900 text-sm">
                          {deviceApp.package_name === 'com.dena.skyleap' ? 'SkyLeap 浏览器' : deviceApp.package_name}
                        </span>
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
                    {isExtracting ? (
                      <div className="flex items-center gap-1.5 text-xs font-semibold text-indigo-700 bg-indigo-50 border border-indigo-200 px-3 py-1.5 rounded-lg shrink-0">
                        <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                        <span>正在从手机提取...</span>
                      </div>
                    ) : inspectedPkg && inspectedPkg.package_name === deviceApp.package_name ? (
                      <div className="flex items-center gap-1.5 text-xs font-semibold text-emerald-700 bg-emerald-50 border border-emerald-200 px-3 py-1.5 rounded-lg shrink-0">
                        <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600" />
                        <span>已提取并载入</span>
                      </div>
                    ) : (
                      <div className="flex items-center gap-1.5 text-xs font-medium text-slate-600 bg-slate-100 border border-slate-200 px-3 py-1.5 rounded-lg shrink-0">
                        <span className="w-2 h-2 rounded-full bg-indigo-500" />
                        <span>就绪待提取</span>
                      </div>
                    )}
                  </div>
                ) : (
                  <div className="bg-amber-50 border border-amber-200 rounded-lg p-3 text-xs text-amber-900 flex items-start gap-2">
                    <AlertTriangle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                    <div className="space-y-1">
                      <div className="font-semibold">
                        未在所选手机中检测到应用 [{targetExtractPackage || 'com.dena.skyleap'}]
                      </div>
                      <div className="text-amber-800 text-[11px]">
                        请确认手机上已安装该应用，或检查包名拼写；亦可切换到上方【拖拽上传】标签页直接导入本地下载好的安装包。
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
                  {inspectedPkg.is_system_webview === false ? (
                    <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-rose-100 text-rose-800 border border-rose-200 inline-flex items-center gap-1">
                      <AlertTriangle className="w-3 h-3 text-rose-600" />
                      <span>{inspectedPkg.engine_desc || '独立内核 · 暂不支持'}</span>
                    </span>
                  ) : inspectedPkg.is_official_skyleap ? (
                    <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-emerald-100 text-emerald-800 border border-emerald-200 inline-flex items-center gap-1">
                      <CheckCircle2 className="w-3 h-3 text-emerald-600" />
                      <span>官方 SkyLeap 认证 (系统 WebView)</span>
                    </span>
                  ) : (
                    <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-sky-100 text-sky-800 border border-sky-200 inline-flex items-center gap-1">
                      <Globe className="w-3 h-3 text-sky-600" />
                      <span>通用系统 WebView 浏览器 (支持)</span>
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

            {/* Kernel Support Note */}
            {inspectedPkg.is_system_webview === false && (
              <div className="bg-rose-50 border border-rose-200 rounded-lg p-3 text-xs text-rose-900 flex items-start gap-2.5">
                <AlertTriangle className="w-4 h-4 text-rose-600 shrink-0 mt-0.5" />
                <div className="space-y-1">
                  <div className="font-bold">❌ 暂不支持该浏览器内核</div>
                  <div className="text-rose-800 text-[11px] leading-relaxed">
                    {inspectedPkg.unsupported_reason || '检测到该安装包为独立 Chromium/Gecko 内核浏览器（如 Chrome、Kiwi 等）。GBF 补丁仅支持基于 Android 系统原生 WebView 的浏览器（如官方 SkyLeap、Via 等）。暂不支持为独立内核浏览器注入补丁。'}
                  </div>
                </div>
              </div>
            )}

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
            {((inspectedPkg && inspectedPkg.is_system_webview === false) || isSelectedBrowserUnsupported) && (
              <span className="text-[11px] text-rose-600 font-medium">
                仅支持系统 WebView 浏览器
              </span>
            )}
            {isExtractStage ? (
              <button
                type="button"
                disabled={isExtracting || !selectedDevice || !deviceApp?.installed || isSelectedBrowserUnsupported || !isReady || !isComponentsVerified}
                onClick={handleExtractFromDevice}
                className="px-6 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-xs sm:text-sm font-bold shadow-xs transition-all flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed select-none"
              >
                {isExtracting ? (
                  <>
                    <RefreshCw className="w-4 h-4 animate-spin" />
                    <span>正在从手机提取并解析...</span>
                  </>
                ) : (
                  <>
                    <Zap className="w-4 h-4 fill-current" />
                    <span>从手机一键提取并载入</span>
                  </>
                )}
              </button>
            ) : isPatchDoneStage ? (
              <div className="flex items-center gap-2 flex-wrap">
                <button
                  type="button"
                  onClick={() => setPatchStatus(null)}
                  className="px-3.5 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-slate-700 text-xs sm:text-sm font-semibold transition-all cursor-pointer"
                  title="重新制作补丁包"
                >
                  重新制作
                </button>
                <button
                  type="button"
                  onClick={handleOpenOutput}
                  className="px-3.5 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-slate-700 text-xs sm:text-sm font-semibold transition-all cursor-pointer"
                  title="打开生成文件所在目录"
                >
                  打开产物目录
                </button>
                <button
                  type="button"
                  disabled={isInstalling || adbDevices.filter(d => d.state === 'device').length === 0}
                  onClick={() => handleInstallToDevice(false)}
                  title={adbDevices.filter(d => d.state === 'device').length === 0 ? '未检测到已连接手机，请通过 USB 连接手机并开启 USB 调试' : '一键将补丁版安装到手机'}
                  className="px-6 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white text-xs sm:text-sm font-bold shadow-xs transition-all flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed select-none"
                >
                  {isInstalling ? (
                    <>
                      <RefreshCw className="w-4 h-4 animate-spin" />
                      <span>正在安装至手机...</span>
                    </>
                  ) : (
                    <>
                      <Zap className="w-4 h-4 fill-current" />
                      <span>一键安装到手机</span>
                    </>
                  )}
                </button>
              </div>
            ) : (
              <button
                type="button"
                disabled={!isReady || !isComponentsVerified || !inspectedPkg || inspectedPkg.is_system_webview === false || isRunning || isPatchStarting}
                onClick={handleStartPatch}
                className="px-6 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-xs sm:text-sm font-bold shadow-xs transition-all flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed select-none"
              >
                {isRunning ? (
                  <>
                    <RefreshCw className="w-4 h-4 animate-spin" />
                    <span>处理中 ({progressPercent}%)...</span>
                  </>
                ) : !inspectedPkg ? (
                  <>
                    <Play className="w-4 h-4 fill-current opacity-60" />
                    <span>请先导入或选择安装包</span>
                  </>
                ) : (
                  <>
                    <Play className="w-4 h-4 fill-current" />
                    <span>开始制作全内置补丁</span>
                  </>
                )}
              </button>
            )}
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
                <div className="font-bold text-slate-800">在手机上的安装说明：</div>
                <ul className="list-disc pl-4 space-y-0.5 pt-1">
                  <li>
                    <span className="font-semibold">一键安装:</span> 若手机已连接电脑并开启 USB 调试，直接点击上方主操作栏「一键安装到手机」自动推入安装。
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
                        <Smartphone className="w-3.5 h-3.5 text-emerald-700" />
                        <span>检测到已连接手机：{adbDevices.find(d => d.serial === selectedDevice)?.model || selectedDevice}</span>
                      </div>
                      <div className="text-[11px] text-slate-500">
                        无需手动传包与安装 SAI，点击上方主操作栏【一键安装到手机】即可推送到手机部署。
                      </div>
                    </div>
                    {isInstalling ? (
                      <div className="flex items-center gap-1.5 text-xs font-semibold text-emerald-800 bg-emerald-100/90 border border-emerald-300 px-3 py-1.5 rounded-lg shrink-0">
                        <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                        <span>正在推送到手机...</span>
                      </div>
                    ) : (
                      <div className="flex items-center gap-1.5 text-xs font-medium text-emerald-800 bg-emerald-100/80 border border-emerald-300 px-3 py-1.5 rounded-lg shrink-0">
                        <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600" />
                        <span>已连接就绪</span>
                      </div>
                    )}
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
        </>
      )}

      {/* 7. User Notices & Security Disclaimers */}
      <div className="bg-slate-50 border border-slate-200/90 rounded-xl p-4 sm:p-5 text-xs text-slate-600 space-y-2">
        <div className="flex items-center gap-1.5 font-bold text-slate-800">
          <ShieldCheck className="w-4 h-4 text-indigo-600" />
          <span>使用须知与安全治理规范</span>
        </div>
        <ul className="list-disc pl-4 space-y-1 leading-relaxed text-slate-600">
          <li>
            <strong className="text-slate-700">浏览器支持与推荐：</strong> 推荐使用标准系统 WebView 浏览器（优先推荐 DeNA SkyLeap），亦兼容各类基于系统 WebView 打造的轻量浏览器。
          </li>
          <li>
            <strong className="text-slate-700">全内置自包含架构：</strong> 补丁将 Go 加速核心内嵌于 APK 内部，应用启动时主进程会自动拉起本地代理与缓存服务，退出时自动终止，手机桌面无需再安装或常驻独立的加速器宿主 App。
          </li>
          <li>
            <strong className="text-slate-700">100% 本地运算与合规：</strong> 所有解包、注入与签名过程完全在您的本地电脑完成，绝不向任何外部服务器上传安装包；本工具不分发、不篡改官方 SkyLeap 原始字节，用户需自行通过官方渠道获取安装包。
          </li>
        </ul>
      </div>
    </div>
  )
}
