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
  XCircle,
} from 'lucide-react'
import {
  AndroidEnvStatus,
  AndroidComponentDownloadProgress,
  AndroidPackageInspection,
  AndroidPatchProgress,
} from '../types'
import {
  fetchAndroidEnv,
  inspectAndroidPackage,
  uploadAndroidPackage,
  startAndroidPatch,
  fetchAndroidPatchStatus,
  openPatchOutputFolder,
  downloadAndroidComponents,
  fetchAndroidComponentDownloadStatus,
  cancelAndroidComponentDownload,
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

  // Component Download state
  const [isStartingDownload, setIsStartingDownload] = useState<boolean>(false)
  const [downloadProgress, setDownloadProgress] = useState<AndroidComponentDownloadProgress | null>(null)

  // Input & Inspection state
  const [inputMode, setInputMode] = useState<'upload' | 'path'>('upload')
  const [manualPath, setManualPath] = useState<string>('')
  const [outputDir, setOutputDir] = useState<string>('output_patched')
  const [inspectedPkg, setInspectedPkg] = useState<AndroidPackageInspection | null>(null)
  const [isUploading, setIsUploading] = useState<boolean>(false)
  const [uploadPercent, setUploadPercent] = useState<number>(0)
  const [isInspecting, setIsInspecting] = useState<boolean>(false)

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
          // Completed or stopped
          if (res.progress.done) {
            showToast('Android Patch 组件下载完成并通过 SHA-256 校验', 'success')
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

  const handleStartDownload = async (force: boolean = false) => {
    setIsStartingDownload(true)
    try {
      const res = await downloadAndroidComponents(force)
      if (res.already_installed) {
        showToast('组件已全部安装并通过校验', 'info')
        await checkEnv()
      } else {
        showToast('开始下载 Android Patch 组件...', 'info')
        setDownloadProgress({
          active: true,
          percent: 0,
          stage: 'downloading',
        })
      }
    } catch (e: any) {
      showToast(`启动下载失败: ${e.message}`, 'error')
    } finally {
      setIsStartingDownload(false)
    }
  }

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

  // Poll patch status if job is running
  useEffect(() => {
    let timer: number | null = null
    const poll = async () => {
      try {
        const status = await fetchAndroidPatchStatus()
        setPatchStatus(status)
        if (status.running) {
          timer = window.setTimeout(poll, 800)
        }
      } catch {
        // quiet error during polling
      }
    }

    // Initial check
    poll()

    return () => {
      if (timer) window.clearTimeout(timer)
    }
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

    setIsPatchStarting(true)
    try {
      await startAndroidPatch(inspectedPkg.file_path, outputDir)
      showToast('处理任务已启动', 'success')
      // Trigger status polling immediately
      const initialStatus = await fetchAndroidPatchStatus()
      setPatchStatus(initialStatus)
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

  return (
    <div className="w-full flex flex-col gap-4 sm:gap-5">
      {/* 1. Header Banner & Architecture Notice */}
      <div className="bg-gradient-to-r from-sky-900 to-indigo-950 text-white rounded-2xl p-5 sm:p-6 shadow-md border border-sky-800/40 relative overflow-hidden">
        <div className="absolute right-0 top-0 bottom-0 w-1/3 bg-radial from-sky-400/10 to-transparent pointer-events-none" />
        <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
          <div className="space-y-1.5 max-w-2xl">
            <div className="flex items-center gap-2">
              <span className="p-1.5 rounded-lg bg-sky-500/20 text-sky-300 border border-sky-400/30">
                <Smartphone className="w-5 h-5" />
              </span>
              <h1 className="text-lg sm:text-xl font-bold tracking-tight">
                Android 浏览器免 Root 补丁工具
              </h1>
              <span className="text-xs font-mono font-semibold px-2 py-0.5 rounded-full bg-sky-500/20 text-sky-200 border border-sky-400/30">
                SkyLeap v0.1
              </span>
            </div>
            <p className="text-xs sm:text-sm text-slate-300 leading-relaxed">
              将官方 SkyLeap 注入 GBF-Accelerator 专用透明代理与缓存控制器模块。
              生成的 APK 无需手机 Root 或安装 LSPosed 框架，配合 Android 端 GBF-Accelerator 即可直接使用。
            </p>
          </div>

          <div className="flex items-center gap-2 self-end sm:self-center shrink-0">
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

      {/* 2. On-Demand Components Management Card */}
      {(!isComponentsVerified || isDownloadActive) && (
        <div
          className={`rounded-xl border shadow-2xs p-4 sm:p-5 transition-all ${
            isCorrupted
              ? 'bg-rose-50/70 border-rose-200'
              : isDownloadActive
              ? 'bg-indigo-50/60 border-indigo-200'
              : 'bg-sky-50/70 border-sky-200'
          }`}
        >
          <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 pb-3 border-b border-slate-200/60 mb-3.5">
            <div className="flex items-center gap-2.5">
              {isCorrupted ? (
                <div className="w-8 h-8 rounded-lg bg-rose-100 text-rose-700 flex items-center justify-center shrink-0">
                  <AlertTriangle className="w-4 h-4" />
                </div>
              ) : isDownloadActive ? (
                <div className="w-8 h-8 rounded-lg bg-indigo-100 text-indigo-700 flex items-center justify-center shrink-0">
                  <RefreshCw className="w-4 h-4 animate-spin" />
                </div>
              ) : (
                <div className="w-8 h-8 rounded-lg bg-sky-100 text-sky-700 flex items-center justify-center shrink-0">
                  <Download className="w-4 h-4" />
                </div>
              )}
              <div>
                <h2 className="text-sm sm:text-base font-bold text-slate-800">
                  {isCorrupted
                    ? 'Android Patch 组件校验失败或已损坏'
                    : isDownloadActive
                    ? '正在安全下载并校验 Android Patch 组件...'
                    : '首次使用需下载 Android Patch 组件 (~13 MB)'}
                </h2>
                <p className="text-xs text-slate-600 mt-0.5">
                  {isCorrupted
                    ? '本地部分组件哈希与官方固定版本不匹配。为防篡改和保障安全性，需重新下载。'
                    : isDownloadActive
                    ? `${
                        downloadProgress?.current_file
                          ? `正在下载 ${downloadProgress.current_file} (${downloadProgress.file_index || 1}/${
                              downloadProgress.total_files || 3
                            })`
                          : '正在建立连接...'
                      }`
                    : 'GBF-Accelerator 遵循最小体积原则，默认不附带 Android 相关文件。点击下方按钮即可一键获取：'}
                </p>
              </div>
            </div>

            <div className="flex items-center gap-2 shrink-0 self-end sm:self-center">
              {isDownloadActive ? (
                <button
                  type="button"
                  onClick={handleCancelDownload}
                  className="px-3.5 py-1.5 rounded-lg bg-rose-100 hover:bg-rose-200 text-rose-700 text-xs font-semibold border border-rose-300 transition-colors flex items-center gap-1.5 cursor-pointer"
                >
                  <XCircle className="w-3.5 h-3.5" />
                  <span>取消下载</span>
                </button>
              ) : (
                <button
                  type="button"
                  onClick={() => handleStartDownload(isCorrupted)}
                  disabled={isStartingDownload}
                  className={`px-4 py-2 rounded-xl text-white text-xs sm:text-sm font-bold shadow-xs transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50 select-none ${
                    isCorrupted
                      ? 'bg-rose-600 hover:bg-rose-700 active:bg-rose-800'
                      : 'bg-sky-600 hover:bg-sky-700 active:bg-sky-800'
                  }`}
                >
                  <Download className="w-4 h-4" />
                  <span>{isCorrupted ? '重新下载并校验组件' : '下载并启用 (~13 MB)'}</span>
                </button>
              )}
            </div>
          </div>

          {/* Download Progress Bar if Active */}
          {isDownloadActive && (
            <div className="space-y-2 mb-3 bg-white/70 p-3 rounded-lg border border-indigo-100">
              <div className="flex items-center justify-between text-xs">
                <span className="font-semibold text-slate-700">
                  {downloadProgress?.stage === 'verifying' ? '正在执行 SHA-256 完整性校验...' : '文件传输中...'}
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
                {downloadProgress?.speed_bytes_sec && downloadProgress.speed_bytes_sec > 0 ? (
                  <span>{formatBytes(downloadProgress.speed_bytes_sec)}/s</span>
                ) : null}
              </div>
            </div>
          )}

          {/* Component items summary */}
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-2.5 pt-1 text-xs">
            <div className="p-2.5 rounded-lg bg-white/80 border border-slate-200/80 flex flex-col justify-between gap-1">
              <div className="font-semibold text-slate-800 flex items-center justify-between">
                <span>LSPatch Portable 核心</span>
                <span className="text-[11px] text-slate-500 font-mono">~12.1 MB</span>
              </div>
              <div className="text-[11px] text-slate-500">v1.2 (Build 487) · 字节级哈希防篡改</div>
            </div>
            <div className="p-2.5 rounded-lg bg-white/80 border border-slate-200/80 flex flex-col justify-between gap-1">
              <div className="font-semibold text-slate-800 flex items-center justify-between">
                <span>SkyLeapModule 模块</span>
                <span className="text-[11px] text-slate-500 font-mono">~1.0 MB</span>
              </div>
              <div className="text-[11px] text-slate-500">独立 Xposed Module · 专用代理控制器</div>
            </div>
            <div className="p-2.5 rounded-lg bg-white/80 border border-slate-200/80 flex flex-col justify-between gap-1">
              <div className="font-semibold text-slate-800 flex items-center justify-between">
                <span>开源许可协议</span>
                <span className="text-[11px] text-slate-500 font-mono">&lt; 2 KB</span>
              </div>
              <div className="text-[11px] text-slate-500">第三方依赖合规声明与许可证</div>
            </div>
          </div>
        </div>
      )}

      {/* Verified Compact Banner if components are completely ready */}
      {isComponentsVerified && !isDownloadActive && (
        <div className="bg-emerald-50/70 border border-emerald-200/90 rounded-xl px-4 py-2.5 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-2 text-xs">
          <div className="flex items-center gap-2 text-emerald-950">
            <CheckCircle2 className="w-4 h-4 text-emerald-600 shrink-0" />
            <span className="font-medium">
              Android Patch 组件全部就绪 · 已通过 SHA-256 防篡改校验
            </span>
            <span className="font-mono text-[11px] text-emerald-700 hidden md:inline">
              ({envStatus?.tools_dir})
            </span>
          </div>
          <button
            type="button"
            onClick={() => handleStartDownload(true)}
            disabled={isStartingDownload}
            className="text-xs text-emerald-800 hover:text-emerald-950 underline font-medium cursor-pointer self-end sm:self-center"
          >
            重新校验/下载
          </button>
        </div>
      )}

      {/* 3. Environment Probe Card */}
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
            {isReady && isComponentsVerified ? '依赖齐备 · 可正常工作' : '环境未齐备 · 请查看提示'}
          </span>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
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
                LSPatch 需 Java 21+。若已安装 Android Studio，系统将自动识别其内置 JBR。
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
                ? `v1.2 (Build 487) · SHA-256 校验通过`
                : isCorrupted
                ? 'SHA-256 不匹配 (已损坏)'
                : '未下载 (首次使用需启用)'}
            </div>
            {envStatus?.lspatch?.verified && (
              <span className="text-[11px] text-emerald-700">固定版本，已防篡改校验</span>
            )}
          </div>

          {/* SkyLeapModule APK */}
          <div className="p-3 rounded-lg border border-slate-200 bg-slate-50/60 flex flex-col justify-between gap-1.5">
            <div className="flex items-center justify-between">
              <span className="text-xs font-semibold text-slate-600">SkyLeapModule 注入包</span>
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
                ? '独立 Xposed Module 就绪'
                : isCorrupted
                ? '哈希或元数据不匹配 (已损坏)'
                : '未下载 (首次使用需启用)'}
            </div>
            {envStatus?.module?.verified && (
              <span className="text-[11px] text-slate-500 truncate" title={envStatus.module.path}>
                {envStatus.module.path.split(/[\\/]/).pop()}
              </span>
            )}
          </div>
        </div>
      </div>

      {/* 3. Input & Package Selection */}
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
                    : '将官方 SkyLeap 安装包拖入此处，或点击浏览文件'}
                </div>
                <div className="text-xs text-slate-500">
                  支持单文件 APK、Split APK 压缩包、.apks 以及 .xapk 格式 (最大 256MB)
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
          <div className="mt-4 p-4 rounded-xl border border-indigo-100 bg-indigo-50/40 flex flex-col sm:flex-row items-start sm:items-center justify-between gap-3 animate-in fade-in duration-200">
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
                  <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-amber-100 text-amber-800 border border-amber-200 inline-flex items-center gap-1">
                    <AlertTriangle className="w-3 h-3 text-amber-600" />
                    <span>非 SkyLeap 包</span>
                  </span>
                )}
              </div>
              <div className="text-xs text-slate-600 flex items-center gap-2">
                <span>
                  结构: {inspectedPkg.is_split ? `Split APK (${inspectedPkg.total_apks} 个子文件)` : '单 APK 文件'}
                </span>
                <span>·</span>
                <span className="truncate" title={inspectedPkg.file_path}>
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
                  <span>开始注入补丁</span>
                </>
              )}
            </button>
          </div>
        </div>
      </div>

      {/* 4. Patch Progress & Terminal Card */}
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
                '2. 解析安装包',
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
          {patchStatus?.done && !patchStatus.error && patchStatus.result && (
            <div className="mb-4 p-4 rounded-xl bg-emerald-50/80 border border-emerald-200 text-xs text-emerald-950 space-y-2">
              <div className="flex items-center gap-2 font-bold text-emerald-900 text-sm">
                <CheckCircle2 className="w-4 h-4 text-emerald-600" />
                <span>处理完成！已生成可供 Android 安装的产物</span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-slate-700 pt-1">
                <div>
                  <span className="font-semibold text-slate-900">输出类型：</span>
                  <span>{patchStatus.result.is_split ? 'Split APK 套件' : '单 APK 安装包'}</span>
                </div>
                <div>
                  <span className="font-semibold text-slate-900">总大小：</span>
                  <span>{(patchStatus.result.total_bytes / (1024 * 1024)).toFixed(2)} MB</span>
                </div>
                <div className="sm:col-span-2 truncate">
                  <span className="font-semibold text-slate-900">生成文件：</span>
                  <span className="font-mono text-slate-800">
                    {patchStatus.result.is_split ? patchStatus.result.apks_archive : patchStatus.result.single_apk}
                  </span>
                </div>
              </div>
              <div className="pt-2 border-t border-emerald-200/60 text-slate-600 space-y-1">
                <div className="font-bold text-slate-800">在手机上的安装建议：</div>
                {patchStatus.result.is_split ? (
                  <ul className="list-disc pl-4 space-y-0.5">
                    <li>
                      <span className="font-mono font-semibold">ADB:</span> adb install-multiple {patchStatus.result.split_dir}\*.apk
                    </li>
                    <li>
                      <span className="font-semibold">手机直接安装:</span> 将生成的 .apks 传至手机，使用 SAI (Split APKs Installer) 或 Shizuku 安装。
                    </li>
                  </ul>
                ) : (
                  <ul className="list-disc pl-4 space-y-0.5">
                    <li>
                      <span className="font-mono font-semibold">ADB:</span> adb install -r {patchStatus.result.single_apk}
                    </li>
                    <li>
                      <span className="font-semibold">手机直接安装:</span> 将生成的 APK 发送至手机并点击安装。
                    </li>
                  </ul>
                )}
              </div>
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

      {/* 5. User Notices & Security Disclaimers */}
      <div className="bg-slate-50 border border-slate-200/90 rounded-xl p-4 sm:p-5 text-xs text-slate-600 space-y-2">
        <div className="flex items-center gap-1.5 font-bold text-slate-800">
          <ShieldCheck className="w-4 h-4 text-indigo-600" />
          <span>使用须知与安全规范</span>
        </div>
        <ul className="list-disc pl-4 space-y-1 leading-relaxed text-slate-600">
          <li>
            <strong className="text-slate-700">私钥重签名说明：</strong> 处理后的安装包会使用本地独立测试密钥重新签名。Android 系统安全机制禁止直接覆盖安装签名不同的应用，因此安装前<strong>必须先卸载手机上的官方原版 SkyLeap</strong>。
          </li>
          <li>
            <strong className="text-slate-700">100% 本地运算：</strong> 所有解包、注入与签名过程完全在您的本地机器完成，绝不向任何外部服务器上传安装包或提取的数据。
          </li>
          <li>
            <strong className="text-slate-700">官方安装包合规：</strong> 本工具遵循严格合规治理，不分发、不篡改官方 SkyLeap 原始字节，用户需自行通过正规官方商店或合法备份获取原版安装包。
          </li>
          <li>
            <strong className="text-slate-700">后续步骤：</strong> 手机安装完毕后，请在 Android 手机上启动 GBF-Accelerator Android，启动 Core 并在首页选择启动 SkyLeap。
          </li>
        </ul>
      </div>
    </div>
  )
}
