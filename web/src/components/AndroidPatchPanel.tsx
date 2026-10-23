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
  cancelAndroidPatch,
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
import { useI18n } from '../i18n'

interface AndroidPatchPanelProps {
  showToast: (msg: string, type: 'success' | 'error' | 'info') => void
}

const isPermissionErrorText = (msg?: string): boolean => {
  if (!msg) return false
  const lower = msg.toLowerCase()
  return (
    lower.includes('写入权限') ||
    lower.includes('以管理员身份运行') ||
    lower.includes('access is denied') ||
    lower.includes('permission denied') ||
    lower.includes('write access denied')
  )
}

const formatBytes = (bytes?: number) => {
  if (!bytes || bytes <= 0) return '0 B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return `${(bytes / Math.pow(k, i)).toFixed(1)} ${sizes[i]}`
}

export const AndroidPatchPanel: React.FC<AndroidPatchPanelProps> = ({ showToast }) => {
  const { t } = useI18n()

  // Environment state
  const [envStatus, setEnvStatus] = useState<AndroidEnvStatus | null>(null)
  const [loadingEnv, setLoadingEnv] = useState<boolean>(false)
  const [isInstallingAll, setIsInstallingAll] = useState<boolean>(false)
  const [isUninstallingAll, setIsUninstallingAll] = useState<boolean>(false)
  const [installError, setInstallError] = useState<{
    message: string
    details?: string
    isPermission?: boolean
  } | null>(null)

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
      } else if (data.download?.stage === 'error' && data.download?.error) {
        setDownloadProgress(data.download)
        setInstallError({
          message: data.download.error,
          details: data.download.error_details,
          isPermission: data.download.is_permission_error || isPermissionErrorText(data.download.error),
        })
      }
    } catch (e: any) {
      showToast(t('modals.androidPatch.detectEnvFailed', { error: e.message }), 'error')
    } finally {
      setLoadingEnv(false)
    }
  }, [showToast, t])

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
            setInstallError(null)
            showToast(t('modals.androidPatch.componentDownloaded'), 'success')
            checkEnv()
          } else if (res.progress.stage === 'error') {
            const isPerm = Boolean(res.progress.is_permission_error || isPermissionErrorText(res.progress.error))
            setInstallError({
              message: res.progress.error || t('modals.androidPatch.componentInstallFailed'),
              details: res.progress.error_details,
              isPermission: isPerm,
            })
            showToast(t('modals.androidPatch.componentDownloadFailed', { error: res.progress.error || 'Unknown' }), 'error')
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
  }, [downloadProgress?.active, envStatus?.download?.active, checkEnv, showToast, t])

  const handleCancelDownload = async () => {
    try {
      await cancelAndroidComponentDownload()
      showToast(t('modals.androidPatch.downloadCancelled'), 'info')
      setDownloadProgress(null)
      await checkEnv()
    } catch (e: any) {
      showToast(t('modals.androidPatch.cancelDownloadFailed', { error: e.message }), 'error')
    }
  }

  const handleInstallAllEnv = async () => {
    setIsInstallingAll(true)
    setInstallError(null)
    try {
      await installAllAndroidEnv()
      showToast(t('modals.androidPatch.installAllStarted'), 'info')
      setDownloadProgress({
        active: true,
        percent: 0,
        stage: 'downloading',
      })
    } catch (e: any) {
      const errMsg = e.message || t('modals.androidPatch.installAllFailed', { error: '' })
      const isPerm = isPermissionErrorText(errMsg)
      setInstallError({
        message: errMsg,
        isPermission: isPerm,
      })
      setDownloadProgress({
        active: false,
        done: false,
        stage: 'error',
        percent: 0,
        error: errMsg,
        is_permission_error: isPerm,
      })
      showToast(t('modals.androidPatch.installAllFailed', { error: errMsg }), 'error')
    } finally {
      setIsInstallingAll(false)
    }
  }

  const handleUninstallAllEnv = async () => {
    if (!window.confirm(t('modals.androidPatch.uninstallConfirm'))) {
      return
    }
    setIsUninstallingAll(true)
    try {
      const res = await uninstallAllAndroidEnv()
      showToast(res.message || t('modals.androidPatch.uninstallSuccess'), 'success')
      await checkEnv()
    } catch (e: any) {
      showToast(t('modals.androidPatch.uninstallFailed', { error: e.message }), 'error')
    } finally {
      setIsUninstallingAll(false)
    }
  }

  const handleOpenBackup = async () => {
    try {
      const res = await openPatchBackupFolder()
      showToast(t('modals.androidPatch.openBackupFolder', { path: res.path || 'backups' }), 'info')
    } catch (e: any) {
      showToast(t('modals.androidPatch.openBackupFailed', { error: e.message }), 'error')
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
    showToast(t('modals.androidPatch.extractingApp', { name: appLabel }), 'info')
    try {
      const result = await extractDeviceApp(selectedDevice, pkg)
      setInspectedPkg(result)
      if (result.is_system_webview === false) {
        showToast(t('modals.androidPatch.extractedUnsupported', { desc: result.engine_desc || t('modals.androidPatch.nonSystemWebview') }), 'error')
      } else {
        showToast(t('modals.androidPatch.extractedSuccess', { name: appLabel, version: result.version_name || t('modals.androidPatch.readyFallback'), count: result.total_apks }), 'success')
      }
    } catch (e: any) {
      showToast(t('modals.androidPatch.extractFailed', { error: e.message }), 'error')
    } finally {
      setIsExtracting(false)
    }
  }

  const handleInstallToDevice = async (forceUninstall: boolean = false) => {
    if (!selectedDevice) {
      showToast(t('modals.androidPatch.noTargetDevice'), 'error')
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
          const appName = targetPkg === 'com.dena.skyleap' ? t('modals.androidPatch.skyleapBrowser') : targetPkg
          const verInfo = installedVer ? ` (v${installedVer})` : ''
          const confirmMsg = t('modals.androidPatch.overwriteConfirm', { name: appName, version: verInfo })
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
        showToast(t('modals.androidPatch.uninstallAndInstall'), 'info')
      } else {
        showToast(t('modals.androidPatch.pushingToDevice'), 'info')
      }

      const res = await installToDevice(selectedDevice, targetPkg, forceUninstall)
      setInstallResult(res)

      if (res.signature_mismatch) {
        setShowUninstallModal(true)
        showToast(t('modals.androidPatch.signatureConflict'), 'info')
      } else if (res.ok) {
        showToast(t('modals.androidPatch.installSuccess'), 'success')
      } else {
        showToast(t('modals.androidPatch.installFailed', { error: res.error || 'Unknown' }), 'error')
      }
    } catch (e: any) {
      showToast(t('modals.androidPatch.installFailed', { error: e.message }), 'error')
    } finally {
      setIsInstalling(false)
    }
  }

  const handleDownloadAdb = async () => {
    setIsDownloadingAdb(true)
    showToast(t('modals.androidPatch.downloadingAdb'), 'info')
    try {
      const res = await downloadAdbPlatformTools()
      if (res.ok) {
        showToast(t('modals.androidPatch.adbDownloaded'), 'success')
        await checkEnv()
        await refreshDevices()
      } else {
        showToast(t('modals.androidPatch.downloadAdbFailed', { error: res.message || 'Unknown' }), 'error')
      }
    } catch (e: any) {
      showToast(t('modals.androidPatch.downloadAdbFailed', { error: e.message }), 'error')
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
      showToast(t('modals.androidPatch.unsupportedFormat', { ext }), 'error')
      return
    }

    setIsUploading(true)
    setUploadPercent(0)
    try {
      showToast(t('modals.androidPatch.importingFile', { name: file.name }), 'info')
      const result = await uploadAndroidPackage(file, (percent) => {
        setUploadPercent(percent)
      })
      setInspectedPkg(result)
      if (result.is_system_webview === false) {
        showToast(t('modals.androidPatch.inspectUnsupportedEngine', { desc: result.engine_desc || t('modals.androidPatch.nonSystemWebview') }), 'error')
      } else {
        showToast(t('modals.androidPatch.inspectSuccess', { pkg: result.package_name, version: result.version_name }), 'success')
      }
    } catch (e: any) {
      showToast(t('modals.androidPatch.uploadInspectFailed', { error: e.message }), 'error')
    } finally {
      setIsUploading(false)
    }
  }

  const handleInspectManualPath = async () => {
    const path = manualPath.trim()
    if (!path) {
      showToast(t('modals.androidPatch.invalidPath'), 'error')
      return
    }

    setIsInspecting(true)
    try {
      const result = await inspectAndroidPackage(path)
      setInspectedPkg(result)
      if (result.is_system_webview === false) {
        showToast(t('modals.androidPatch.inspectUnsupportedEngine', { desc: result.engine_desc || t('modals.androidPatch.nonSystemWebview') }), 'error')
      } else {
        showToast(t('modals.androidPatch.inspectSuccess', { pkg: result.package_name, version: result.version_name }), 'success')
      }
    } catch (e: any) {
      showToast(t('modals.androidPatch.inspectFailed', { error: e.message }), 'error')
    } finally {
      setIsInspecting(false)
    }
  }

  const handleStartPatch = async () => {
    if (!inspectedPkg) {
      showToast(t('modals.androidPatch.noPackageSelected'), 'error')
      return
    }

    if (inspectedPkg.is_system_webview === false) {
      showToast(inspectedPkg.unsupported_reason || t('modals.androidPatch.onlyWebViewTip'), 'error')
      return
    }

    if (!envStatus?.components_verified) {
      showToast(t('modals.androidPatch.patchEnvNotReady'), 'error')
      return
    }

    if (patchStatus?.running) {
      showToast(t('modals.androidPatch.jobRunning'), 'info')
      return
    }

    setIsPatchStarting(true)
    try {
      await startAndroidPatch(inspectedPkg.file_path, outputDir, autoBackup)
      showToast(t('modals.androidPatch.jobStarted'), 'success')
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
      showToast(t('modals.androidPatch.jobStartFailed', { error: e.message }), 'error')
    } finally {
      setIsPatchStarting(false)
    }
  }

  const handleCancelPatch = async () => {
    try {
      await cancelAndroidPatch()
      showToast(t('modals.androidPatch.cancelRequested'), 'info')
      const status = await fetchAndroidPatchStatus().catch(() => null)
      if (status) {
        setPatchStatus(status)
      }
    } catch (e: any) {
      showToast(t('modals.androidPatch.cancelFailed', { error: e.message }), 'error')
    }
  }

  const handleOpenOutput = async () => {
    try {
      const res = await openPatchOutputFolder()
      showToast(t('modals.androidPatch.dirOpened', { path: res.path || outputDir }), 'info')
    } catch (e: any) {
      showToast(t('modals.androidPatch.dirOpenFailed', { error: e.message }), 'error')
    }
  }

  const isReady = Boolean(envStatus?.ready)
  const isRunning = Boolean(patchStatus?.running)
  const isCorrupted = Boolean(envStatus?.components_corrupted)
  const isModuleOutdated = Boolean(envStatus?.can_auto_update_module || envStatus?.module_outdated)
  const isComponentsVerified = Boolean(envStatus?.components_verified)
  const isDownloadActive = Boolean(downloadProgress?.active || envStatus?.download?.active)
  const isEnvironmentReady = isReady && isComponentsVerified

  // Active error detection for environment setup
  const activeErrorMsg =
    downloadProgress?.stage === 'error'
      ? downloadProgress.error
      : envStatus?.download?.stage === 'error'
      ? envStatus.download.error
      : installError?.message
  const activeErrorDetails =
    downloadProgress?.error_details ||
    envStatus?.download?.error_details ||
    installError?.details ||
    ''
  const isActivePermissionError = Boolean(
    downloadProgress?.is_permission_error ||
    envStatus?.download?.is_permission_error ||
    installError?.isPermission ||
    (activeErrorMsg && isPermissionErrorText(activeErrorMsg))
  )

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
                {t('modals.androidPatch.title')}
              </h1>
              <span className="text-xs font-mono font-semibold px-2 py-0.5 rounded-full bg-sky-500/20 text-sky-200 border border-sky-400/30">
                All-in-One v2.0
              </span>
              <span
                className={`text-xs font-semibold px-2.5 py-0.5 rounded-full border ${
                  isEnvironmentReady
                    ? 'bg-emerald-500/20 text-emerald-300 border-emerald-400/30'
                    : activeErrorMsg
                    ? 'bg-rose-500/20 text-rose-200 border-rose-400/30'
                    : 'bg-amber-500/20 text-amber-200 border-amber-400/30'
                }`}
              >
                {isEnvironmentReady
                  ? t('modals.androidPatch.envReady')
                  : activeErrorMsg
                  ? t('modals.androidPatch.envFailed')
                  : t('modals.androidPatch.envMissing')}
              </span>
            </div>
            <p className="text-xs sm:text-sm text-slate-300 leading-relaxed">
              {t('modals.androidPatch.desc')}
            </p>
          </div>

          <div className="flex items-center gap-2 self-end sm:self-center shrink-0 flex-wrap">
            <button
              type="button"
              onClick={handleOpenBackup}
              className="px-3 py-1.5 rounded-xl bg-white/10 hover:bg-white/15 active:bg-white/20 text-white text-xs font-semibold border border-white/20 transition-all flex items-center gap-1.5 cursor-pointer"
              title={t('modals.androidPatch.backupTooltip')}
            >
              <Archive className="w-3.5 h-3.5" />
              <span>{t('modals.androidPatch.backupBtn')}</span>
            </button>
            <button
              type="button"
              onClick={checkEnv}
              disabled={loadingEnv}
              className="px-3 py-1.5 rounded-xl bg-white/10 hover:bg-white/15 active:bg-white/20 text-white text-xs font-semibold border border-white/20 transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
              title={t('modals.androidPatch.checkDepsTooltip')}
            >
              <RefreshCw className={`w-3.5 h-3.5 ${loadingEnv ? 'animate-spin' : ''}`} />
              <span>{t('modals.androidPatch.checkDepsBtn')}</span>
            </button>
            {isEnvironmentReady ? (
              <button
                type="button"
                onClick={handleUninstallAllEnv}
                disabled={isUninstallingAll || isDownloadActive}
                className="px-3 py-1.5 rounded-xl bg-rose-500/20 hover:bg-rose-500/30 text-rose-200 text-xs font-semibold border border-rose-400/30 transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50"
                title={t('modals.androidPatch.uninstallEnvTooltip')}
              >
                <Trash2 className="w-3.5 h-3.5" />
                <span>{t('modals.androidPatch.uninstallEnvBtn')}</span>
              </button>
            ) : (
              <button
                type="button"
                onClick={handleInstallAllEnv}
                disabled={isInstallingAll || isDownloadActive}
                className="px-3.5 py-1.5 rounded-xl bg-indigo-500 hover:bg-indigo-600 active:bg-indigo-700 text-white text-xs font-bold shadow-sm transition-all flex items-center gap-1.5 cursor-pointer disabled:opacity-50 select-none"
              >
                <Zap className="w-3.5 h-3.5 fill-current" />
                <span>{t('modals.androidPatch.installEnvBtn')}</span>
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
                <span>{t('modals.androidPatch.toolchainSize')}</span>
                <span className="font-mono text-white font-semibold">
                  {formatBytes(envStatus?.tools_disk_bytes)}
                </span>
              </div>
            ) : null}
            {adbDevices.filter(d => d.state === 'device').length > 0 ? (
              <div className="flex items-center gap-1.5 text-emerald-300 font-medium">
                <span className="w-2 h-2 rounded-full bg-emerald-400 animate-pulse" />
                <span>{t('modals.androidPatch.devicesConnected', { count: adbDevices.filter(d => d.state === 'device').length })}</span>
              </div>
            ) : (
              <div className="flex items-center gap-1.5 text-slate-400">
                <Smartphone className="w-3.5 h-3.5" />
                <span>{t('modals.androidPatch.devicesNotConnected')}</span>
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
              {isJavaFound ? '✓' : '✗'} Java 21+{!isPortableJava && isJavaFound ? t('modals.androidPatch.systemTag') : ''}
            </span>

            {/* LSPatch */}
            <span className={`px-2 py-0.5 rounded border flex items-center gap-1 ${
              isLspatchInstalled
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : 'bg-white/5 border-white/10 text-slate-400'
            }`}>
              {isLspatchInstalled ? '✓' : '✗'} LSPatch
            </span>

            {/* Builtin Module */}
            <span className={`px-2 py-0.5 rounded border flex items-center gap-1 ${
              isModuleInstalled
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : 'bg-white/5 border-white/10 text-slate-400'
            }`}>
              {isModuleInstalled ? '✓' : '✗'} {t('modals.androidPatch.builtinModule')}
            </span>

            {/* ADB */}
            <span className={`px-2 py-0.5 rounded border flex items-center gap-1 ${
              isInternalAdb
                ? 'bg-emerald-500/10 border-emerald-500/30 text-emerald-300'
                : isAdbFound
                  ? 'bg-sky-500/10 border-sky-500/30 text-sky-300'
                  : 'bg-white/5 border-white/10 text-slate-400'
            }`}>
              {isAdbFound ? '✓' : '✗'} ADB{!isInternalAdb && isAdbFound ? t('modals.androidPatch.systemTag') : ''}
            </span>
          </div>
        </div>

        {/* Download Progress Bar if Active */}
        {isDownloadActive && (
          <div className="mt-3.5 pt-3.5 border-t border-white/10 space-y-2">
            <div className="flex items-center justify-between text-xs text-sky-200">
              <span className="font-semibold">
                {downloadProgress?.stage === 'verifying' ? t('modals.androidPatch.verifyingSha') : t('modals.androidPatch.transferring')}
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
                  {t('modals.androidPatch.cancel')}
                </button>
              </div>
            </div>
          </div>
        )}
      </div>

      {/* Conditional: If Environment is NOT ready, display error alert or clean onboarding card */}
      {!isEnvironmentReady ? (
        activeErrorMsg ? (
          <div className="bg-white rounded-xl border border-rose-200 shadow-2xs p-6 sm:p-8 space-y-5 text-left">
            {/* Error Banner */}
            <div className="rounded-xl bg-rose-50 border border-rose-200/80 p-4 sm:p-5 text-rose-900 space-y-3">
              <div className="flex items-start gap-3">
                <span className="p-2 rounded-lg bg-rose-100 text-rose-600 shrink-0 mt-0.5">
                  <AlertTriangle className="w-5 h-5" />
                </span>
                <div className="space-y-1 flex-1 min-w-0">
                  <h3 className="text-sm sm:text-base font-bold text-rose-900">
                    {isActivePermissionError ? t('modals.androidPatch.permissionErrorTitle') : t('modals.androidPatch.envConfigFailedTitle')}
                  </h3>
                  <p className="text-xs sm:text-sm text-rose-700 leading-relaxed">
                    {isActivePermissionError
                      ? t('modals.androidPatch.permissionErrorDesc')
                      : activeErrorMsg}
                  </p>
                </div>
              </div>

              {/* If Permission error, show clear dual actionable solutions */}
              {isActivePermissionError && (
                <div className="ml-11 bg-white/90 rounded-lg p-3 text-xs text-slate-700 border border-rose-200/60 space-y-2">
                  <div className="font-semibold text-rose-800">{t('modals.androidPatch.solutionTitle')}</div>
                  <div className="space-y-1.5 text-slate-600">
                    <div className="flex items-start gap-1.5">
                      <span className="font-bold text-indigo-600 shrink-0">{t('modals.androidPatch.solution1Title')}</span>
                      <span>{t('modals.androidPatch.solution1Desc')}</span>
                    </div>
                    <div className="flex items-start gap-1.5">
                      <span className="font-bold text-indigo-600 shrink-0">{t('modals.androidPatch.solution2Title')}</span>
                      <span>{t('modals.androidPatch.solution2Desc')}</span>
                    </div>
                  </div>
                </div>
              )}

              {/* Technical error details if available */}
              {activeErrorDetails && (
                <details className="ml-11 text-xs text-rose-700/80 cursor-pointer">
                  <summary className="font-medium hover:underline">{t('modals.androidPatch.viewErrorDetails')}</summary>
                  <pre className="mt-1.5 p-2 bg-rose-100/60 rounded text-[11px] font-mono text-rose-900 whitespace-pre-wrap break-all">
                    {activeErrorDetails}
                  </pre>
                </details>
              )}
            </div>

            {/* Component status overview */}
            <div className="bg-slate-50/70 border border-slate-200/60 rounded-xl p-4 space-y-3">
              <div className="text-xs font-bold text-slate-700 flex items-center justify-between">
                <span>{t('modals.androidPatch.compStatusTitle')}</span>
                <span className="text-[11px] font-normal text-slate-500">{t('modals.androidPatch.compStatusDesc')}</span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-xs">
                <div className="flex items-center justify-between p-2 rounded-lg bg-white border border-slate-200/80">
                  <span className="text-slate-600">{t('modals.androidPatch.compJava')}</span>
                  <span className={`font-mono font-medium ${isJavaFound ? 'text-emerald-600' : 'text-rose-500'}`}>
                    {isJavaFound ? t('modals.androidPatch.readyTag') : t('modals.androidPatch.notReadyTag')}
                  </span>
                </div>
                <div className="flex items-center justify-between p-2 rounded-lg bg-white border border-slate-200/80">
                  <span className="text-slate-600">{t('modals.androidPatch.compLspatch')}</span>
                  <span className={`font-mono font-medium ${isLspatchInstalled ? 'text-emerald-600' : 'text-rose-500'}`}>
                    {isLspatchInstalled ? t('modals.androidPatch.readyTag') : t('modals.androidPatch.notReadyTag')}
                  </span>
                </div>
                <div className="flex items-center justify-between p-2 rounded-lg bg-white border border-slate-200/80">
                  <span className="text-slate-600">{t('modals.androidPatch.compModule')}</span>
                  <span className={`font-mono font-medium ${isModuleInstalled ? 'text-emerald-600' : 'text-rose-500'}`}>
                    {isModuleInstalled ? t('modals.androidPatch.readyTag') : t('modals.androidPatch.notReadyTag')}
                  </span>
                </div>
                <div className="flex items-center justify-between p-2 rounded-lg bg-white border border-slate-200/80">
                  <span className="text-slate-600">{t('modals.androidPatch.compAdb')}</span>
                  <span className={`font-mono font-medium ${isAdbFound ? 'text-emerald-600' : 'text-rose-500'}`}>
                    {isAdbFound ? t('modals.androidPatch.readyTag') : t('modals.androidPatch.notReadyTag')}
                  </span>
                </div>
              </div>
            </div>

            {/* Action button */}
            <div className="flex items-center justify-center pt-1">
              <button
                type="button"
                onClick={handleInstallAllEnv}
                disabled={isInstallingAll || isDownloadActive}
                className="px-6 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-sm font-bold shadow-sm transition-all inline-flex items-center gap-2 cursor-pointer disabled:opacity-50 select-none"
              >
                <RefreshCw className={`w-4 h-4 ${isInstallingAll ? 'animate-spin' : ''}`} />
                <span>{isDownloadActive ? t('modals.androidPatch.componentDownloading') : t('modals.androidPatch.retryInstallAll')}</span>
              </button>
            </div>
          </div>
        ) : (
          <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-8 sm:p-10 text-center space-y-4">
            <div className="w-14 h-14 rounded-2xl bg-indigo-50 text-indigo-600 flex items-center justify-center mx-auto shadow-2xs">
              <Layers className="w-7 h-7" />
            </div>
            <div className="space-y-1.5 max-w-lg mx-auto">
              <h3 className="text-base sm:text-lg font-bold text-slate-800">
                {t('modals.androidPatch.onboardingTitle')}
              </h3>
              <p className="text-xs sm:text-sm text-slate-500 leading-relaxed">
                {t('modals.androidPatch.onboardingDesc')}
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
                <span>{isDownloadActive ? t('modals.androidPatch.componentDownloading') : t('modals.androidPatch.installAllEnvSize')}</span>
              </button>
            </div>
          </div>
        )
      ) : (
        <>

      {/* 5. Input & Package Selection */}
      <div className="bg-white rounded-xl border border-slate-200/90 shadow-2xs p-4 sm:p-5">
        <div className="flex items-center justify-between pb-3 border-b border-slate-100 mb-4">
          <div className="flex items-center gap-2">
            <FileBox className="w-4 h-4 text-indigo-600" />
            <h2 className="text-sm sm:text-base font-bold text-slate-800">
              {t('modals.androidPatch.selectOfficialPackage')}
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
              <span>{t('modals.androidPatch.tabDeviceShort')}</span>
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
              {t('modals.androidPatch.tabUploadShort')}
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
              {t('modals.androidPatch.tabPathShort')}
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
                  <div className="text-xs font-semibold text-slate-500">{t('modals.androidPatch.connectedDeviceTitle')}</div>
                  {adbDevices.length === 0 ? (
                    <div className="text-sm font-bold text-slate-700">{t('modals.androidPatch.noAdbDevice')}</div>
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
                        {t('modals.androidPatch.onlineReady')}
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
                <span>{t('modals.androidPatch.refreshDevicesBtn')}</span>
              </button>
            </div>

            {adbDevices.length === 0 ? (
              <div className="py-4 text-center space-y-2">
                <p className="text-xs text-slate-600 max-w-md mx-auto">
                  {t('modals.androidPatch.adbUsbPrompt1')}
                </p>
                <div className="text-[11px] text-slate-400">
                  {t('modals.androidPatch.adbUsbPrompt2')}
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
                      <span>{isDownloadingAdb ? t('modals.androidPatch.downloadingTools') : t('modals.androidPatch.downloadAdbBtn')}</span>
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
                      <span>{t('modals.androidPatch.targetAppLabel')}</span>
                      {isLoadingBrowsers && (
                        <span className="text-[11px] text-indigo-600 font-normal animate-pulse">
                          {t('modals.androidPatch.scanningApps')}
                        </span>
                      )}
                    </span>
                    <span className="text-[11px] text-slate-500">
                      {t('modals.androidPatch.detectTip')}
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
                            <optgroup label={t('modals.androidPatch.groupInstalledWebView')}>
                              {deviceBrowsers.filter(b => b.is_installed && b.is_system_webview !== false).map(b => (
                                <option key={b.package_name} value={b.package_name}>
                                  {b.is_skyleap ? '⭐ ' : '🌐 '}{b.label}
                                </option>
                              ))}
                            </optgroup>
                          )}
                          {deviceBrowsers.some(b => b.is_installed && b.is_system_webview === false) && (
                            <optgroup label={t('modals.androidPatch.groupInstalledStandalone')}>
                              {deviceBrowsers.filter(b => b.is_installed && b.is_system_webview === false).map(b => (
                                <option key={b.package_name} value={b.package_name}>
                                  🚫 {b.label}
                                </option>
                              ))}
                            </optgroup>
                          )}
                          {deviceBrowsers.some(b => !b.is_installed) && (
                            <optgroup label={t('modals.androidPatch.groupOther')}>
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
                          <option value="com.dena.skyleap">{t('modals.androidPatch.optSkyleap')}</option>
                          <option value="mark.via.gp">{t('modals.androidPatch.optVia')}</option>
                          <option value="com.android.chrome">{t('modals.androidPatch.optChrome')}</option>
                          <option value="com.kiwibrowser.browser">{t('modals.androidPatch.optKiwi')}</option>
                          <option value="com.microsoft.emmx">{t('modals.androidPatch.optEdge')}</option>
                          <option value="com.brave.browser">{t('modals.androidPatch.optBrave')}</option>
                        </>
                      )}
                      <option value="__custom__">{t('modals.androidPatch.optCustom')}</option>
                    </select>
                    {isCustomExtractPackage && (
                      <input
                        type="text"
                        value={targetExtractPackage}
                        onChange={(e) => setTargetExtractPackage(e.target.value.trim())}
                        placeholder={t('modals.androidPatch.customPackagePlaceholder')}
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
                            <strong>{t('modals.androidPatch.unsupportedBrowserTitle')}</strong>
                            <span>{t('modals.androidPatch.unsupportedBrowserDesc', { name: sel.label })}</span>
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
                          {deviceApp.package_name === 'com.dena.skyleap' ? t('modals.androidPatch.skyleapBrowser') : deviceApp.package_name}
                        </span>
                        <span className="text-[11px] font-mono px-1.5 py-0.5 rounded bg-slate-100 text-slate-700 font-semibold">
                          v{deviceApp.version_name || t('modals.androidPatch.installedStatus')}
                        </span>
                        <span className="text-[11px] text-emerald-700 font-medium">
                          {t('modals.androidPatch.splitComponentsCount', { count: deviceApp.total_apks ?? 1 })}
                        </span>
                      </div>
                      <div className="text-xs text-slate-500 font-mono">
                        {t('modals.androidPatch.packageNameLabel', { name: deviceApp.package_name })}
                      </div>
                    </div>
                    {isExtracting ? (
                      <div className="flex items-center gap-1.5 text-xs font-semibold text-indigo-700 bg-indigo-50 border border-indigo-200 px-3 py-1.5 rounded-lg shrink-0">
                        <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                        <span>{t('modals.androidPatch.extractingState')}</span>
                      </div>
                    ) : inspectedPkg && inspectedPkg.package_name === deviceApp.package_name ? (
                      <div className="flex items-center gap-1.5 text-xs font-semibold text-emerald-700 bg-emerald-50 border border-emerald-200 px-3 py-1.5 rounded-lg shrink-0">
                        <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600" />
                        <span>{t('modals.androidPatch.extractedLoadedState')}</span>
                      </div>
                    ) : (
                      <div className="flex items-center gap-1.5 text-xs font-medium text-slate-600 bg-slate-100 border border-slate-200 px-3 py-1.5 rounded-lg shrink-0">
                        <span className="w-2 h-2 rounded-full bg-indigo-500" />
                        <span>{t('modals.androidPatch.readyExtractState')}</span>
                      </div>
                    )}
                  </div>
                ) : (
                  <div className="bg-amber-50 border border-amber-200 rounded-lg p-3 text-xs text-amber-900 flex items-start gap-2">
                    <AlertTriangle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                    <div className="space-y-1">
                      <div className="font-semibold">
                        {t('modals.androidPatch.appNotFoundOnPhone', { pkg: targetExtractPackage || 'com.dena.skyleap' })}
                      </div>
                      <div className="text-amber-800 text-[11px]">
                        {t('modals.androidPatch.appNotFoundTip')}
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
                    ? t('modals.androidPatch.uploadingPercent', { percent: uploadPercent })
                    : t('modals.androidPatch.dropOrBrowse')}
                </div>
                <div className="text-xs text-slate-500">
                  {t('modals.androidPatch.dropTip')}
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
              {t('modals.androidPatch.inputPathLabel')}
            </label>
            <div className="flex items-center gap-2">
              <input
                type="text"
                value={manualPath}
                onChange={(e) => setManualPath(e.target.value)}
                placeholder={t('modals.androidPatch.pathPlaceholder')}
                className="flex-1 bg-slate-50 border border-slate-200 rounded-lg px-3 py-2 text-xs sm:text-sm font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-2 focus:ring-indigo-500/20 focus:border-indigo-500 transition-all"
              />
              <button
                type="button"
                onClick={handleInspectManualPath}
                disabled={isInspecting || !manualPath.trim()}
                className="px-4 py-2 rounded-lg bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-xs sm:text-sm font-semibold transition-all shadow-2xs disabled:opacity-50 cursor-pointer shrink-0"
              >
                {isInspecting ? t('modals.androidPatch.inspectingBtn') : t('modals.androidPatch.inspectBtn')}
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
                      <span>{inspectedPkg.engine_desc || t('modals.androidPatch.standaloneEngine')}</span>
                    </span>
                  ) : inspectedPkg.is_official_skyleap ? (
                    <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-emerald-100 text-emerald-800 border border-emerald-200 inline-flex items-center gap-1">
                      <CheckCircle2 className="w-3 h-3 text-emerald-600" />
                      <span>{t('modals.androidPatch.officialSkyleap')}</span>
                    </span>
                  ) : (
                    <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-sky-100 text-sky-800 border border-sky-200 inline-flex items-center gap-1">
                      <Globe className="w-3 h-3 text-sky-600" />
                      <span>{t('modals.androidPatch.genericWebView')}</span>
                    </span>
                  )}
                  {inspectedPkg.is_already_patched && (
                    <span className="text-xs font-bold px-2 py-0.5 rounded-full bg-amber-100 text-amber-800 border border-amber-200 inline-flex items-center gap-1">
                      <Zap className="w-3 h-3 text-amber-600" />
                      <span>{t('modals.androidPatch.lspatchEmbedded')}</span>
                    </span>
                  )}
                </div>
                <div className="text-xs text-slate-600 flex items-center gap-2 flex-wrap">
                  <span>
                    {t('modals.androidPatch.structureLabel', { structure: inspectedPkg.is_split ? t('modals.androidPatch.splitApkStructure', { count: inspectedPkg.total_apks }) : t('modals.androidPatch.singleApkStructure') })}
                  </span>
                  <span>·</span>
                  <span className="truncate max-w-xs sm:max-w-md" title={inspectedPkg.file_path}>
                    {t('modals.androidPatch.fileLabel', { file: inspectedPkg.base_input_name })}
                  </span>
                </div>
              </div>

              <div className="shrink-0 flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => setInspectedPkg(null)}
                  className="text-xs text-slate-500 hover:text-slate-800 px-2 py-1 rounded transition-colors"
                >
                  {t('modals.androidPatch.clearBtn')}
                </button>
              </div>
            </div>

            {/* Already Patched Note */}
            {inspectedPkg.is_already_patched && (
              <div className="bg-amber-50 border border-amber-200 rounded-lg p-3 text-xs text-amber-900 flex items-start gap-2.5">
                <AlertCircle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                <div className="space-y-1">
                  <div className="font-bold">{t('modals.androidPatch.lspatchDetectedTitle')}</div>
                  <div className="text-amber-800 text-[11px] leading-relaxed">
                    {t('modals.androidPatch.lspatchDetectedDesc', { version: inspectedPkg.original_version_name ? ` (v${inspectedPkg.original_version_name})` : '' })}
                  </div>
                </div>
              </div>
            )}

            {/* Kernel Support Note */}
            {inspectedPkg.is_system_webview === false && (
              <div className="bg-rose-50 border border-rose-200 rounded-lg p-3 text-xs text-rose-900 flex items-start gap-2.5">
                <AlertTriangle className="w-4 h-4 text-rose-600 shrink-0 mt-0.5" />
                <div className="space-y-1">
                  <div className="font-bold">{t('modals.androidPatch.unsupportedEngineTitle')}</div>
                  <div className="text-rose-800 text-[11px] leading-relaxed">
                    {inspectedPkg.unsupported_reason || t('modals.androidPatch.unsupportedEngineDesc')}
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
                  {t('modals.androidPatch.autoBackupCheckbox')}
                </span>
              </label>
              <button
                type="button"
                onClick={handleOpenBackup}
                className="text-xs text-indigo-700 hover:text-indigo-900 underline font-medium cursor-pointer"
              >
                {t('modals.androidPatch.openBackupDir')}
              </button>
            </div>
          </div>
        )}

        {/* Patch Options & Run Button */}
        <div className="mt-5 pt-4 border-t border-slate-100 flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
          <div className="flex items-center gap-2 flex-1 max-w-md">
            <label className="text-xs font-medium text-slate-600 shrink-0">{t('modals.androidPatch.outputDirLabel')}</label>
            <input
              type="text"
              value={outputDir}
              onChange={(e) => setOutputDir(e.target.value)}
              disabled={isRunning}
              className="flex-1 bg-slate-50 border border-slate-200 rounded-lg px-2.5 py-1.5 text-xs font-mono text-slate-800 focus:bg-white focus:outline-none focus:ring-1 focus:ring-sky-500"
            />
          </div>

          <div className="flex flex-col sm:flex-row items-end sm:items-center gap-2">
            {isModuleOutdated && (
              <span className="text-[11px] text-sky-700 font-medium">
                {t('modals.androidPatch.moduleNotLatestTip')}
              </span>
            )}
            {!isComponentsVerified && !isModuleOutdated && (
              <span className="text-[11px] text-amber-700 font-medium">
                {isCorrupted ? t('modals.androidPatch.componentCorruptedTip') : t('modals.androidPatch.componentNeedDownloadTip')}
              </span>
            )}
            {((inspectedPkg && inspectedPkg.is_system_webview === false) || isSelectedBrowserUnsupported) && (
              <span className="text-[11px] text-rose-600 font-medium">
                {t('modals.androidPatch.onlyWebViewTip')}
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
                    <span>{t('modals.androidPatch.extractAndInspectState')}</span>
                  </>
                ) : (
                  <>
                    <Zap className="w-4 h-4 fill-current" />
                    <span>{t('modals.androidPatch.extractFromPhoneBtn')}</span>
                  </>
                )}
              </button>
            ) : isPatchDoneStage ? (
              <div className="flex items-center gap-2 flex-wrap">
                <button
                  type="button"
                  onClick={() => setPatchStatus(null)}
                  className="px-3.5 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-slate-700 text-xs sm:text-sm font-semibold transition-all cursor-pointer"
                  title={t('modals.androidPatch.repatchTooltip')}
                >
                  {t('modals.androidPatch.repatchBtn')}
                </button>
                <button
                  type="button"
                  onClick={handleOpenOutput}
                  className="px-3.5 py-2.5 rounded-xl bg-slate-100 hover:bg-slate-200 text-slate-700 text-xs sm:text-sm font-semibold transition-all cursor-pointer"
                  title={t('modals.androidPatch.openOutputDirTooltip')}
                >
                  {t('modals.androidPatch.openOutputDirBtn')}
                </button>
                <button
                  type="button"
                  disabled={isInstalling || adbDevices.filter(d => d.state === 'device').length === 0}
                  onClick={() => handleInstallToDevice(false)}
                  title={adbDevices.filter(d => d.state === 'device').length === 0 ? t('modals.androidPatch.installToPhoneTooltipNoDev') : t('modals.androidPatch.installToPhoneTooltip')}
                  className="px-6 py-2.5 rounded-xl bg-emerald-600 hover:bg-emerald-700 active:bg-emerald-800 text-white text-xs sm:text-sm font-bold shadow-xs transition-all flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed select-none"
                >
                  {isInstalling ? (
                    <>
                      <RefreshCw className="w-4 h-4 animate-spin" />
                      <span>{t('modals.androidPatch.installingToPhoneState')}</span>
                    </>
                  ) : (
                    <>
                      <Zap className="w-4 h-4 fill-current" />
                      <span>{t('modals.androidPatch.installToPhoneBtn')}</span>
                    </>
                  )}
                </button>
              </div>
            ) : (
              <div className="flex items-center gap-2">
                {isRunning && (
                  <button
                    type="button"
                    onClick={handleCancelPatch}
                    className="px-4 py-2.5 rounded-xl bg-rose-50 hover:bg-rose-100 text-rose-700 border border-rose-200 text-xs sm:text-sm font-semibold transition-all cursor-pointer select-none"
                  >
                    {t('modals.androidPatch.cancelPatchBtn')}
                  </button>
                )}
                <button
                  type="button"
                  disabled={!isReady || !isComponentsVerified || !inspectedPkg || inspectedPkg.is_system_webview === false || isRunning || isPatchStarting}
                  onClick={handleStartPatch}
                  className="px-6 py-2.5 rounded-xl bg-indigo-600 hover:bg-indigo-700 active:bg-indigo-800 text-white text-xs sm:text-sm font-bold shadow-xs transition-all flex items-center justify-center gap-2 cursor-pointer disabled:opacity-50 disabled:cursor-not-allowed select-none"
                >
                  {isRunning ? (
                    <>
                      <RefreshCw className="w-4 h-4 animate-spin" />
                      <span>{t('modals.androidPatch.processingPercent', { percent: progressPercent })}</span>
                    </>
                  ) : !inspectedPkg ? (
                    <>
                      <Play className="w-4 h-4 fill-current opacity-60" />
                      <span>{t('modals.androidPatch.pleaseSelectPackage')}</span>
                    </>
                  ) : inspectedPkg.is_already_patched ? (
                    <>
                      <RefreshCw className="w-4 h-4" />
                      <span>{t('modals.androidPatch.repatchOrUpgrade')}</span>
                    </>
                  ) : (
                    <>
                      <Play className="w-4 h-4 fill-current" />
                      <span>{t('modals.androidPatch.startPatchBtn')}</span>
                    </>
                  )}
                </button>
              </div>
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
                {t('modals.androidPatch.progressAndLogs')}
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
                  <span>{t('modals.androidPatch.openOutputFolderBtn')}</span>
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
                {patchStatus?.stage_text || (isRunning ? t('modals.androidPatch.stageProcessing') : t('modals.androidPatch.stageReady'))}
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
                t('modals.androidPatch.step1'),
                t('modals.androidPatch.step2'),
                t('modals.androidPatch.step3'),
                t('modals.androidPatch.step4'),
                t('modals.androidPatch.step5'),
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
                <span>{t('modals.androidPatch.successAllBuiltIn')}</span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-slate-700 pt-1">
                <div>
                  <span className="font-semibold text-slate-900">{t('modals.androidPatch.outputTypeLabel')}</span>
                  <span>{isResultSplit ? t('modals.androidPatch.splitApkSuite', { count: resultTotalApks > 0 ? t('modals.androidPatch.splitApkSubCount', { count: resultTotalApks }) : '' }) : t('modals.androidPatch.singleApkSuite')}</span>
                </div>
                <div>
                  <span className="font-semibold text-slate-900">{t('modals.androidPatch.totalSizeLabel')}</span>
                  <span>{resultTotalMb} MB</span>
                </div>
                <div className="sm:col-span-2 truncate">
                  <span className="font-semibold text-slate-900">{t('modals.androidPatch.outputFilesLabel')}</span>
                  <span className="font-mono text-slate-800" title={resultGeneratedFile}>
                    {resultGeneratedFile}
                  </span>
                </div>
                {patchResult?.backup_path && (
                  <div className="sm:col-span-2 truncate text-slate-500">
                    <span className="font-semibold text-slate-700">{t('modals.androidPatch.backupOriginLabel')}</span>
                    <span className="font-mono" title={patchResult.backup_path}>
                      {patchResult.backup_path}
                    </span>
                  </div>
                )}
              </div>

              {/* Installation guidance */}
              <div className="pt-2 border-t border-emerald-200/60 text-slate-600 space-y-2">
                <div className="font-bold text-slate-800">{t('modals.androidPatch.phoneInstallGuide')}</div>
                <ul className="list-disc pl-4 space-y-0.5 pt-1">
                  <li>
                    <span className="font-semibold">{t('modals.androidPatch.oneClickGuideTitle')}</span>{t('modals.androidPatch.oneClickGuideDesc')}
                  </li>
                  <li>
                    <span className="font-semibold">{t('modals.androidPatch.manualGuideTitle')}</span>{isResultSplit ? t('modals.androidPatch.manualGuideSplitDesc') : t('modals.androidPatch.manualGuideSingleDesc')}
                  </li>
                  <li>
                    <span className="font-semibold">{t('modals.androidPatch.startUseTitle')}</span>{t('modals.androidPatch.startUseDesc')}
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
                        <span>{t('modals.androidPatch.deviceDetectedNotice', { model: adbDevices.find(d => d.serial === selectedDevice)?.model || selectedDevice })}</span>
                      </div>
                      <div className="text-[11px] text-slate-500">
                        {t('modals.androidPatch.noNeedManualInstall')}
                      </div>
                    </div>
                    {isInstalling ? (
                      <div className="flex items-center gap-1.5 text-xs font-semibold text-emerald-800 bg-emerald-100/90 border border-emerald-300 px-3 py-1.5 rounded-lg shrink-0">
                        <RefreshCw className="w-3.5 h-3.5 animate-spin" />
                        <span>{t('modals.androidPatch.pushingToPhoneState')}</span>
                      </div>
                    ) : (
                      <div className="flex items-center gap-1.5 text-xs font-medium text-emerald-800 bg-emerald-100/80 border border-emerald-300 px-3 py-1.5 rounded-lg shrink-0">
                        <CheckCircle2 className="w-3.5 h-3.5 text-emerald-600" />
                        <span>{t('modals.androidPatch.connectedReady')}</span>
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
                            {t('modals.androidPatch.sigConflictTitle')}
                          </div>
                          <div className="text-[11px] text-amber-800 leading-relaxed">
                            {t('modals.androidPatch.sigConflictDesc')}
                          </div>
                          <div className="text-[11px] font-bold text-rose-700 bg-rose-50 border border-rose-200 p-2 rounded">
                            {t('modals.androidPatch.sigConflictWarning')}
                          </div>
                        </div>
                      </div>
                      <div className="flex items-center justify-end gap-2 pt-1">
                        <button
                          type="button"
                          onClick={() => setShowUninstallModal(false)}
                          className="px-3 py-1.5 rounded-lg border border-slate-300 text-slate-700 hover:bg-slate-100 text-xs font-medium cursor-pointer"
                        >
                          {t('modals.androidPatch.cancelBtn')}
                        </button>
                        <button
                          type="button"
                          onClick={() => handleInstallToDevice(true)}
                          disabled={isInstalling}
                          className="px-3.5 py-1.5 rounded-lg bg-rose-600 hover:bg-rose-700 text-white text-xs font-bold flex items-center gap-1.5 cursor-pointer shadow-2xs"
                        >
                          <Trash2 className="w-3.5 h-3.5" />
                          <span>{t('modals.androidPatch.confirmReinstallBtn')}</span>
                        </button>
                      </div>
                    </div>
                  )}

                  {installResult?.ok && (
                    <div className="bg-emerald-100/70 border border-emerald-300 rounded-lg p-2.5 text-xs text-emerald-900 flex items-center gap-2">
                      <CheckCircle2 className="w-4 h-4 text-emerald-600 shrink-0" />
                      <span className="font-medium">{t('modals.androidPatch.deploySuccessNotice')}</span>
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
                <span className="font-bold">{t('modals.androidPatch.processFailed')}</span>
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
          <span>{t('modals.androidPatch.complianceTitle')}</span>
        </div>
        <ul className="list-disc pl-4 space-y-1 leading-relaxed text-slate-600">
          <li>
            <strong className="text-slate-700">{t('modals.androidPatch.complianceItem1Title')}</strong> {t('modals.androidPatch.complianceItem1Desc')}
          </li>
          <li>
            <strong className="text-slate-700">{t('modals.androidPatch.complianceItem2Title')}</strong> {t('modals.androidPatch.complianceItem2Desc')}
          </li>
          <li>
            <strong className="text-slate-700">{t('modals.androidPatch.complianceItem3Title')}</strong> {t('modals.androidPatch.complianceItem3Desc')}
          </li>
        </ul>
      </div>
    </div>
  )
}
