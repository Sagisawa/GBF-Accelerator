import React, { useState, useEffect, useRef, useCallback } from 'react'
import {
  Smartphone,
  UploadCloud,
  CheckCircle2,
  XCircle,
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
import { Card, CardHeader, InsetPanel, CompoundInputGroup } from './common/Card'
import { Badge } from './common/Badge'
import { Button } from './common/Button'
import { InfoTip } from './common/InfoTip'

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

const BROWSER_PKG_KEY_MAP: Record<string, string> = {
  'com.dena.skyleap': 'skyleap',
  'mark.via.gp': 'via',
  'mark.via': 'via',
  'com.xbrowser.play': 'x',
  'com.mb.browser': 'x',
  'com.android.browser': 'android',
  'acr.browser.lightning': 'lightning',
  'com.android.chrome': 'chrome',
  'com.kiwibrowser.browser': 'kiwi',
  'com.microsoft.emmx': 'edge',
  'com.brave.browser': 'brave',
  'com.sec.android.app.sbrowser': 'samsung',
  'org.mozilla.firefox': 'firefox',
  'com.opera.browser': 'opera',
  'com.quark.browser': 'quark',
  'com.ucmobile': 'uc',
  'com.UCMobile': 'uc',
  'com.baidu.searchbox': 'baidu',
  'com.heytap.browser': 'oppo',
  'com.mi.globalbrowser': 'xiaomi',
  'com.vivo.browser': 'vivo',
  'com.huawei.browser': 'huawei',
  'com.watanuki.tarou': 'tarou',
}

const getDeviceBrowserName = (b: DeviceBrowserItem, t: (k: string) => string): string => {
  const subkey = BROWSER_PKG_KEY_MAP[b.package_name]
  if (subkey) {
    return t(`modals.androidPatch.browserNames.${subkey}`)
  }
  if (b.is_skyleap || b.package_name.toLowerCase().includes('skyleap')) {
    return 'SkyLeap'
  }
  if (b.package_name.toLowerCase().includes('browser')) {
    return t('modals.androidPatch.browserNames.genericApp')
  }
  const match = b.label?.match(/^([^(]+)/)
  if (match && match[1].trim()) {
    return match[1].trim()
  }
  return b.package_name
}

const formatDeviceBrowserOption = (b: DeviceBrowserItem, t: (k: string) => string): string => {
  const name = getDeviceBrowserName(b, t)
  const isInstalled = Boolean(b.is_installed)
  const isWebView = b.is_system_webview !== false

  const tags: string[] = []

  if (b.package_name === 'com.dena.skyleap') {
    tags.push(t('modals.androidPatch.tagOfficial'))
    tags.push(t('modals.androidPatch.tagSystemWebView'))
    tags.push(isInstalled ? t('modals.androidPatch.tagInstalled') : t('modals.androidPatch.tagNotInstalled'))
    return `${name} (${tags.join(' · ')})`
  }

  tags.push(b.package_name)

  if (!isWebView) {
    tags.push(t('modals.androidPatch.tagStandaloneUnsupported'))
  } else if (isInstalled && (Boolean(BROWSER_PKG_KEY_MAP[b.package_name]) || b.package_name.toLowerCase().includes('skyleap'))) {
    tags.push(t('modals.androidPatch.tagSystemWebView'))
  }

  tags.push(isInstalled ? t('modals.androidPatch.tagInstalled') : t('modals.androidPatch.tagNotInstalled'))

  return `${name} (${tags.join(' · ')})`
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
  const [showCompliance, setShowCompliance] = useState<boolean>(false)
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

  const modeOrder: Array<'device' | 'upload' | 'path'> = ['device', 'upload', 'path']
  const modeIndex = Math.max(0, modeOrder.indexOf(inputMode))
  const onlineAdbCount = adbDevices.filter((d) => d.state === 'device').length

  return (
    <div className="w-full flex flex-col gap-4">
      {/* 1. Liquid Glass Android Cockpit (consistent with HeaderCockpit & Sidebar y=12px) */}
      <section className="liquid-glass rounded-[20px] p-4 sm:p-5">
        {/* Top Row: Identity, Status Badge, InfoTip & Quick Actions */}
        <div className="flex flex-col lg:flex-row lg:items-center justify-between gap-3 pb-3.5 hairline-b">
          <div className="flex items-start sm:items-center gap-3 min-w-0">
            <div
              className={`w-10 h-10 rounded-xl flex items-center justify-center shrink-0 ${
                isEnvironmentReady
                  ? 'bg-emerald-600 text-white shadow-[inset_0_1px_0_rgba(255,255,255,0.28),0_4px_14px_-4px_rgba(16,185,129,0.5)]'
                  : 'bg-sky-600 text-white shadow-[inset_0_1px_0_rgba(255,255,255,0.25),0_4px_14px_-4px_rgba(2,132,199,0.45)]'
              }`}
            >
              <Smartphone className="w-5 h-5" />
            </div>

            <div className="flex flex-col min-w-0 gap-1.5">
              <div className="flex items-center gap-2 flex-wrap">
                <h1 className="text-base sm:text-lg font-bold text-label-primary tracking-tight leading-snug inline-flex items-center gap-1.5">
                  <span>{t('modals.androidPatch.title')}</span>
                  <InfoTip text={t('modals.androidPatch.desc')} />
                </h1>
                <Badge variant={isEnvironmentReady ? 'green' : activeErrorMsg ? 'red' : 'amber'}>
                  {isEnvironmentReady
                    ? t('modals.androidPatch.envReady')
                    : activeErrorMsg
                    ? t('modals.androidPatch.envFailed')
                    : t('modals.androidPatch.envMissing')}
                </Badge>
              </div>
              <div className="flex items-center gap-x-3 gap-y-1 text-xs text-label-tertiary flex-wrap">
                {envStatus?.tools_disk_bytes ? (
                  <span className="inline-flex items-center gap-1 font-mono tnum whitespace-nowrap">
                    <HardDrive className="w-3.5 h-3.5 text-sky-500 shrink-0" />
                    <span>{t('modals.androidPatch.toolchainSize')}</span>
                    <strong className="text-label-secondary font-semibold">
                      {formatBytes(envStatus.tools_disk_bytes)}
                    </strong>
                  </span>
                ) : null}
                {onlineAdbCount > 0 ? (
                  <span className="inline-flex items-center gap-1.5 text-sys-green font-medium whitespace-nowrap">
                    <span className="w-2 h-2 rounded-full bg-emerald-500 shrink-0" />
                    <span>{t('modals.androidPatch.devicesConnected', { count: onlineAdbCount })}</span>
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1.5 whitespace-nowrap">
                    <Smartphone className="w-3.5 h-3.5 shrink-0" />
                    <span>{t('modals.androidPatch.devicesNotConnected')}</span>
                  </span>
                )}
              </div>
            </div>
          </div>

          {/* Right Quick Actions */}
          <div className="flex items-center gap-2 self-end lg:self-center shrink-0 flex-wrap">
            <Button
              type="button"
              variant="desktop"
              size="sm"
              icon={<Archive className="w-3.5 h-3.5" />}
              onClick={handleOpenBackup}
              title={t('modals.androidPatch.backupTooltip')}
            >
              {t('modals.androidPatch.backupBtn')}
            </Button>
            <Button
              type="button"
              variant="desktop"
              size="sm"
              icon={<RefreshCw className={`w-3.5 h-3.5 ${loadingEnv ? 'animate-spin' : ''}`} />}
              onClick={checkEnv}
              disabled={loadingEnv}
              title={t('modals.androidPatch.checkDepsTooltip')}
            >
              {t('modals.androidPatch.checkDepsBtn')}
            </Button>
            {isEnvironmentReady ? (
              <Button
                type="button"
                variant="desktop"
                size="sm"
                icon={<Trash2 className="w-3.5 h-3.5 text-rose-500" />}
                onClick={handleUninstallAllEnv}
                disabled={isUninstallingAll || isDownloadActive}
                title={t('modals.androidPatch.uninstallEnvTooltip')}
                className="hover:!text-rose-600 dark:hover:!text-rose-400 hover:!border-rose-500/30"
              >
                {t('modals.androidPatch.uninstallEnvBtn')}
              </Button>
            ) : (
              <Button
                type="button"
                variant="primary"
                size="sm"
                icon={<Zap className="w-3.5 h-3.5 fill-current" />}
                onClick={handleInstallAllEnv}
                disabled={isInstallingAll || isDownloadActive}
              >
                {t('modals.androidPatch.installEnvBtn')}
              </Button>
            )}
          </div>
        </div>

        {/* Bottom Inset Status Strip: Compact Single-Row Dependency Pills */}
        <InsetPanel className="mt-3 px-3 py-2 flex items-center justify-between gap-2 flex-wrap">
          <div className="flex items-center gap-1.5 flex-wrap text-[11px] font-mono">
            <span
              className={`px-2 py-0.5 rounded-full border inline-flex items-center gap-1 whitespace-nowrap ${
                isPortableJava
                  ? 'bg-sys-greenBg border-emerald-500/20 text-sys-green'
                  : isJavaFound
                  ? 'bg-sys-blueBg border-sky-500/20 text-sys-blue'
                  : 'bg-surface-active border-hairline text-label-tertiary'
              }`}
            >
              {isJavaFound ? (
                <CheckCircle2 className="w-3 h-3 shrink-0" />
              ) : (
                <XCircle className="w-3 h-3 shrink-0 opacity-60" />
              )}
              <span>Java 21+{!isPortableJava && isJavaFound ? t('modals.androidPatch.systemTag') : ''}</span>
            </span>

            <span
              className={`px-2 py-0.5 rounded-full border inline-flex items-center gap-1 whitespace-nowrap ${
                isLspatchInstalled
                  ? 'bg-sys-greenBg border-emerald-500/20 text-sys-green'
                  : 'bg-surface-active border-hairline text-label-tertiary'
              }`}
            >
              {isLspatchInstalled ? (
                <CheckCircle2 className="w-3 h-3 shrink-0" />
              ) : (
                <XCircle className="w-3 h-3 shrink-0 opacity-60" />
              )}
              <span>LSPatch</span>
            </span>

            <span
              className={`px-2 py-0.5 rounded-full border inline-flex items-center gap-1 whitespace-nowrap ${
                isModuleInstalled
                  ? 'bg-sys-greenBg border-emerald-500/20 text-sys-green'
                  : 'bg-surface-active border-hairline text-label-tertiary'
              }`}
            >
              {isModuleInstalled ? (
                <CheckCircle2 className="w-3 h-3 shrink-0" />
              ) : (
                <XCircle className="w-3 h-3 shrink-0 opacity-60" />
              )}
              <span>{t('modals.androidPatch.builtinModule')}</span>
            </span>

            <span
              className={`px-2 py-0.5 rounded-full border inline-flex items-center gap-1 whitespace-nowrap ${
                isInternalAdb
                  ? 'bg-sys-greenBg border-emerald-500/20 text-sys-green'
                  : isAdbFound
                  ? 'bg-sys-blueBg border-sky-500/20 text-sys-blue'
                  : 'bg-surface-active border-hairline text-label-tertiary'
              }`}
            >
              {isAdbFound ? (
                <CheckCircle2 className="w-3 h-3 shrink-0" />
              ) : (
                <XCircle className="w-3 h-3 shrink-0 opacity-60" />
              )}
              <span>ADB{!isInternalAdb && isAdbFound ? t('modals.androidPatch.systemTag') : ''}</span>
            </span>
          </div>
        </InsetPanel>

        {/* Download Progress Bar if Active */}
        {isDownloadActive && (
          <InsetPanel className="mt-3 space-y-2">
            <div className="flex items-center justify-between text-xs text-sys-blue">
              <span className="font-semibold">
                {downloadProgress?.stage === 'verifying'
                  ? t('modals.androidPatch.verifyingSha')
                  : t('modals.androidPatch.transferring')}
                {downloadProgress?.current_file ? ` (${downloadProgress.current_file})` : ''}
              </span>
              <span className="font-mono tnum font-bold">
                {Math.round(downloadProgress?.percent || 0)}%
              </span>
            </div>
            <div className="w-full bg-slate-500/15 rounded-full h-2 overflow-hidden">
              <div
                className="bg-sky-500 h-2 rounded-full transition-all duration-200"
                style={{ width: `${Math.round(downloadProgress?.percent || 0)}%` }}
              />
            </div>
            <div className="flex items-center justify-between text-[11px] text-label-tertiary font-mono tnum">
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
                  className="text-sys-red hover:underline font-semibold cursor-pointer"
                >
                  {t('modals.androidPatch.cancel')}
                </button>
              </div>
            </div>
          </InsetPanel>
        )}
      </section>

      {/* 2. Conditional: Environment NOT Ready — Error Alert or Onboarding */}
      {!isEnvironmentReady ? (
        activeErrorMsg ? (
          <Card className="border-rose-500/30 p-5 sm:p-7 space-y-4">
            <InsetPanel tone="rose" className="p-4 sm:p-5 space-y-3">
              <div className="flex items-start gap-3">
                <span className="p-2 rounded-lg bg-rose-500/15 text-rose-600 dark:text-rose-400 shrink-0 mt-0.5">
                  <AlertTriangle className="w-5 h-5" />
                </span>
                <div className="space-y-1 flex-1 min-w-0">
                  <h3 className="text-sm sm:text-base font-bold">
                    {isActivePermissionError
                      ? t('modals.androidPatch.permissionErrorTitle')
                      : t('modals.androidPatch.envConfigFailedTitle')}
                  </h3>
                  <p className="text-xs sm:text-sm opacity-90 leading-relaxed">
                    {isActivePermissionError
                      ? t('modals.androidPatch.permissionErrorDesc')
                      : activeErrorMsg}
                  </p>
                </div>
              </div>

              {isActivePermissionError && (
                <InsetPanel className="ml-11 text-xs space-y-2">
                  <div className="font-semibold text-sys-red">{t('modals.androidPatch.solutionTitle')}</div>
                  <div className="space-y-1.5 text-label-secondary">
                    <div className="flex items-start gap-1.5">
                      <span className="font-bold text-sys-blue shrink-0">{t('modals.androidPatch.solution1Title')}</span>
                      <span>{t('modals.androidPatch.solution1Desc')}</span>
                    </div>
                    <div className="flex items-start gap-1.5">
                      <span className="font-bold text-sys-blue shrink-0">{t('modals.androidPatch.solution2Title')}</span>
                      <span>{t('modals.androidPatch.solution2Desc')}</span>
                    </div>
                  </div>
                </InsetPanel>
              )}

              {activeErrorDetails && (
                <details className="ml-11 text-xs cursor-pointer">
                  <summary className="font-medium hover:underline">{t('modals.androidPatch.viewErrorDetails')}</summary>
                  <pre className="mt-1.5 p-2.5 inset-panel rounded-lg text-[11px] font-mono whitespace-pre-wrap break-all">
                    {activeErrorDetails}
                  </pre>
                </details>
              )}
            </InsetPanel>

            <InsetPanel className="p-4 space-y-3">
              <div className="text-xs font-bold text-label-primary flex items-center justify-between">
                <span>{t('modals.androidPatch.compStatusTitle')}</span>
                <span className="text-[11px] font-normal text-label-tertiary">
                  {t('modals.androidPatch.compStatusDesc')}
                </span>
              </div>
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-xs">
                <div className="flex items-center justify-between p-2.5 rounded-lg bg-surface-active border border-hairline">
                  <span className="text-label-secondary">{t('modals.androidPatch.compJava')}</span>
                  <span className={`font-mono font-medium ${isJavaFound ? 'text-sys-green' : 'text-sys-red'}`}>
                    {isJavaFound ? t('modals.androidPatch.readyTag') : t('modals.androidPatch.notReadyTag')}
                  </span>
                </div>
                <div className="flex items-center justify-between p-2.5 rounded-lg bg-surface-active border border-hairline">
                  <span className="text-label-secondary">{t('modals.androidPatch.compLspatch')}</span>
                  <span className={`font-mono font-medium ${isLspatchInstalled ? 'text-sys-green' : 'text-sys-red'}`}>
                    {isLspatchInstalled ? t('modals.androidPatch.readyTag') : t('modals.androidPatch.notReadyTag')}
                  </span>
                </div>
                <div className="flex items-center justify-between p-2.5 rounded-lg bg-surface-active border border-hairline">
                  <span className="text-label-secondary">{t('modals.androidPatch.compModule')}</span>
                  <span className={`font-mono font-medium ${isModuleInstalled ? 'text-sys-green' : 'text-sys-red'}`}>
                    {isModuleInstalled ? t('modals.androidPatch.readyTag') : t('modals.androidPatch.notReadyTag')}
                  </span>
                </div>
                <div className="flex items-center justify-between p-2.5 rounded-lg bg-surface-active border border-hairline">
                  <span className="text-label-secondary">{t('modals.androidPatch.compAdb')}</span>
                  <span className={`font-mono font-medium ${isAdbFound ? 'text-sys-green' : 'text-sys-red'}`}>
                    {isAdbFound ? t('modals.androidPatch.readyTag') : t('modals.androidPatch.notReadyTag')}
                  </span>
                </div>
              </div>
            </InsetPanel>

            <div className="flex items-center justify-center pt-1">
              <Button
                type="button"
                variant="primary"
                size="lg"
                icon={<RefreshCw className={`w-4 h-4 ${isInstallingAll ? 'animate-spin' : ''}`} />}
                onClick={handleInstallAllEnv}
                disabled={isInstallingAll || isDownloadActive}
                className="rounded-xl"
              >
                {isDownloadActive
                  ? t('modals.androidPatch.componentDownloading')
                  : t('modals.androidPatch.retryInstallAll')}
              </Button>
            </div>
          </Card>
        ) : (
          <Card className="p-8 sm:p-10 text-center items-center">
            <div className="w-14 h-14 rounded-2xl bg-sys-blueBg text-sys-blue border border-sky-500/20 flex items-center justify-center mx-auto">
              <Layers className="w-7 h-7" />
            </div>
            <div className="space-y-1.5 max-w-lg mx-auto">
              <h3 className="text-base sm:text-lg font-bold text-label-primary">
                {t('modals.androidPatch.onboardingTitle')}
              </h3>
              <p className="text-xs sm:text-sm text-label-tertiary leading-relaxed">
                {t('modals.androidPatch.onboardingDesc')}
              </p>
            </div>
            <div className="pt-2">
              <Button
                type="button"
                variant="primary"
                size="lg"
                icon={<Zap className="w-4 h-4 fill-current" />}
                onClick={handleInstallAllEnv}
                disabled={isInstallingAll || isDownloadActive}
                className="rounded-xl"
              >
                {isDownloadActive
                  ? t('modals.androidPatch.componentDownloading')
                  : t('modals.androidPatch.installAllEnvSize')}
              </Button>
            </div>
          </Card>
        )
      ) : (
        <>
          {/* 3. Input & Package Selection Card */}
          <Card>
            <CardHeader
              icon={<FileBox className="w-[18px] h-[18px]" strokeWidth={1.85} />}
              title={t('modals.androidPatch.selectOfficialPackage')}
              badge={
                <div
                  role="tablist"
                  aria-label={t('modals.androidPatch.selectOfficialPackage')}
                  className="relative inline-grid grid-cols-3 items-center rounded-full inset-panel p-1 text-xs font-medium select-none"
                >
                  <div
                    className="liquid-pill absolute top-1 bottom-1 left-1 w-[calc((100%-8px)/3)] rounded-full transition-transform duration-[240ms] ease-fluid pointer-events-none"
                    style={{ transform: `translateX(${modeIndex * 100}%)` }}
                    aria-hidden="true"
                  />
                  <button
                    type="button"
                    role="tab"
                    aria-selected={inputMode === 'device'}
                    onClick={() => setInputMode('device')}
                    className={`relative z-10 px-2.5 py-1 rounded-full transition-colors cursor-pointer inline-flex items-center justify-center gap-1 whitespace-nowrap ${
                      inputMode === 'device'
                        ? 'text-sky-700 dark:text-sky-300 font-semibold'
                        : 'text-label-tertiary hover:text-label-primary'
                    }`}
                  >
                    <Smartphone className="w-3.5 h-3.5 shrink-0" />
                    <span>{t('modals.androidPatch.tabDeviceShort')}</span>
                    {onlineAdbCount > 0 && (
                      <span className="w-1.5 h-1.5 rounded-full bg-emerald-500 shrink-0" />
                    )}
                  </button>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={inputMode === 'upload'}
                    onClick={() => setInputMode('upload')}
                    className={`relative z-10 px-2.5 py-1 rounded-full transition-colors cursor-pointer inline-flex items-center justify-center whitespace-nowrap ${
                      inputMode === 'upload'
                        ? 'text-sky-700 dark:text-sky-300 font-semibold'
                        : 'text-label-tertiary hover:text-label-primary'
                    }`}
                  >
                    {t('modals.androidPatch.tabUploadShort')}
                  </button>
                  <button
                    type="button"
                    role="tab"
                    aria-selected={inputMode === 'path'}
                    onClick={() => setInputMode('path')}
                    className={`relative z-10 px-2.5 py-1 rounded-full transition-colors cursor-pointer inline-flex items-center justify-center whitespace-nowrap ${
                      inputMode === 'path'
                        ? 'text-sky-700 dark:text-sky-300 font-semibold'
                        : 'text-label-tertiary hover:text-label-primary'
                    }`}
                  >
                    {t('modals.androidPatch.tabPathShort')}
                  </button>
                </div>
              }
            />

            {/* Device Extract Mode */}
            {inputMode === 'device' && (
              <InsetPanel className="p-4 space-y-3.5">
                <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 pb-3 hairline-b">
                  <div className="flex items-center gap-2.5">
                    <div className="w-9 h-9 rounded-xl bg-sys-blueBg text-sys-blue border border-sky-500/20 flex items-center justify-center shrink-0">
                      <Smartphone className="w-4.5 h-4.5" />
                    </div>
                    <div>
                      <div className="text-xs font-medium text-label-tertiary">
                        {t('modals.androidPatch.connectedDeviceTitle')}
                      </div>
                      {adbDevices.length === 0 ? (
                        <div className="text-sm font-semibold text-label-primary">
                          {t('modals.androidPatch.noAdbDevice')}
                        </div>
                      ) : (
                        <div className="flex items-center gap-2 flex-wrap mt-0.5">
                          <select
                            value={selectedDevice}
                            onChange={(e) => setSelectedDevice(e.target.value)}
                            className="ui-input text-xs sm:text-sm font-semibold rounded-lg px-2.5 py-1 cursor-pointer"
                          >
                            {adbDevices.map((d) => (
                              <option key={d.serial} value={d.serial}>
                                {d.model} ({d.serial}) {d.state !== 'device' ? `[${d.state}]` : ''}
                              </option>
                            ))}
                          </select>
                          <Badge variant="green">{t('modals.androidPatch.onlineReady')}</Badge>
                        </div>
                      )}
                    </div>
                  </div>
                  <Button
                    type="button"
                    variant="desktop"
                    size="sm"
                    icon={<RefreshCw className="w-3.5 h-3.5" />}
                    onClick={refreshDevices}
                    className="self-start sm:self-center"
                  >
                    {t('modals.androidPatch.refreshDevicesBtn')}
                  </Button>
                </div>

                {adbDevices.length === 0 ? (
                  <div className="py-4 text-center space-y-2">
                    <p className="text-xs text-label-secondary max-w-md mx-auto">
                      {t('modals.androidPatch.adbUsbPrompt1')}
                    </p>
                    <div className="text-[11px] text-label-tertiary">
                      {t('modals.androidPatch.adbUsbPrompt2')}
                    </div>
                    {!envStatus?.adb?.found && (
                      <div className="pt-2">
                        <Button
                          type="button"
                          variant="primary"
                          size="sm"
                          icon={<Download className="w-3.5 h-3.5" />}
                          onClick={handleDownloadAdb}
                          disabled={isDownloadingAdb}
                        >
                          {isDownloadingAdb
                            ? t('modals.androidPatch.downloadingTools')
                            : t('modals.androidPatch.downloadAdbBtn')}
                        </Button>
                      </div>
                    )}
                  </div>
                ) : (
                  <div className="space-y-3">
                    {/* Target App Selector */}
                    <div className="rounded-xl border border-hairline bg-white/60 dark:bg-white/[0.03] p-3 space-y-2">
                      <div className="flex items-center justify-between gap-2 text-xs">
                        <span className="font-semibold text-label-primary flex items-center gap-1.5">
                          <span>{t('modals.androidPatch.targetAppLabel')}</span>
                          <InfoTip text={t('modals.androidPatch.detectTip')} />
                          {isLoadingBrowsers && (
                            <span className="text-[11px] text-sys-blue font-normal">
                              {t('modals.androidPatch.scanningApps')}
                            </span>
                          )}
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
                          className="ui-input text-xs font-semibold rounded-lg px-2.5 py-1.5 cursor-pointer flex-1"
                        >
                          {deviceBrowsers.length > 0 ? (
                            <>
                              {deviceBrowsers.some((b) => b.is_installed && b.is_system_webview !== false) && (
                                <optgroup label={t('modals.androidPatch.groupInstalledWebView')}>
                                  {deviceBrowsers
                                    .filter((b) => b.is_installed && b.is_system_webview !== false)
                                    .map((b) => (
                                      <option key={b.package_name} value={b.package_name}>
                                        {b.is_skyleap ? '[SkyLeap] ' : ''}
                                        {formatDeviceBrowserOption(b, t)}
                                      </option>
                                    ))}
                                </optgroup>
                              )}
                              {deviceBrowsers.some((b) => b.is_installed && b.is_system_webview === false) && (
                                <optgroup label={t('modals.androidPatch.groupInstalledStandalone')}>
                                  {deviceBrowsers
                                    .filter((b) => b.is_installed && b.is_system_webview === false)
                                    .map((b) => (
                                      <option key={b.package_name} value={b.package_name}>
                                        {formatDeviceBrowserOption(b, t)}
                                      </option>
                                    ))}
                                </optgroup>
                              )}
                              {deviceBrowsers.some((b) => !b.is_installed) && (
                                <optgroup label={t('modals.androidPatch.groupOther')}>
                                  {deviceBrowsers
                                    .filter((b) => !b.is_installed)
                                    .map((b) => (
                                      <option key={b.package_name} value={b.package_name}>
                                        {formatDeviceBrowserOption(b, t)}
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
                            className="ui-input text-xs font-mono rounded-lg px-2.5 py-1.5 flex-1"
                          />
                        )}
                      </div>
                      {(() => {
                        const sel = deviceBrowsers.find((b) => b.package_name === targetExtractPackage)
                        if (sel && sel.is_system_webview === false) {
                          return (
                            <InsetPanel tone="amber" className="p-2.5 text-xs flex items-start gap-2 mt-1">
                              <AlertTriangle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                              <div className="text-[11px] leading-relaxed">
                                <strong>{t('modals.androidPatch.unsupportedBrowserTitle')}</strong>
                                <span>
                                  {t('modals.androidPatch.unsupportedBrowserDesc', {
                                    name: getDeviceBrowserName(sel, t),
                                  })}
                                </span>
                              </div>
                            </InsetPanel>
                          )
                        }
                        return null
                      })()}
                    </div>

                    {deviceApp?.installed ? (
                      <div className="rounded-xl border border-emerald-500/25 bg-sys-greenBg/40 p-3.5 flex flex-col sm:flex-row sm:items-center justify-between gap-3">
                        <div className="space-y-1">
                          <div className="flex items-center gap-2 flex-wrap">
                            <span className="font-bold text-label-primary text-sm">
                              {deviceApp.package_name === 'com.dena.skyleap'
                                ? t('modals.androidPatch.skyleapBrowser')
                                : deviceApp.package_name}
                            </span>
                            <Badge mono>
                              v{deviceApp.version_name || t('modals.androidPatch.installedStatus')}
                            </Badge>
                            <span className="text-[11px] text-sys-green font-medium">
                              {t('modals.androidPatch.splitComponentsCount', { count: deviceApp.total_apks ?? 1 })}
                            </span>
                          </div>
                          <div className="text-xs text-label-tertiary font-mono">
                            {t('modals.androidPatch.packageNameLabel', { name: deviceApp.package_name })}
                          </div>
                        </div>
                        {isExtracting ? (
                          <Badge variant="blue" className="px-3 py-1.5">
                            <RefreshCw className="w-3.5 h-3.5 animate-spin mr-1 inline" />
                            {t('modals.androidPatch.extractingState')}
                          </Badge>
                        ) : inspectedPkg && inspectedPkg.package_name === deviceApp.package_name ? (
                          <Badge variant="green" className="px-3 py-1.5">
                            <CheckCircle2 className="w-3.5 h-3.5 mr-1 inline" />
                            {t('modals.androidPatch.extractedLoadedState')}
                          </Badge>
                        ) : (
                          <Badge variant="neutral" dot className="px-3 py-1.5">
                            {t('modals.androidPatch.readyExtractState')}
                          </Badge>
                        )}
                      </div>
                    ) : (
                      <InsetPanel tone="amber" className="p-3 text-xs flex items-start gap-2">
                        <AlertTriangle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                        <div className="space-y-1">
                          <div className="font-semibold">
                            {t('modals.androidPatch.appNotFoundOnPhone', {
                              pkg: targetExtractPackage || 'com.dena.skyleap',
                            })}
                          </div>
                          <div className="text-[11px] opacity-90">{t('modals.androidPatch.appNotFoundTip')}</div>
                        </div>
                      </InsetPanel>
                    )}
                  </div>
                )}
              </InsetPanel>
            )}

            {/* Upload Mode Dropzone */}
            {inputMode === 'upload' && (
              <div
                onDragOver={handleDragOver}
                onDragLeave={handleDragLeave}
                onDrop={handleDrop}
                onClick={() => fileInputRef.current?.click()}
                className={`border-2 border-dashed rounded-xl p-7 sm:p-9 text-center cursor-pointer transition-all ${
                  isDragging
                    ? 'border-sky-500 bg-sys-blueBg scale-[1.005]'
                    : 'border-hairline hover:border-sky-500/50 inset-panel'
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
                  <div className="w-11 h-11 rounded-full bg-sys-blueBg text-sys-blue border border-sky-500/20 flex items-center justify-center">
                    <UploadCloud className="w-5.5 h-5.5" />
                  </div>
                  <div className="space-y-1">
                    <div className="text-sm font-semibold text-label-primary">
                      {isUploading
                        ? t('modals.androidPatch.uploadingPercent', { percent: uploadPercent })
                        : t('modals.androidPatch.dropOrBrowse')}
                    </div>
                    <div className="text-xs text-label-tertiary">{t('modals.androidPatch.dropTip')}</div>
                  </div>
                  {isUploading && (
                    <div className="w-48 bg-slate-500/15 rounded-full h-1.5 overflow-hidden mt-2">
                      <div
                        className="bg-sky-500 h-1.5 rounded-full transition-all duration-150"
                        style={{ width: `${uploadPercent}%` }}
                      />
                    </div>
                  )}
                </div>
              </div>
            )}

            {/* Path Input Mode */}
            {inputMode === 'path' && (
              <div className="space-y-2">
                <label className="text-xs text-label-secondary font-medium block">
                  {t('modals.androidPatch.inputPathLabel')}
                </label>
                <CompoundInputGroup>
                  <input
                    type="text"
                    value={manualPath}
                    onChange={(e) => setManualPath(e.target.value)}
                    placeholder={t('modals.androidPatch.pathPlaceholder')}
                    className="flex-1 px-2.5 py-1 text-xs sm:text-[13px] font-mono"
                  />
                  <Button
                    type="button"
                    variant="primary"
                    size="sm"
                    onClick={handleInspectManualPath}
                    disabled={isInspecting || !manualPath.trim()}
                  >
                    {isInspecting ? t('modals.androidPatch.inspectingBtn') : t('modals.androidPatch.inspectBtn')}
                  </Button>
                </CompoundInputGroup>
              </div>
            )}

            {/* Inspected Package Card */}
            {inspectedPkg && (
              <InsetPanel className="mt-4 p-4 space-y-3 border-sky-500/25 bg-sys-blueBg/35 animate-fade-soft">
                <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-2 pb-2.5 hairline-b">
                  <div className="space-y-1.5 min-w-0">
                    <div className="flex items-center gap-2 flex-wrap">
                      <span className="text-xs sm:text-[13px] font-bold text-label-primary font-mono">
                        {inspectedPkg.package_name}
                      </span>
                      <Badge mono>v{inspectedPkg.version_name}</Badge>
                      {inspectedPkg.is_system_webview === false ? (
                        <Badge variant="red">
                          <AlertTriangle className="w-3 h-3 mr-1 inline" />
                          {inspectedPkg.engine_desc || t('modals.androidPatch.standaloneEngine')}
                        </Badge>
                      ) : inspectedPkg.is_official_skyleap ? (
                        <Badge variant="green">
                          <CheckCircle2 className="w-3 h-3 mr-1 inline" />
                          {t('modals.androidPatch.officialSkyleap')}
                        </Badge>
                      ) : (
                        <Badge variant="blue">
                          <Globe className="w-3 h-3 mr-1 inline" />
                          {t('modals.androidPatch.genericWebView')}
                        </Badge>
                      )}
                      {inspectedPkg.is_already_patched && (
                        <Badge variant="amber">
                          <Zap className="w-3 h-3 mr-1 inline" />
                          {t('modals.androidPatch.lspatchEmbedded')}
                        </Badge>
                      )}
                    </div>
                    <div className="text-xs text-label-secondary flex items-center gap-2 flex-wrap">
                      <span>
                        {t('modals.androidPatch.structureLabel', {
                          structure: inspectedPkg.is_split
                            ? t('modals.androidPatch.splitApkStructure', { count: inspectedPkg.total_apks })
                            : t('modals.androidPatch.singleApkStructure'),
                        })}
                      </span>
                      <span className="opacity-40">·</span>
                      <span className="truncate max-w-xs sm:max-w-md" title={inspectedPkg.file_path}>
                        {t('modals.androidPatch.fileLabel', { file: inspectedPkg.base_input_name })}
                      </span>
                    </div>
                  </div>

                  <Button
                    type="button"
                    variant="ghost"
                    size="xs"
                    onClick={() => setInspectedPkg(null)}
                    className="shrink-0"
                  >
                    {t('modals.androidPatch.clearBtn')}
                  </Button>
                </div>

                {inspectedPkg.is_already_patched && (
                  <InsetPanel tone="amber" className="p-3 text-xs flex items-start gap-2.5">
                    <AlertCircle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                    <div className="space-y-1">
                      <div className="font-bold">{t('modals.androidPatch.lspatchDetectedTitle')}</div>
                      <div className="text-[11px] leading-relaxed opacity-90">
                        {t('modals.androidPatch.lspatchDetectedDesc', {
                          version: inspectedPkg.original_version_name
                            ? ` (v${inspectedPkg.original_version_name})`
                            : '',
                        })}
                      </div>
                    </div>
                  </InsetPanel>
                )}

                {inspectedPkg.is_system_webview === false && (
                  <InsetPanel tone="rose" className="p-3 text-xs flex items-start gap-2.5">
                    <AlertTriangle className="w-4 h-4 text-rose-600 shrink-0 mt-0.5" />
                    <div className="space-y-1">
                      <div className="font-bold">{t('modals.androidPatch.unsupportedEngineTitle')}</div>
                      <div className="text-[11px] leading-relaxed opacity-90">
                        {inspectedPkg.unsupported_reason || t('modals.androidPatch.unsupportedEngineDesc')}
                      </div>
                    </div>
                  </InsetPanel>
                )}

                <div className="bg-white/65 dark:bg-white/[0.04] p-3 rounded-xl border border-hairline flex items-center justify-between gap-2">
                  <label className="flex items-center gap-2 cursor-pointer select-none">
                    <input
                      type="checkbox"
                      checked={autoBackup}
                      onChange={(e) => setAutoBackup(e.target.checked)}
                      className="w-4 h-4 rounded border-slate-300"
                    />
                    <span className="text-xs font-medium text-label-primary">
                      {t('modals.androidPatch.autoBackupCheckbox')}
                    </span>
                  </label>
                  <button
                    type="button"
                    onClick={handleOpenBackup}
                    className="text-xs text-sys-blue hover:underline font-medium cursor-pointer shrink-0"
                  >
                    {t('modals.androidPatch.openBackupDir')}
                  </button>
                </div>
              </InsetPanel>
            )}

            {/* Patch Options & Run Button */}
            <div className="mt-4 pt-4 hairline-t flex flex-col sm:flex-row items-stretch sm:items-center justify-between gap-3">
              <div className="flex items-center gap-2 flex-1 max-w-md">
                <label className="text-xs font-medium text-label-secondary shrink-0">
                  {t('modals.androidPatch.outputDirLabel')}
                </label>
                <input
                  type="text"
                  value={outputDir}
                  onChange={(e) => setOutputDir(e.target.value)}
                  disabled={isRunning}
                  className="ui-input flex-1 rounded-lg px-2.5 py-1.5 text-xs font-mono"
                />
              </div>

              <div className="flex flex-col sm:flex-row items-end sm:items-center gap-2">
                {isModuleOutdated && (
                  <span className="text-[11px] text-sys-blue font-medium">
                    {t('modals.androidPatch.moduleNotLatestTip')}
                  </span>
                )}
                {!isComponentsVerified && !isModuleOutdated && (
                  <span className="text-[11px] text-sys-amber font-medium">
                    {isCorrupted
                      ? t('modals.androidPatch.componentCorruptedTip')
                      : t('modals.androidPatch.componentNeedDownloadTip')}
                  </span>
                )}
                {((inspectedPkg && inspectedPkg.is_system_webview === false) || isSelectedBrowserUnsupported) && (
                  <span className="text-[11px] text-sys-red font-medium">
                    {t('modals.androidPatch.onlyWebViewTip')}
                  </span>
                )}
                {isExtractStage ? (
                  <Button
                    type="button"
                    variant="primary"
                    size="lg"
                    icon={
                      isExtracting ? (
                        <RefreshCw className="w-4 h-4 animate-spin" />
                      ) : (
                        <Zap className="w-4 h-4 fill-current" />
                      )
                    }
                    disabled={
                      isExtracting ||
                      !selectedDevice ||
                      !deviceApp?.installed ||
                      isSelectedBrowserUnsupported ||
                      !isReady ||
                      !isComponentsVerified
                    }
                    onClick={handleExtractFromDevice}
                    className="rounded-xl"
                  >
                    {isExtracting
                      ? t('modals.androidPatch.extractAndInspectState')
                      : t('modals.androidPatch.extractFromPhoneBtn')}
                  </Button>
                ) : isPatchDoneStage ? (
                  <div className="flex items-center gap-2 flex-wrap">
                    <Button
                      type="button"
                      variant="desktop"
                      size="md"
                      onClick={() => setPatchStatus(null)}
                      title={t('modals.androidPatch.repatchTooltip')}
                      className="rounded-xl py-2"
                    >
                      {t('modals.androidPatch.repatchBtn')}
                    </Button>
                    <Button
                      type="button"
                      variant="desktop"
                      size="md"
                      onClick={handleOpenOutput}
                      title={t('modals.androidPatch.openOutputDirTooltip')}
                      className="rounded-xl py-2"
                    >
                      {t('modals.androidPatch.openOutputDirBtn')}
                    </Button>
                    <Button
                      type="button"
                      variant="success"
                      size="lg"
                      icon={
                        isInstalling ? (
                          <RefreshCw className="w-4 h-4 animate-spin" />
                        ) : (
                          <Zap className="w-4 h-4 fill-current" />
                        )
                      }
                      disabled={isInstalling || onlineAdbCount === 0}
                      onClick={() => handleInstallToDevice(false)}
                      title={
                        onlineAdbCount === 0
                          ? t('modals.androidPatch.installToPhoneTooltipNoDev')
                          : t('modals.androidPatch.installToPhoneTooltip')
                      }
                      className="rounded-xl"
                    >
                      {isInstalling
                        ? t('modals.androidPatch.installingToPhoneState')
                        : t('modals.androidPatch.installToPhoneBtn')}
                    </Button>
                  </div>
                ) : (
                  <div className="flex items-center gap-2">
                    {isRunning && (
                      <Button
                        type="button"
                        variant="danger"
                        size="md"
                        onClick={handleCancelPatch}
                        className="rounded-xl py-2"
                      >
                        {t('modals.androidPatch.cancelPatchBtn')}
                      </Button>
                    )}
                    <Button
                      type="button"
                      variant="primary"
                      size="lg"
                      icon={
                        isRunning ? (
                          <RefreshCw className="w-4 h-4 animate-spin" />
                        ) : inspectedPkg?.is_already_patched ? (
                          <RefreshCw className="w-4 h-4" />
                        ) : (
                          <Play className="w-4 h-4 fill-current" />
                        )
                      }
                      disabled={
                        !isReady ||
                        !isComponentsVerified ||
                        !inspectedPkg ||
                        inspectedPkg.is_system_webview === false ||
                        isRunning ||
                        isPatchStarting
                      }
                      onClick={handleStartPatch}
                      className="rounded-xl"
                    >
                      {isRunning
                        ? t('modals.androidPatch.processingPercent', { percent: progressPercent })
                        : !inspectedPkg
                        ? t('modals.androidPatch.pleaseSelectPackage')
                        : inspectedPkg.is_already_patched
                        ? t('modals.androidPatch.repatchOrUpgrade')
                        : t('modals.androidPatch.startPatchBtn')}
                    </Button>
                  </div>
                )}
              </div>
            </div>
          </Card>

          {/* 4. Patch Progress & Terminal Card */}
          {(isRunning || patchStatus?.done) && (
            <Card>
              <div className="flex items-center justify-between pb-2.5 hairline-b">
                <div className="flex items-center gap-2">
                  <Terminal className="w-[18px] h-[18px] text-label-secondary" />
                  <h2 className="text-sm sm:text-[15px] font-bold text-label-primary tracking-tight">
                    {t('modals.androidPatch.progressAndLogs')}
                  </h2>
                </div>
                <div className="flex items-center gap-2">
                  {patchStatus?.done && !patchStatus.error && (
                    <Button
                      type="button"
                      variant="desktop"
                      size="xs"
                      icon={<FolderOpen className="w-3.5 h-3.5 text-emerald-500" />}
                      onClick={handleOpenOutput}
                    >
                      {t('modals.androidPatch.openOutputFolderBtn')}
                    </Button>
                  )}
                  <button
                    type="button"
                    onClick={() => setShowLogs(!showLogs)}
                    className="w-7 h-7 rounded-lg text-label-tertiary hover:text-label-primary hover:bg-slate-500/10 flex items-center justify-center transition-colors cursor-pointer"
                  >
                    {showLogs ? <ChevronUp className="w-4 h-4" /> : <ChevronDown className="w-4 h-4" />}
                  </button>
                </div>
              </div>

              {/* Progress Bar & 5-Step Pipeline */}
              <div className="space-y-2.5">
                <div className="flex items-center justify-between text-xs">
                  <span className="font-semibold text-label-primary">
                    {patchStatus?.stage_text ||
                      (isRunning ? t('modals.androidPatch.stageProcessing') : t('modals.androidPatch.stageReady'))}
                  </span>
                  <span className="font-mono tnum font-bold text-sys-blue">{progressPercent}%</span>
                </div>
                <div className="w-full bg-slate-500/15 rounded-full h-2 overflow-hidden">
                  <div
                    className={`h-2 rounded-full transition-all duration-300 ${
                      patchStatus?.error
                        ? 'bg-rose-500'
                        : patchStatus?.done
                        ? 'bg-emerald-500'
                        : 'bg-sky-500'
                    }`}
                    style={{ width: `${progressPercent}%` }}
                  />
                </div>

                {/* Stages Pipeline */}
                <div className="grid grid-cols-5 gap-1.5 text-[11px] text-center pt-0.5 font-medium">
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
                        className={`py-1.5 px-1.5 rounded-lg border text-[11px] flex items-center justify-center gap-1 truncate transition-colors ${
                          isPassed
                            ? 'bg-sys-greenBg text-sys-green border-emerald-500/20'
                            : isCurrent
                            ? 'bg-sys-blueBg text-sys-blue border-sky-500/35 font-semibold'
                            : 'inset-panel text-label-tertiary'
                        }`}
                      >
                        {isPassed && <CheckCircle2 className="w-3 h-3 shrink-0" />}
                        {isCurrent && <RefreshCw className="w-3 h-3 animate-spin shrink-0" />}
                        <span className="truncate">{name}</span>
                      </div>
                    )
                  })}
                </div>
              </div>

              {/* Result Output Card if Done */}
              {patchStatus?.done && !patchStatus.error && patchResult && (
                <InsetPanel className="p-4 border-emerald-500/25 bg-sys-greenBg/40 text-xs space-y-2.5">
                  <div className="flex items-center gap-2 font-bold text-sys-green text-sm">
                    <CheckCircle2 className="w-4 h-4 shrink-0" />
                    <span>{t('modals.androidPatch.successAllBuiltIn')}</span>
                  </div>
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-2 text-label-secondary pt-1">
                    <div>
                      <span className="font-semibold text-label-primary">
                        {t('modals.androidPatch.outputTypeLabel')}
                      </span>
                      <span>
                        {isResultSplit
                          ? t('modals.androidPatch.splitApkSuite', {
                              count:
                                resultTotalApks > 0
                                  ? t('modals.androidPatch.splitApkSubCount', { count: resultTotalApks })
                                  : '',
                            })
                          : t('modals.androidPatch.singleApkSuite')}
                      </span>
                    </div>
                    <div>
                      <span className="font-semibold text-label-primary">
                        {t('modals.androidPatch.totalSizeLabel')}
                      </span>
                      <span className="font-mono tnum">{resultTotalMb} MB</span>
                    </div>
                    <div className="sm:col-span-2 truncate">
                      <span className="font-semibold text-label-primary">
                        {t('modals.androidPatch.outputFilesLabel')}
                      </span>
                      <span className="font-mono text-label-primary" title={resultGeneratedFile}>
                        {resultGeneratedFile}
                      </span>
                    </div>
                    {patchResult?.backup_path && (
                      <div className="sm:col-span-2 truncate text-label-tertiary">
                        <span className="font-semibold text-label-secondary">
                          {t('modals.androidPatch.backupOriginLabel')}
                        </span>
                        <span className="font-mono" title={patchResult.backup_path}>
                          {patchResult.backup_path}
                        </span>
                      </div>
                    )}
                  </div>

                  {/* Installation guidance */}
                  <div className="pt-2.5 hairline-t text-label-secondary space-y-1.5">
                    <div className="font-bold text-label-primary">{t('modals.androidPatch.phoneInstallGuide')}</div>
                    <ul className="list-disc pl-4 space-y-0.5 pt-0.5">
                      <li>
                        <span className="font-semibold">{t('modals.androidPatch.oneClickGuideTitle')}</span>
                        {t('modals.androidPatch.oneClickGuideDesc')}
                      </li>
                      <li>
                        <span className="font-semibold">{t('modals.androidPatch.manualGuideTitle')}</span>
                        {isResultSplit
                          ? t('modals.androidPatch.manualGuideSplitDesc')
                          : t('modals.androidPatch.manualGuideSingleDesc')}
                      </li>
                      <li>
                        <span className="font-semibold">{t('modals.androidPatch.startUseTitle')}</span>
                        {t('modals.androidPatch.startUseDesc')}
                      </li>
                    </ul>
                  </div>

                  {/* One-Click Install to Phone */}
                  {onlineAdbCount > 0 && (
                    <div className="pt-3 hairline-t space-y-2">
                      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-2">
                        <div className="space-y-0.5">
                          <div className="font-bold text-label-primary text-xs flex items-center gap-1.5">
                            <Smartphone className="w-3.5 h-3.5 text-emerald-500" />
                            <span>
                              {t('modals.androidPatch.deviceDetectedNotice', {
                                model: adbDevices.find((d) => d.serial === selectedDevice)?.model || selectedDevice,
                              })}
                            </span>
                          </div>
                          <div className="text-[11px] text-label-tertiary">
                            {t('modals.androidPatch.noNeedManualInstall')}
                          </div>
                        </div>
                        {isInstalling ? (
                          <Badge variant="green" className="px-3 py-1.5">
                            <RefreshCw className="w-3.5 h-3.5 animate-spin mr-1 inline" />
                            {t('modals.androidPatch.pushingToPhoneState')}
                          </Badge>
                        ) : (
                          <Badge variant="green" className="px-3 py-1.5">
                            <CheckCircle2 className="w-3.5 h-3.5 mr-1 inline" />
                            {t('modals.androidPatch.connectedReady')}
                          </Badge>
                        )}
                      </div>

                      {showUninstallModal && (
                        <InsetPanel tone="amber" className="p-3 text-xs space-y-2">
                          <div className="flex items-start gap-2">
                            <AlertTriangle className="w-4 h-4 text-amber-600 shrink-0 mt-0.5" />
                            <div className="space-y-1">
                              <div className="font-bold">{t('modals.androidPatch.sigConflictTitle')}</div>
                              <div className="text-[11px] leading-relaxed opacity-90">
                                {t('modals.androidPatch.sigConflictDesc')}
                              </div>
                              <div className="text-[11px] font-bold text-sys-red bg-sys-redBg border border-rose-500/20 p-2 rounded-lg">
                                {t('modals.androidPatch.sigConflictWarning')}
                              </div>
                            </div>
                          </div>
                          <div className="flex items-center justify-end gap-2 pt-1">
                            <Button
                              type="button"
                              variant="desktop"
                              size="sm"
                              onClick={() => setShowUninstallModal(false)}
                            >
                              {t('modals.androidPatch.cancelBtn')}
                            </Button>
                            <Button
                              type="button"
                              variant="danger"
                              size="sm"
                              icon={<Trash2 className="w-3.5 h-3.5" />}
                              onClick={() => handleInstallToDevice(true)}
                              disabled={isInstalling}
                            >
                              {t('modals.androidPatch.confirmReinstallBtn')}
                            </Button>
                          </div>
                        </InsetPanel>
                      )}

                      {installResult?.ok && (
                        <InsetPanel className="p-2.5 bg-sys-greenBg/70 border-emerald-500/30 text-xs text-sys-green flex items-center gap-2">
                          <CheckCircle2 className="w-4 h-4 shrink-0" />
                          <span className="font-medium">{t('modals.androidPatch.deploySuccessNotice')}</span>
                        </InsetPanel>
                      )}
                    </div>
                  )}
                </InsetPanel>
              )}

              {/* Error Banner */}
              {patchStatus?.error && (
                <InsetPanel tone="rose" className="p-3 text-xs flex items-start gap-2">
                  <AlertCircle className="w-4 h-4 text-rose-600 shrink-0 mt-0.5" />
                  <div>
                    <span className="font-bold">{t('modals.androidPatch.processFailed')}</span>
                    <span>{patchStatus.error}</span>
                  </div>
                </InsetPanel>
              )}

              {/* Terminal Logs */}
              {showLogs && (
                <div
                  ref={logTerminalRef}
                  className="bg-slate-950/95 text-slate-200 p-3.5 rounded-xl font-mono text-xs max-h-60 overflow-y-auto space-y-1 border border-white/10 select-text"
                >
                  {(patchStatus?.logs || []).map((line, idx) => (
                    <div key={idx} className="leading-relaxed whitespace-pre-wrap break-all">
                      {line}
                    </div>
                  ))}
                </div>
              )}
            </Card>
          )}
        </>
      )}

      {/* 5. Collapsible User Notices & Security Disclaimers */}
      <InsetPanel className="rounded-[16px] p-0 overflow-hidden">
        <button
          type="button"
          onClick={() => setShowCompliance((v) => !v)}
          aria-expanded={showCompliance}
          className="w-full px-4 py-3 flex items-center justify-between gap-2 text-xs font-semibold text-label-secondary hover:text-label-primary hover:bg-slate-500/5 transition-colors cursor-pointer select-none"
        >
          <span className="flex items-center gap-2">
            <ShieldCheck className="w-4 h-4 text-sky-500 shrink-0" />
            <span>{t('modals.androidPatch.complianceTitle')}</span>
          </span>
          {showCompliance ? (
            <ChevronUp className="w-4 h-4 text-label-tertiary shrink-0" />
          ) : (
            <ChevronDown className="w-4 h-4 text-label-tertiary shrink-0" />
          )}
        </button>

        {showCompliance && (
          <div className="px-4 pb-3.5 pt-1 hairline-t text-xs text-label-secondary animate-fade-soft">
            <ul className="list-disc pl-4 space-y-2 leading-relaxed pt-2">
              <li>
                <strong className="text-label-primary">{t('modals.androidPatch.complianceItem1Title')}</strong>{' '}
                {t('modals.androidPatch.complianceItem1Desc')}
              </li>
              <li>
                <strong className="text-label-primary">{t('modals.androidPatch.complianceItem2Title')}</strong>{' '}
                {t('modals.androidPatch.complianceItem2Desc')}
              </li>
              <li className="space-y-1">
                <strong className="text-label-primary block">
                  {t('modals.androidPatch.complianceNetworkTitle')}
                </strong>
                <ul className="list-[circle] pl-4 space-y-1 text-label-secondary">
                  <li>{t('modals.androidPatch.complianceNetworkTunnel')}</li>
                  <li>{t('modals.androidPatch.complianceNetworkAcgp')}</li>
                </ul>
              </li>
              <li>
                <strong className="text-label-primary">
                  {t('modals.androidPatch.complianceMultiBrowserTitle')}
                </strong>{' '}
                {t('modals.androidPatch.complianceMultiBrowserDesc')}
              </li>
            </ul>
          </div>
        )}
      </InsetPanel>
    </div>
  )
}