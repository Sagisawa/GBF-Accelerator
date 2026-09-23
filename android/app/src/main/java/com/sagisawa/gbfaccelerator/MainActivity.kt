package com.sagisawa.gbfaccelerator

import android.annotation.SuppressLint
import android.content.pm.ActivityInfo
import android.graphics.Bitmap
import android.os.Bundle
import android.view.View
import android.view.WindowManager
import android.webkit.CookieManager
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Toast
import androidx.activity.OnBackPressedCallback
import androidx.appcompat.app.AppCompatActivity
import com.sagisawa.gbfaccelerator.databinding.ActivityMainBinding

class MainActivity : AppCompatActivity() {

    private lateinit var binding: ActivityMainBinding
    private var isScreenKeepOn = true
    private var backPressedTime = 0L

    companion object {
        const val GBF_GAME_URL = "https://game.granbluefantasy.jp/"
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        binding = ActivityMainBinding.inflate(layoutInflater)
        setContentView(binding.root)

        // 1. 初始化屏幕常亮（游戏默认开启常亮）
        applyScreenKeepOn(true)

        // 2. 初始化 WebView 与游戏环境
        setupWebView()

        // 3. 初始化控制栏与按键交互
        setupToolbar()

        // 4. 处理系统物理/手势返回事件
        setupBackNavigation()

        // 5. 加载碧蓝幻想官方入口
        if (savedInstanceState == null) {
            binding.webView.loadUrl(GBF_GAME_URL)
        } else {
            binding.webView.restoreState(savedInstanceState)
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun setupWebView() {
        val webView = binding.webView
        val settings = webView.settings

        // 核心脚本支持
        settings.javaScriptEnabled = true
        settings.javaScriptCanOpenWindowsAutomatically = true

        // 本地存储与持久化（IndexedDB, LocalStorage, Database）
        settings.domStorageEnabled = true
        @Suppress("DEPRECATION")
        settings.databaseEnabled = true

        // 视口与缩放适配
        settings.useWideViewPort = true
        settings.loadWithOverviewMode = true
        settings.builtInZoomControls = false
        settings.displayZoomControls = false

        // 缓存策略：默认根据服务器 Cache-Control
        settings.cacheMode = WebSettings.LOAD_DEFAULT

        // 游戏音频无阻碍自动播放
        settings.mediaPlaybackRequiresUserGesture = false

        // 混合内容策略（允许 HTTPS 页面加载必要的资源）
        settings.mixedContentMode = WebSettings.MIXED_CONTENT_COMPATIBILITY_MODE

        // Cookie 持久化支持
        val cookieManager = CookieManager.getInstance()
        cookieManager.setAcceptCookie(true)
        cookieManager.setAcceptThirdPartyCookies(webView, true)

        // 设置 WebViewClient：保证页面跳转均在应用内完成
        webView.webViewClient = object : WebViewClient() {
            override fun shouldOverrideUrlLoading(view: WebView?, request: WebResourceRequest?): Boolean {
                val url = request?.url?.toString() ?: return false
                // HTTP/HTTPS 链接均在内部 WebView 加载
                return if (url.startsWith("http://") || url.startsWith("https://")) {
                    false
                } else {
                    // 非网页协议不阻断外部意图
                    true
                }
            }

            override fun onPageStarted(view: WebView?, url: String?, favicon: Bitmap?) {
                super.onPageStarted(view, url, favicon)
                binding.progressBar.visibility = View.VISIBLE
            }

            override fun onPageFinished(view: WebView?, url: String?) {
                super.onPageFinished(view, url)
                binding.progressBar.visibility = View.GONE
                // 持久化写入 Cookie
                CookieManager.getInstance().flush()
                updateNavButtonsState()
            }
        }

        // 设置 WebChromeClient：处理加载进度与页面标题
        webView.webChromeClient = object : WebChromeClient() {
            override fun onProgressChanged(view: WebView?, newProgress: Int) {
                if (newProgress < 100) {
                    binding.progressBar.visibility = View.VISIBLE
                    binding.progressBar.progress = newProgress
                } else {
                    binding.progressBar.visibility = View.GONE
                }
            }
        }
    }

    private fun setupToolbar() {
        // 后退
        binding.btnBack.setOnClickListener {
            if (binding.webView.canGoBack()) {
                binding.webView.goBack()
            }
        }

        // 前进
        binding.btnForward.setOnClickListener {
            if (binding.webView.canGoForward()) {
                binding.webView.goForward()
            }
        }

        // 刷新
        binding.btnRefresh.setOnClickListener {
            binding.webView.reload()
            Toast.makeText(this, R.string.nav_refresh, Toast.LENGTH_SHORT).show()
        }

        // 主页（重返官方 GBF 入口）
        binding.btnHome.setOnClickListener {
            binding.webView.loadUrl(GBF_GAME_URL)
        }

        // 屏幕常亮切换
        binding.btnScreenOn.setOnClickListener {
            applyScreenKeepOn(!isScreenKeepOn)
            val msg = if (isScreenKeepOn) getString(R.string.nav_screen_on) else getString(R.string.nav_screen_off)
            Toast.makeText(this, msg, Toast.LENGTH_SHORT).show()
        }

        // 横竖屏切换（在横屏、竖屏、重力传感器自适应之间切换）
        binding.btnRotate.setOnClickListener {
            toggleOrientation()
        }

        // 收起工具栏（防遮挡游戏操作）
        binding.btnCollapse.setOnClickListener {
            binding.toolbarContainer.visibility = View.GONE
            binding.btnExpandMenu.visibility = View.VISIBLE
        }

        // 展开工具栏
        binding.btnExpandMenu.setOnClickListener {
            binding.toolbarContainer.visibility = View.VISIBLE
            binding.btnExpandMenu.visibility = View.GONE
        }
    }

    private fun updateNavButtonsState() {
        binding.btnBack.alpha = if (binding.webView.canGoBack()) 1.0f else 0.4f
        binding.btnForward.alpha = if (binding.webView.canGoForward()) 1.0f else 0.4f
    }

    private fun setupBackNavigation() {
        onBackPressedDispatcher.addCallback(this, object : OnBackPressedCallback(true) {
            override fun handleOnBackPressed() {
                if (binding.webView.canGoBack()) {
                    binding.webView.goBack()
                } else {
                    // 双击返回键退出应用，防止游戏中误触
                    val currentTime = System.currentTimeMillis()
                    if (currentTime - backPressedTime < 2000) {
                        finish()
                    } else {
                        backPressedTime = currentTime
                        Toast.makeText(this@MainActivity, R.string.exit_confirm, Toast.LENGTH_SHORT).show()
                    }
                }
            }
        })
    }

    private fun applyScreenKeepOn(keepOn: Boolean) {
        isScreenKeepOn = keepOn
        if (keepOn) {
            window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            binding.btnScreenOn.setImageResource(R.drawable.ic_screen_on)
        } else {
            window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            binding.btnScreenOn.setImageResource(R.drawable.ic_screen_off)
        }
    }

    private fun toggleOrientation() {
        requestedOrientation = when (requestedOrientation) {
            ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE,
            ActivityInfo.SCREEN_ORIENTATION_SENSOR_LANDSCAPE -> {
                Toast.makeText(this, "已切换为竖屏模式", Toast.LENGTH_SHORT).show()
                ActivityInfo.SCREEN_ORIENTATION_PORTRAIT
            }
            ActivityInfo.SCREEN_ORIENTATION_PORTRAIT,
            ActivityInfo.SCREEN_ORIENTATION_SENSOR_PORTRAIT -> {
                Toast.makeText(this, "已切换为横屏模式", Toast.LENGTH_SHORT).show()
                ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE
            }
            else -> {
                Toast.makeText(this, "已锁定为横屏模式", Toast.LENGTH_SHORT).show()
                ActivityInfo.SCREEN_ORIENTATION_LANDSCAPE
            }
        }
    }

    override fun onSaveInstanceState(outState: Bundle) {
        super.onSaveInstanceState(outState)
        binding.webView.saveState(outState)
    }

    override fun onResume() {
        super.onResume()
        binding.webView.onResume()
    }

    override fun onPause() {
        super.onPause()
        binding.webView.onPause()
        CookieManager.getInstance().flush()
    }

    override fun onDestroy() {
        binding.webView.destroy()
        super.onDestroy()
    }
}
