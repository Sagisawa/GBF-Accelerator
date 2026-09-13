import os
import sys
import time
import threading
import subprocess
import urllib.parse
import webbrowser
import tkinter as tk
from tkinter import ttk, filedialog, messagebox
from pathlib import Path

# Enable DPI awareness on Windows before creating Tk windows
if sys.platform == "win32":
    try:
        import ctypes
        ctypes.windll.shcore.SetProcessDpiAwareness(1)
    except Exception:
        try:
            import ctypes
            ctypes.windll.user32.SetProcessDPIAware()
        except Exception:
            pass

# Ensure PIL and pystray
from PIL import Image, ImageDraw, ImageTk
import pystray

from config_manager import (
    get_base_dir,
    config_manager,
    is_ca_installed,
    install_ca_certificate,
    uninstall_ca_certificate,
    find_installed_gbf_ca_thumbprints,
    check_legacy_leaked_ca_installed,
    clean_legacy_leaked_ca,
    auto_detect_acgpower_cache,
    detect_upstream_proxies,
    is_port_open,
    check_upstream_connectivity,
)
from cert_manager import ensure_ca, CA_CERT_PATH, get_ca_fingerprint_sha256
from cache_manager import cache_manager
import gbf_proxy
import system_proxy
from startup_manager import is_startup_enabled, set_startup_enabled
import update_manager
from update_manager import APP_VERSION, UpdateInfo

START_MINIMIZED = "--minimized" in sys.argv

def create_tray_icon_image(is_running: bool = True) -> Image.Image:
    """Generate a clean lightning bolt icon for system tray."""
    img = Image.new("RGBA", (64, 64), (0, 0, 0, 0))
    draw = ImageDraw.Draw(img)
    # Background circle
    bg_color = (40, 167, 69) if is_running else (108, 117, 125)
    draw.ellipse([4, 4, 60, 60], fill=bg_color)
    # Lightning bolt polygon
    bolt_coords = [
        (34, 10),
        (18, 34),
        (31, 34),
        (26, 54),
        (48, 28),
        (35, 28),
    ]
    draw.polygon(bolt_coords, fill=(255, 255, 255))
    return img

class GBFAcceleratorGUI:
    def __init__(self, root: tk.Tk):
        self.root = root
        self.window_icon = ImageTk.PhotoImage(create_tray_icon_image(True))
        self.root.iconphoto(True, self.window_icon)
        self.root.title("GBF 加速器")

        # Responsive window sizing based on screen height
        screen_h = self.root.winfo_screenheight()
        win_w = 680
        win_h = 830 if screen_h >= 900 else max(720, screen_h - 70)
        self.root.geometry(f"{win_w}x{win_h}")
        self.root.minsize(620, min(740, win_h))

        # Styles
        self.setup_styles()

        # Data variables
        self.var_status_text = tk.StringVar(value="● 正在启动中...")
        self.var_hits = tk.StringVar(value="0")
        self.var_downloads = tk.StringVar(value="0")
        self.var_apis = tk.StringVar(value="0")
        self.var_cache_dir = tk.StringVar(value=str(config_manager.get_effective_cache_dir(interactive=False)))
        self.var_upstream = tk.StringVar(value=config_manager.get_effective_upstream_proxy())
        self.var_direct_mode = tk.BooleanVar(value=config_manager.config.get("direct_mode", False))
        self.var_shimakaze_mode = tk.BooleanVar(value=config_manager.config.get("shimakaze_mode", False))
        self.var_listen_port = tk.StringVar(value=str(config_manager.get_listen_port()))
        self.var_ca_status = tk.StringVar(value="检测中...")
        self.var_ca_fp = tk.StringVar(value="")
        self.var_auto_pac = tk.BooleanVar(value=config_manager.config.get("auto_system_proxy", True))
        self.var_auto_start = tk.BooleanVar(
            value=bool(config_manager.config.get("auto_start", False)) or is_startup_enabled()
        )

        # Performance & Resource Controls
        self.var_ram_cache = tk.BooleanVar(value=config_manager.config.get("enable_ram_cache", True))
        self.var_browser_cache = tk.BooleanVar(value=config_manager.config.get("enable_browser_cache", False))
        self.var_auto_repair = tk.BooleanVar(value=config_manager.config.get("enable_auto_repair", True))
        self.var_auto_update = tk.BooleanVar(value=config_manager.config.get("auto_check_update", True))
        self.update_info: Optional[UpdateInfo] = None

        # Build UI
        self.build_ui()
        self.update_shimakaze_controls()

        # Center window after layout is constructed
        self.center_window()

        # System tray setup
        self.tray_icon = None
        self.setup_tray()

        # Window events
        self.root.protocol("WM_DELETE_WINDOW", self.hide_to_tray)

        # Check CA status and detect legacy leaked cert
        self.update_ca_status()
        self.root.after(500, self.check_and_prompt_ca_state)

        # Ensure helper files
        from app_main import ensure_bundled_files
        ensure_bundled_files()

        # Start proxy thread automatically
        self.start_proxy()

        # Periodic timer for stats update
        self.update_stats_loop()

        # Check updates automatically if enabled
        if self.var_auto_update.get():
            self.root.after(2000, self.start_auto_update_check)

    def center_window(self):
        self.root.update_idletasks()
        w = self.root.winfo_width()
        h = self.root.winfo_height()
        ws = self.root.winfo_screenwidth()
        hs = self.root.winfo_screenheight()
        x = max(0, (ws // 2) - (w // 2))
        y = max(0, (hs // 2) - (h // 2) - 30)
        self.root.geometry(f"+{x}+{y}")

    def setup_styles(self):
        style = ttk.Style(self.root)
        available = style.theme_names()
        if "vista" in available:
            style.theme_use("vista")
        elif "winnative" in available:
            style.theme_use("winnative")
        else:
            style.theme_use("clam")

        # Backgrounds
        self.root.configure(bg="#f4f6f9")
        style.configure("TFrame", background="#f4f6f9")
        style.configure("Card.TFrame", background="#ffffff", relief="flat")
        style.configure("CardInner.TFrame", background="#ffffff")

        # Checkbutton
        style.configure("TCheckbutton", background="#ffffff", font=("Microsoft YaHei UI", 9))

        # Labels
        style.configure("Title.TLabel", font=("Microsoft YaHei UI", 12, "bold"), background="#ffffff", foreground="#212529")
        style.configure("Subtitle.TLabel", font=("Microsoft YaHei UI", 9), background="#ffffff", foreground="#6c757d")
        style.configure("StatNum.TLabel", font=("Microsoft YaHei UI", 15, "bold"), background="#ffffff")
        style.configure("StatLabel.TLabel", font=("Microsoft YaHei UI", 9), background="#ffffff", foreground="#6c757d")
        style.configure("Normal.TLabel", font=("Microsoft YaHei UI", 9), background="#ffffff", foreground="#333333")
        style.configure("Gray.TLabel", font=("Microsoft YaHei UI", 8), background="#ffffff", foreground="#888888")

        # Buttons
        style.configure("Primary.TButton", font=("Microsoft YaHei UI", 9, "bold"))
        style.configure("Success.TButton", font=("Microsoft YaHei UI", 9, "bold"))
        style.configure("Danger.TButton", font=("Microsoft YaHei UI", 9, "bold"))

    def build_ui(self):
        main_container = ttk.Frame(self.root, padding="14 10 14 10")
        main_container.pack(fill="both", expand=True)

        # ---------------- 1. Status & Header Card ----------------
        card_header = ttk.Frame(main_container, style="Card.TFrame", padding="14 10 14 10")
        card_header.pack(fill="x", pady=(0, 8))

        h_left = ttk.Frame(card_header, style="CardInner.TFrame")
        h_left.pack(side="left", fill="both", expand=True)

        h_title_box = ttk.Frame(h_left, style="CardInner.TFrame")
        h_title_box.pack(anchor="w")
        ttk.Label(h_title_box, text="碧蓝幻想 GBF 加速器", style="Title.TLabel").pack(side="left")
        self.lbl_ver = ttk.Label(h_title_box, text=f"v{APP_VERSION}", style="Gray.TLabel")
        self.lbl_ver.pack(side="left", padx=(6, 0), pady=(3, 0))

        # Dynamic update badge button (hidden until update is detected)
        self.btn_update_badge = tk.Button(
            h_title_box,
            text="🔥 发现新版",
            bg="#ffc107",
            fg="#212529",
            activebackground="#e0a800",
            activeforeground="#212529",
            font=("Microsoft YaHei UI", 8, "bold"),
            relief="flat",
            padx=6,
            pady=1,
            cursor="hand2",
            command=self.on_click_update_badge,
        )

        self.lbl_status = ttk.Label(h_left, textvariable=self.var_status_text, style="Subtitle.TLabel")
        self.lbl_status.pack(anchor="w", pady=(2, 0))

        self.btn_toggle = tk.Button(
            card_header,
            text="停止加速",
            bg="#dc3545",
            fg="#ffffff",
            activebackground="#bd2130",
            activeforeground="#ffffff",
            font=("Microsoft YaHei UI", 10, "bold"),
            relief="flat",
            padx=16,
            pady=5,
            cursor="hand2",
            command=self.toggle_proxy,
        )
        self.btn_toggle.pack(side="right")

        # ---------------- 2. Real-time Stats Card ----------------
        card_stats = ttk.Frame(main_container, style="Card.TFrame", padding="12 8 12 8")
        card_stats.pack(fill="x", pady=(0, 8))

        grid_frame = ttk.Frame(card_stats, style="CardInner.TFrame")
        grid_frame.pack(fill="x")
        grid_frame.columnconfigure(0, weight=1)
        grid_frame.columnconfigure(1, weight=1)
        grid_frame.columnconfigure(2, weight=1)

        # Stat 1: Cache Hits
        c1 = ttk.Frame(grid_frame, style="CardInner.TFrame")
        c1.grid(row=0, column=0, sticky="ew")
        lbl_hits_num = ttk.Label(c1, textvariable=self.var_hits, style="StatNum.TLabel", foreground="#28a745")
        lbl_hits_num.pack(anchor="center")
        ttk.Label(c1, text="⚡ 本地缓存命中", style="StatLabel.TLabel").pack(anchor="center")

        # Stat 2: Downloads
        c2 = ttk.Frame(grid_frame, style="CardInner.TFrame")
        c2.grid(row=0, column=1, sticky="ew")
        lbl_dl_num = ttk.Label(c2, textvariable=self.var_downloads, style="StatNum.TLabel", foreground="#007bff")
        lbl_dl_num.pack(anchor="center")
        ttk.Label(c2, text="📥 远程下载缓存", style="StatLabel.TLabel").pack(anchor="center")

        # Stat 3: APIs
        c3 = ttk.Frame(grid_frame, style="CardInner.TFrame")
        c3.grid(row=0, column=2, sticky="ew")
        lbl_api_num = ttk.Label(c3, textvariable=self.var_apis, style="StatNum.TLabel", foreground="#6c757d")
        lbl_api_num.pack(anchor="center")
        ttk.Label(c3, text="🔄 游戏 API 转发", style="StatLabel.TLabel").pack(anchor="center")

        # ---------------- 3. Bottom Action Bar (Pack to bottom FIRST so it is NEVER cut off) ----------------
        f_bottom = ttk.Frame(main_container)
        f_bottom.pack(side="bottom", fill="x", pady=(8, 0))

        btn_open_folder = ttk.Button(f_bottom, text="📂 缓存目录", command=self.open_cache_folder)
        btn_open_folder.pack(side="left", padx=(0, 4))

        btn_clear_cache = ttk.Button(f_bottom, text="🗑️ 清空缓存", command=self.clear_cache_dialog)
        btn_clear_cache.pack(side="left", padx=(0, 4))

        btn_proxy_guide = ttk.Button(f_bottom, text="🌐 分流说明", command=self.show_guide)
        btn_proxy_guide.pack(side="left", padx=(0, 4))

        btn_github = ttk.Button(f_bottom, text="⭐ GitHub", command=self.open_github)
        btn_github.pack(side="left", padx=(0, 4))

        self.btn_check_update = ttk.Button(f_bottom, text="🔄 检查更新", command=self.manual_check_update)
        self.btn_check_update.pack(side="left", padx=(0, 4))

        btn_tray = ttk.Button(f_bottom, text="⬇ 最小化到托盘", command=self.hide_to_tray)
        btn_tray.pack(side="right")

        # ---------------- 4. Settings Card ----------------
        card_settings = ttk.Frame(main_container, style="Card.TFrame", padding="14 10 14 10")
        card_settings.pack(fill="both", expand=True)

        ttk.Label(card_settings, text="配置选项", style="Title.TLabel").pack(anchor="w", pady=(0, 6))

        # Field 1: Local Cache Dir
        ttk.Label(card_settings, text="本地缓存目录（支持无缝复用 ACGPower 缓存）：", style="Normal.TLabel").pack(anchor="w")
        f_dir = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_dir.pack(fill="x", pady=(2, 6))

        self.entry_dir = ttk.Entry(f_dir, textvariable=self.var_cache_dir, font=("Consolas", 9))
        self.entry_dir.pack(side="left", fill="x", expand=True, padx=(0, 6))

        btn_browse = ttk.Button(f_dir, text="浏览...", width=8, command=self.browse_cache_dir)
        btn_browse.pack(side="left", padx=(0, 4))

        btn_acgp = ttk.Button(f_dir, text="检测 ACGP", width=11, command=self.detect_acgp)
        btn_acgp.pack(side="left")

        # Field 2: Upstream Proxy
        ttk.Label(card_settings, text="上游网络代理（Clash Verge / Clash / V2ray / 岛风GO 等）：", style="Normal.TLabel").pack(anchor="w")
        f_up = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_up.pack(fill="x", pady=(2, 6))

        self.entry_up = ttk.Entry(f_up, textvariable=self.var_upstream, font=("Consolas", 9))
        self.entry_up.pack(side="left", fill="x", expand=True, padx=(0, 6))

        self.btn_confirm_upstream = ttk.Button(f_up, text="确认", width=8, command=self.confirm_upstream)
        self.btn_confirm_upstream.pack(side="left", padx=(0, 4))

        self.btn_probe = ttk.Button(f_up, text="自动探测", width=10, command=self.probe_upstream)
        self.btn_probe.pack(side="left")

        self.chk_direct = ttk.Checkbutton(
            card_settings,
            text="直连模式（使用本机网络，不经过上游代理；仍使用本地缓存）",
            variable=self.var_direct_mode,
            command=self.toggle_direct_mode,
        )
        self.chk_direct.pack(anchor="w", pady=(0, 2))

        self.chk_shimakaze = ttk.Checkbutton(
            card_settings,
            text="岛风GO 兼容优化模式（放宽超时、自愈重试、适配自签证书；默认关闭）",
            variable=self.var_shimakaze_mode,
            command=self.toggle_shimakaze_mode,
        )
        self.chk_shimakaze.pack(anchor="w", pady=(0, 2))

        self.lbl_shimakaze_hint = ttk.Label(
            card_settings,
            text="⚠️ 提示：使用岛风GO时，请务必在岛风GO主界面关闭【使用远端缓存】功能，避免双重反代冲突！",
            style="Gray.TLabel",
            foreground="#d9534f",
            wraplength=560,
        )
        self.update_upstream_controls()

        # Field 3: Local Listen Port
        ttk.Label(card_settings, text="本地监听端口（默认 8124，支持自定义）：", style="Normal.TLabel").pack(anchor="w")
        f_port = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_port.pack(fill="x", pady=(2, 6))

        self.entry_port = ttk.Entry(f_port, textvariable=self.var_listen_port, font=("Consolas", 9), width=10)
        self.entry_port.pack(side="left", padx=(0, 6))

        btn_reset_port = ttk.Button(f_port, text="恢复默认 (8124)", width=14, command=self.reset_port_default)
        btn_reset_port.pack(side="left", padx=(0, 6))

        btn_save = ttk.Button(f_port, text="保存配置", width=10, command=self.save_settings)
        btn_save.pack(side="left")

        # Field 4: CA Certificate
        ttk.Label(card_settings, text="HTTPS 根证书状态（游戏静态资源本地解析必需）：", style="Normal.TLabel").pack(anchor="w")
        f_ca = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_ca.pack(fill="x", pady=(2, 2))

        self.lbl_ca = ttk.Label(f_ca, textvariable=self.var_ca_status, font=("Microsoft YaHei UI", 9, "bold"))
        self.lbl_ca.pack(side="left", padx=(0, 10))

        btn_install_ca = ttk.Button(f_ca, text="一键安装/修复根证书", command=self.install_ca)
        btn_install_ca.pack(side="left", padx=(0, 6))

        btn_uninstall_ca = ttk.Button(f_ca, text="一键注销/卸载根证书", command=self.uninstall_ca)
        btn_uninstall_ca.pack(side="left")

        # CA Fingerprint info
        f_ca_fp = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_ca_fp.pack(fill="x", pady=(1, 4))
        ttk.Label(f_ca_fp, text="SHA-256 指纹：", style="Gray.TLabel").pack(side="left")
        self.lbl_ca_fp = ttk.Label(f_ca_fp, textvariable=self.var_ca_fp, style="Gray.TLabel", font=("Consolas", 8))
        self.lbl_ca_fp.pack(side="left")

        # Field 5: Windows System PAC Automation
        f_sys_proxy = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_sys_proxy.pack(fill="x", pady=(4, 2))
        chk_pac = ttk.Checkbutton(
            f_sys_proxy,
            text="自动配置 Windows 系统 PAC 代理（开启后浏览器无需插件，仅分流 GBF 流量）",
            variable=self.var_auto_pac,
            command=self.toggle_sys_proxy_setting,
        )
        chk_pac.pack(anchor="w")

        chk_startup = ttk.Checkbutton(
            f_sys_proxy,
            text="开机自启（启动后自动缩小到系统托盘，默认关闭）",
            variable=self.var_auto_start,
            command=self.toggle_startup_setting,
        )
        chk_startup.pack(anchor="w", pady=(2, 0))

        chk_auto_update = ttk.Checkbutton(
            f_sys_proxy,
            text="启动时自动检测新版本（发现新版时右上角提醒，默认开启）",
            variable=self.var_auto_update,
            command=self.toggle_auto_update_setting,
        )
        chk_auto_update.pack(anchor="w", pady=(2, 0))

        # Field 6: Performance & System Resource Options
        ttk.Separator(card_settings, orient="horizontal").pack(fill="x", pady=(6, 6))
        ttk.Label(
            card_settings,
            text="性能与系统资源选项（默认开启；若需降低内存/显存占用可取消对应勾选）：",
            style="Normal.TLabel",
        ).pack(anchor="w", pady=(0, 3))

        f_perf = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_perf.pack(fill="x", pady=(1, 2))

        chk_ram = ttk.Checkbutton(
            f_perf,
            text="启用内存热点缓存 (RAM Cache) - 占用约 256MB 内存，高频静态资源 0 磁盘 I/O 极速直出",
            variable=self.var_ram_cache,
            command=self.toggle_perf_settings,
        )
        chk_ram.pack(anchor="w", pady=2)

        chk_browser = ttk.Checkbutton(
            f_perf,
            text="启用浏览器强缓存与渲染留存（仅对版本化静态资源注入 immutable，默认关闭）",
            variable=self.var_browser_cache,
            command=self.toggle_perf_settings,
        )
        chk_browser.pack(anchor="w", pady=2)

        chk_repair = ttk.Checkbutton(
            f_perf,
            text="自动检测并修复损坏/空缓存 - 自动识别并重下 0 字节损坏文件，防止黑屏卡死",
            variable=self.var_auto_repair,
            command=self.toggle_perf_settings,
        )
        chk_repair.pack(anchor="w", pady=2)

    # ================= Functional Methods =================
    def update_ca_status(self):
        fp = get_ca_fingerprint_sha256()
        if fp:
            self.var_ca_fp.set(fp)
        else:
            self.var_ca_fp.set("未生成")

        if is_ca_installed():
            self.var_ca_status.set("已信任 (正常工作)")
            self.lbl_ca.configure(foreground="#28a745")
        else:
            self.var_ca_status.set("未安装信任")
            self.lbl_ca.configure(foreground="#dc3545")

    def open_github(self):
        webbrowser.open("https://github.com/Sagisawa/GBF-Accelerator")

    def toggle_auto_update_setting(self):
        val = self.var_auto_update.get()
        config_manager.config["auto_check_update"] = val
        config_manager.save_config()

    def start_auto_update_check(self):
        threading.Thread(target=self._bg_check_update, args=(False,), daemon=True).start()

    def manual_check_update(self):
        if hasattr(self, "btn_check_update") and self.btn_check_update.winfo_exists():
            self.btn_check_update.configure(text="⏳ 检查中...", state="disabled")
        threading.Thread(target=self._bg_check_update, args=(True,), daemon=True).start()

    def _bg_check_update(self, is_manual: bool):
        up = None if self.var_direct_mode.get() else gbf_proxy.UPSTREAM_PROXY
        info = update_manager.check_for_updates(upstream_proxy=up)
        try:
            self.root.after(0, self._handle_update_result, info, is_manual)
        except Exception:
            pass

    def _handle_update_result(self, info: UpdateInfo, is_manual: bool):
        self.update_info = info
        if hasattr(self, "btn_check_update") and self.btn_check_update.winfo_exists():
            self.btn_check_update.configure(text="🔄 检查更新", state="normal")

        if info.has_update:
            if hasattr(self, "btn_update_badge") and self.btn_update_badge.winfo_exists():
                self.btn_update_badge.configure(text=f"🔥 发现新版 v{info.latest_version}")
                self.btn_update_badge.pack(side="left", padx=(6, 0))
            if is_manual:
                self.show_update_dialog(info)
        else:
            if is_manual:
                if info.error:
                    messagebox.showwarning("检查更新", f"检查更新失败：\n{info.error}\n\n建议检查网络连接或稍后重试。")
                else:
                    messagebox.showinfo("检查更新", f"当前已是最新版本 (v{info.current_version})！")

    def on_click_update_badge(self):
        if self.update_info and self.update_info.has_update:
            self.show_update_dialog(self.update_info)

    def show_update_dialog(self, info: UpdateInfo):
        dialog = tk.Toplevel(self.root)
        dialog.title(f"发现新版本 - v{info.latest_version}")
        dialog.geometry("540x420")
        dialog.minsize(480, 360)
        dialog.transient(self.root)
        dialog.grab_set()

        # Center relative to parent
        self.root.update_idletasks()
        dialog.update_idletasks()
        rx = self.root.winfo_x()
        ry = self.root.winfo_y()
        rw = self.root.winfo_width()
        rh = self.root.winfo_height()
        x = max(0, rx + (rw - 540) // 2)
        y = max(0, ry + (rh - 420) // 2)
        dialog.geometry(f"+{x}+{y}")

        content = ttk.Frame(dialog, padding="16 14 16 14")
        content.pack(fill="both", expand=True)

        # Top banner
        f_top = ttk.Frame(content)
        f_top.pack(fill="x", pady=(0, 10))
        ttk.Label(
            f_top,
            text=f"🎉 发现新版本：v{info.latest_version}",
            font=("Microsoft YaHei UI", 12, "bold"),
            foreground="#28a745",
        ).pack(anchor="w")

        sub_info = f"当前运行版本: v{info.current_version}"
        if info.published_at:
            sub_info += f"  |  发布时间: {info.published_at}"
        ttk.Label(f_top, text=sub_info, style="Gray.TLabel").pack(anchor="w", pady=(2, 0))

        if info.release_title and info.release_title != f"v{info.latest_version}":
            ttk.Label(
                f_top,
                text=info.release_title,
                font=("Microsoft YaHei UI", 9, "bold"),
            ).pack(anchor="w", pady=(4, 0))

        # Release notes text
        ttk.Label(content, text="更新内容：", style="Normal.TLabel").pack(anchor="w", pady=(0, 4))
        f_text = ttk.Frame(content)
        f_text.pack(fill="both", expand=True, pady=(0, 12))

        scrollbar = ttk.Scrollbar(f_text)
        scrollbar.pack(side="right", fill="y")

        txt_notes = tk.Text(
            f_text,
            wrap="word",
            font=("Microsoft YaHei UI", 9),
            yscrollcommand=scrollbar.set,
            bg="#fdfdfd",
            relief="solid",
            bd=1,
            padx=8,
            pady=8,
        )
        txt_notes.pack(side="left", fill="both", expand=True)
        scrollbar.config(command=txt_notes.yview)

        notes_content = info.release_notes.strip() if info.release_notes else "暂无详细更新日志。"
        txt_notes.insert("1.0", notes_content)
        txt_notes.configure(state="disabled")

        # Buttons
        f_btns = ttk.Frame(content)
        f_btns.pack(fill="x")

        def open_download():
            target_url = info.html_url or f"https://github.com/{update_manager.GITHUB_REPO}/releases/latest"
            webbrowser.open(target_url)
            dialog.destroy()

        btn_dl = tk.Button(
            f_btns,
            text="🚀 前往 GitHub Releases 下载更新",
            bg="#28a745",
            fg="#ffffff",
            activebackground="#218838",
            activeforeground="#ffffff",
            font=("Microsoft YaHei UI", 9, "bold"),
            relief="flat",
            padx=14,
            pady=6,
            cursor="hand2",
            command=open_download,
        )
        btn_dl.pack(side="right")

        btn_close = ttk.Button(f_btns, text="稍后再说", command=dialog.destroy)
        btn_close.pack(side="right", padx=(0, 8))

    def install_ca(self):
        ensure_ca()
        if check_legacy_leaked_ca_installed():
            clean_legacy_leaked_ca()
        if is_ca_installed():
            messagebox.showinfo("根证书提示", "本机专属根证书已在系统的【受信任的根证书颁发机构】中，正常工作中！")
            return

        messagebox.showinfo("安装指引", "即将调起 Windows 证书导入向导，若弹出系统安全提示框，请点击【是 (Y)】允许信任。")
        install_ca_certificate(CA_CERT_PATH)
        self.update_ca_status()

    def uninstall_ca(self):
        installed = find_installed_gbf_ca_thumbprints()
        if not installed and not is_ca_installed() and not check_legacy_leaked_ca_installed():
            messagebox.showinfo("根证书提示", "系统中未检测到已安装的 GBF 根证书。")
            return
        if not messagebox.askyesno("注销根证书", "确定要从系统【受信任的根证书颁发机构】中注销/卸载根证书吗？\n\n注销后，加速器将无法解密和缓存 HTTPS 资源，直到重新安装。"):
            return
        ok, msg = uninstall_ca_certificate()
        clean_legacy_leaked_ca()
        if ok:
            messagebox.showinfo("注销成功", f"根证书已成功从系统受信任列表中移除。\n\n{msg}")
        else:
            messagebox.showwarning("注销提示", f"注销结果：\n{msg}")
        self.update_ca_status()

    def check_and_prompt_ca_state(self):
        legacy_found = check_legacy_leaked_ca_installed()
        current_installed = is_ca_installed()

        if legacy_found:
            if messagebox.askyesno(
                "检测到旧版废弃根证书",
                "⚠️ 安全与兼容性提示：\n\n"
                "检测到您的系统根证书库中存在已废弃的旧版公开根证书 (GBF Speed CA)。\n\n"
                "• 旧证书私钥曾公开，继续保留存在安全隐患，且会导致新版无法解密与缓存游戏资源。\n"
                "• 建议立即执行一键清理旧证书，并安装本机生成的专属新根证书。\n\n"
                "是否立即执行一键迁移？\n"
                "（若弹出 Windows 证书提示框，请点击【是 (Y)】允许信任）",
            ):
                clean_legacy_leaked_ca()
                self.install_ca()
                self.update_ca_status()
                return

        if not current_installed:
            self.prompt_first_run_ca()

    def prompt_first_run_ca(self):
        if not is_ca_installed():
            if messagebox.askyesno(
                "根证书安装引导",
                "检测到本机尚未安装/信任加速器专属的 HTTPS 根证书。\n\n"
                "碧蓝幻想的大部分静态资源（立绘、音频、脚本等）均通过 HTTPS 传输。"
                "安装根证书后，加速器才能解密并为您极速缓存这些静态素材。\n\n"
                "是否立即启动一键安装向导？\n"
                "（若弹出系统安全提示框，请点击【是 (Y)】允许信任）",
            ):
                self.install_ca()

    def clear_cache_dialog(self):
        current_dir = Path(self.var_cache_dir.get()).resolve()
        if not messagebox.askyesno(
            "清空本地缓存确认",
            f"确定要清空全部本地缓存吗？\n\n"
            f"目标缓存目录：\n{current_dir}\n\n"
            "⚠️ 注意事项：\n"
            "• 将删除该目录下的所有静态资源（立绘、音频、剧场漫画等）\n"
            "• 若您直接复用了 ACGPower 缓存目录，此操作将清空 ACGPower 的已有资源！\n"
            "• 将同步清空内存热点缓存 (RAM Cache)\n\n"
            "确认执行清空？",
        ):
            return
        deleted, freed = cache_manager.clear_all_cache()
        mb = freed / (1024 * 1024)
        messagebox.showinfo("清空完成", f"本地缓存已清空！\n已删除 {deleted} 个文件，释放 {mb:.1f} MB 磁盘空间。")

    def browse_cache_dir(self):
        chosen = filedialog.askdirectory(title="选择 GBF 本地缓存保存目录", initialdir=self.var_cache_dir.get())
        if chosen:
            p = Path(chosen).resolve()
            self.var_cache_dir.set(str(p))
            cache_manager.set_cache_base(p)
            config_manager.config["cache_dir"] = str(p)
            config_manager.save_config()
            messagebox.showinfo("缓存设置", f"缓存目录已成功更改为：\n{p}")

    def detect_acgp(self):
        found = auto_detect_acgpower_cache()
        if found:
            self.var_cache_dir.set(str(found))
            cache_manager.set_cache_base(found)
            config_manager.config["cache_dir"] = str(found)
            config_manager.save_config()
            messagebox.showinfo("ACGP 探测成功", f"成功检测到 ACGPower 缓存目录！\n\n路径：{found}\n\n已自动关联，无需重新下载游戏静态资源！")
        else:
            messagebox.showwarning("探测结果", "在常用盘符（C/D/E/F 盘）中未找到现成的 ACGPower 缓存。\n你可以点击【浏览...】手动指定。")

    def probe_upstream(self):
        if self.var_direct_mode.get():
            messagebox.showinfo("直连模式", "当前已启用直连模式，请先取消勾选后再探测上游代理。")
            return
        detected = detect_upstream_proxies()
        if not detected:
            messagebox.showwarning(
                "探测结果",
                "未检测到已运行的上游代理。\n\n支持的默认端口：Clash/v2rayN，以及岛风 GO 8099。",
            )
            return

        if len(detected) == 1:
            active = detected[0][0]
        else:
            active = self.choose_upstream(detected)
            if not active:
                return

        self.var_upstream.set(active)
        config_manager.config["upstream_proxy"] = active
        config_manager.save_config()
        gbf_proxy.UPSTREAM_PROXY = active
        # Check connectivity
        ok, msg = check_upstream_connectivity(active)
        # Restart proxy if running so connection pool picks up the new upstream proxy
        if gbf_proxy.PROXY_STATS.get("is_running", False):
            self.stop_proxy()
            self.start_proxy()
        if ok:
            if "8099" in active and not self.var_shimakaze_mode.get():
                if messagebox.askyesno(
                    "岛风GO 适配建议",
                    "检测到上游代理为岛风GO (8099)。\n\n是否立即启用【岛风GO 兼容优化模式】？\n\n（将自动放宽超时、启用断线自愈重试、适配自签证书；请记得在岛风GO主界面关闭远端缓存）",
                ):
                    self.var_shimakaze_mode.set(True)
                    config_manager.config["shimakaze_mode"] = True
                    config_manager.save_config()
                    gbf_proxy.SHIMAKAZE_MODE = True
                    self.update_shimakaze_controls()
            messagebox.showinfo("上游探测结果", f"检测并连通本地代理服务：\n{active}\n\n代理连接池已更新生效！")
        else:
            messagebox.showwarning("上游探测警告", f"检测到本地代理地址：\n{active}\n\n但连通测试失败：{msg}\n请确认 Clash 是否已启动并开启本地监听。")

    def choose_upstream(self, detected):
        """Show all detected upstreams and return the user's selected URL."""
        dialog = tk.Toplevel(self.root)
        dialog.title("选择上游代理")
        dialog.transient(self.root)
        dialog.grab_set()
        dialog.resizable(False, False)

        ttk.Label(dialog, text="检测到多个上游代理，请选择要使用的代理：").pack(anchor="w", padx=14, pady=(12, 6))

        listbox = tk.Listbox(dialog, width=58, height=min(8, max(3, len(detected))), exportselection=False)
        listbox.pack(fill="both", expand=True, padx=14, pady=(0, 8))
        for url, name in detected:
            listbox.insert(tk.END, f"{name}  —  {url}")
        listbox.selection_set(0)
        listbox.focus_set()

        result = {"url": None}

        def confirm():
            selected = listbox.curselection()
            if selected:
                result["url"] = detected[selected[0]][0]
            dialog.destroy()

        def cancel():
            dialog.destroy()

        buttons = ttk.Frame(dialog)
        buttons.pack(fill="x", padx=14, pady=(0, 12))
        ttk.Button(buttons, text="确认", width=10, command=confirm).pack(side="right")
        ttk.Button(buttons, text="取消", width=10, command=cancel).pack(side="right", padx=(0, 6))
        dialog.bind("<Return>", lambda _event: confirm())
        dialog.bind("<Escape>", lambda _event: cancel())

        # Center the selection dialog over the main window instead of letting
        # Tk place it at the screen's top-left corner.
        dialog.update_idletasks()
        parent_x = self.root.winfo_rootx()
        parent_y = self.root.winfo_rooty()
        parent_w = self.root.winfo_width()
        parent_h = self.root.winfo_height()
        dialog_w = dialog.winfo_width()
        dialog_h = dialog.winfo_height()
        x = parent_x + max(0, (parent_w - dialog_w) // 2)
        y = parent_y + max(0, (parent_h - dialog_h) // 2)
        dialog.geometry(f"+{x}+{y}")

        self.root.wait_window(dialog)
        return result["url"]

    def update_shimakaze_controls(self):
        direct = self.var_direct_mode.get()
        if self.var_shimakaze_mode.get() and not direct:
            self.lbl_shimakaze_hint.pack(anchor="w", padx=(20, 0), pady=(0, 4), after=self.chk_shimakaze)
        else:
            self.lbl_shimakaze_hint.pack_forget()

    def toggle_shimakaze_mode(self):
        enabled = self.var_shimakaze_mode.get()
        self.update_shimakaze_controls()
        config_manager.config["shimakaze_mode"] = enabled
        config_manager.save_config()
        gbf_proxy.SHIMAKAZE_MODE = enabled
        if gbf_proxy.PROXY_STATS.get("is_running", False):
            self.stop_proxy()
            self.start_proxy()

    def update_upstream_controls(self):
        """Disable upstream controls while direct mode is active."""
        direct = self.var_direct_mode.get()
        self.entry_up.configure(state="disabled" if direct else "normal")
        self.btn_confirm_upstream.configure(state="disabled" if direct else "normal")
        self.btn_probe.configure(state="disabled" if direct else "normal")
        self.chk_shimakaze.configure(state="disabled" if direct else "normal")
        self.update_shimakaze_controls()

    def reset_port_default(self):
        self.var_listen_port.set("8124")

    def confirm_upstream(self):
        """Immediately switch the live connection pool to the entered upstream."""
        if self.var_direct_mode.get():
            return

        upstream = self.var_upstream.get().strip()
        try:
            parsed = urllib.parse.urlparse(upstream)
            if parsed.scheme.lower() not in ("http", "https", "socks5", "socks5h") or not parsed.hostname:
                raise ValueError("支持 http、https、socks5 或 socks5h 上游代理地址")
            if parsed.port is None or not (1 <= parsed.port <= 65535):
                raise ValueError("上游代理端口无效")
            if parsed.port == gbf_proxy.LISTEN_PORT:
                raise ValueError("上游代理端口不能与本地监听端口相同")
        except ValueError as exc:
            messagebox.showerror("上游代理地址无效", str(exc))
            return

        ok, msg = check_upstream_connectivity(upstream)
        if not ok and not messagebox.askyesno(
            "上游代理连通警告",
            f"当前地址测试失败：\n{msg}\n\n仍然立即切换到这个上游代理吗？",
        ):
            return

        config_manager.config["upstream_proxy"] = upstream
        config_manager.save_config()
        gbf_proxy.UPSTREAM_PROXY = upstream

        was_running = gbf_proxy.PROXY_STATS.get("is_running", False)
        if was_running:
            # stop_proxy_thread cancels active sessions and closes the old
            # httpx pool before start_proxy creates a pool for the new proxy.
            self.stop_proxy()
            self.start_proxy()
        else:
            self.var_status_text.set("● 上游代理已确认（服务未启动）")

        if ok:
            messagebox.showinfo("上游代理已切换", f"已断开旧连接并切换到：\n{upstream}")

    def save_settings(self):
        up = self.var_upstream.get().strip()
        cd = self.var_cache_dir.get().strip()
        port_str = self.var_listen_port.get().strip()

        if not up and not self.var_direct_mode.get():
            messagebox.showerror("错误", "上游代理地址不能为空！")
            return

        try:
            port = int(port_str)
            if not (1 <= port <= 65535):
                raise ValueError()
        except ValueError:
            messagebox.showerror("错误", "本地监听端口必须是 1 到 65535 之间的有效整数！")
            return

        # Prevent port conflict with upstream proxy
        try:
            parsed_up = urllib.parse.urlparse(up)
            if not self.var_direct_mode.get() and parsed_up.port == port:
                messagebox.showerror("端口冲突", "本地监听端口不能与上游代理端口相同！")
                return
        except Exception:
            pass

        old_port = gbf_proxy.LISTEN_PORT
        old_up = gbf_proxy.UPSTREAM_PROXY
        old_direct = bool(config_manager.config.get("direct_mode", False))
        old_shimakaze = bool(config_manager.config.get("shimakaze_mode", False))
        port_changed = (port != old_port)
        upstream_changed = (up != old_up)
        direct_changed = (self.var_direct_mode.get() != old_direct)
        shimakaze_changed = (self.var_shimakaze_mode.get() != old_shimakaze)

        # Check connectivity to upstream
        up_ok, up_msg = check_upstream_connectivity(up)
        if not up_ok and not self.var_direct_mode.get():
            if not messagebox.askyesno("上游代理连通警告", f"测试连接上游代理失败：\n{up_msg}\n\n是否仍然保存该代理地址？"):
                return

        config_manager.config["upstream_proxy"] = up
        config_manager.config["direct_mode"] = self.var_direct_mode.get()
        config_manager.config["shimakaze_mode"] = self.var_shimakaze_mode.get()
        config_manager.config["cache_dir"] = cd
        config_manager.config["listen_port"] = port
        config_manager.config["auto_system_proxy"] = self.var_auto_pac.get()
        config_manager.config["enable_ram_cache"] = self.var_ram_cache.get()
        config_manager.config["enable_browser_cache"] = self.var_browser_cache.get()
        config_manager.config["enable_auto_repair"] = self.var_auto_repair.get()
        config_manager.save_config()

        if not self.var_ram_cache.get():
            cache_manager.clear_ram_cache()

        gbf_proxy.UPSTREAM_PROXY = up
        gbf_proxy.DIRECT_MODE = self.var_direct_mode.get()
        gbf_proxy.SHIMAKAZE_MODE = self.var_shimakaze_mode.get()
        if cd:
            cache_manager.set_cache_base(Path(cd).resolve())

        # Update local proxy.pac file
        from app_main import update_pac_file
        update_pac_file(port)

        if (port_changed or upstream_changed or direct_changed or shimakaze_changed) and gbf_proxy.PROXY_STATS.get("is_running", False):
            self.stop_proxy()
            gbf_proxy.LISTEN_PORT = port
            gbf_proxy.UPSTREAM_PROXY = up
            self.start_proxy()
            messagebox.showinfo("保存成功", f"配置已保存！\n代理服务已自动重启生效（上游：{up}，端口：{port}）。")
        else:
            gbf_proxy.LISTEN_PORT = port
            gbf_proxy.UPSTREAM_PROXY = up
            messagebox.showinfo("保存成功", "配置已保存成功！")

    def toggle_perf_settings(self):
        config_manager.config["enable_ram_cache"] = self.var_ram_cache.get()
        config_manager.config["enable_browser_cache"] = self.var_browser_cache.get()
        config_manager.config["enable_auto_repair"] = self.var_auto_repair.get()
        config_manager.save_config()
        if not self.var_ram_cache.get():
            cache_manager.clear_ram_cache()

    def toggle_direct_mode(self):
        """Switch routing immediately while keeping the local cache/proxy active."""
        enabled = self.var_direct_mode.get()
        self.update_upstream_controls()
        config_manager.config["direct_mode"] = enabled
        config_manager.save_config()
        gbf_proxy.DIRECT_MODE = enabled
        if gbf_proxy.PROXY_STATS.get("is_running", False):
            self.stop_proxy()
            self.start_proxy()
        else:
            self.var_status_text.set("● 直连模式已启用" if enabled else "● 上游代理模式已启用")

    def toggle_sys_proxy_setting(self):
        enabled = self.var_auto_pac.get()
        config_manager.config["auto_system_proxy"] = enabled
        config_manager.save_config()
        if gbf_proxy.PROXY_STATS.get("is_running", False):
            if enabled:
                system_proxy.enable_pac_proxy(f"http://127.0.0.1:{gbf_proxy.LISTEN_PORT}/proxy.pac")
            else:
                system_proxy.disable_pac_proxy()

    def toggle_startup_setting(self):
        enabled = self.var_auto_start.get()
        ok, msg = set_startup_enabled(enabled)
        if not ok:
            self.var_auto_start.set(not enabled)
            messagebox.showerror("开机自启设置失败", msg or "无法修改 Windows 开机启动项。")
            return
        config_manager.config["auto_start"] = enabled
        config_manager.save_config()

    def open_cache_folder(self):
        p = Path(self.var_cache_dir.get()).resolve()
        p.mkdir(parents=True, exist_ok=True)
        os.startfile(str(p))

    def show_guide(self):
        base_dir = get_base_dir()
        readme = base_dir / "使用说明.txt"
        if readme.is_file():
            os.startfile(str(readme))
        else:
            messagebox.showinfo("分流指引", "请使用 ZeroOmega / SwitchyOmega 导入同目录下的 SwitchyOmega_GBF.bak，或直接勾选【自动配置 Windows 系统 PAC 代理】实现免插件极速游玩。")

    def toggle_proxy(self):
        if gbf_proxy.PROXY_STATS["is_running"]:
            self.stop_proxy()
        else:
            self.start_proxy()

    def start_proxy(self):
        port_str = self.var_listen_port.get().strip()
        try:
            port = int(port_str)
            if not (1 <= port <= 65535):
                raise ValueError()
        except ValueError:
            messagebox.showerror("端口错误", "本地监听端口必须是 1 到 65535 之间的整数！")
            return

        up = self.var_upstream.get().strip() or "http://127.0.0.1:7897"
        try:
            parsed_up = urllib.parse.urlparse(up)
            if not self.var_direct_mode.get() and parsed_up.port == port:
                messagebox.showerror("端口冲突", "本地监听端口不能与上游代理端口相同！")
                return
        except Exception:
            pass

        gbf_proxy.LISTEN_HOST = config_manager.config.get("listen_host", "127.0.0.1")
        gbf_proxy.LISTEN_PORT = port
        gbf_proxy.UPSTREAM_PROXY = up
        gbf_proxy.DIRECT_MODE = self.var_direct_mode.get()
        gbf_proxy.SHIMAKAZE_MODE = self.var_shimakaze_mode.get()
        config_manager.config["listen_port"] = port
        config_manager.config["upstream_proxy"] = up
        config_manager.config["direct_mode"] = self.var_direct_mode.get()
        config_manager.config["shimakaze_mode"] = self.var_shimakaze_mode.get()
        config_manager.config["enable_ram_cache"] = self.var_ram_cache.get()
        config_manager.config["enable_browser_cache"] = self.var_browser_cache.get()
        config_manager.config["enable_auto_repair"] = self.var_auto_repair.get()
        config_manager.save_config()

        cache_manager.set_cache_base(Path(self.var_cache_dir.get()).resolve())

        # Update local proxy.pac file
        from app_main import update_pac_file
        update_pac_file(port)

        gbf_proxy.start_proxy_thread()

        # Wait for actual socket bind success (up to 2 seconds)
        is_ready = gbf_proxy.proxy_ready_event.wait(timeout=2.0)
        is_running = gbf_proxy.PROXY_STATS.get("is_running", False)

        if is_ready and is_running:
            if self.var_auto_pac.get():
                system_proxy.enable_pac_proxy(f"http://127.0.0.1:{gbf_proxy.LISTEN_PORT}/proxy.pac")

            self.var_status_text.set(f"● 运行中 (监听端口 {gbf_proxy.LISTEN_PORT})")
            self.lbl_status.configure(foreground="#28a745")
            self.btn_toggle.configure(text="停止加速", bg="#dc3545", activebackground="#bd2130")
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(True)
        else:
            err = gbf_proxy.PROXY_STATS.get("last_error", "端口绑定失败或超时")
            self.var_status_text.set(f"● 启动失败: {err[:20]}")
            self.lbl_status.configure(foreground="#dc3545")
            self.btn_toggle.configure(text="启动加速", bg="#28a745", activebackground="#218838")
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(False)
            messagebox.showerror("启动失败", f"代理服务无法在端口 {port} 启动：\n{err}\n\n请尝试更换端口或检查是否有其他程序占用。")

    def stop_proxy(self):
        gbf_proxy.stop_proxy_thread()
        if self.var_auto_pac.get():
            system_proxy.disable_pac_proxy()
        self.var_status_text.set("● 服务已停止")
        self.lbl_status.configure(foreground="#6c757d")
        self.btn_toggle.configure(text="启动加速", bg="#28a745", activebackground="#218838")
        if self.tray_icon:
            self.tray_icon.icon = create_tray_icon_image(False)

    def update_stats_loop(self):
        # Update numbers from PROXY_STATS
        hits = gbf_proxy.PROXY_STATS.get("hits", 0)
        ram_hits = gbf_proxy.PROXY_STATS.get("ram_hits", 0)
        dls = gbf_proxy.PROXY_STATS.get("downloads", 0)
        apis = gbf_proxy.PROXY_STATS.get("apis", 0)
        if ram_hits > 0:
            self.var_hits.set(f"{hits:,} (内存 {ram_hits:,})")
        else:
            self.var_hits.set(f"{hits:,}")
        self.var_downloads.set(f"{dls:,}")
        self.var_apis.set(f"{apis:,}")

        # Sync button text if state changed outside
        is_thread_alive = gbf_proxy.proxy_thread is not None and gbf_proxy.proxy_thread.is_alive()
        is_running = gbf_proxy.PROXY_STATS.get("is_running", False) or is_thread_alive
        last_error = gbf_proxy.PROXY_STATS.get("last_error", "")

        if is_running and "停止" not in self.btn_toggle.cget("text"):
            self.btn_toggle.configure(text="停止加速", bg="#dc3545", activebackground="#bd2130")
            self.var_status_text.set(f"● 运行中 (监听端口 {gbf_proxy.LISTEN_PORT})")
            self.lbl_status.configure(foreground="#28a745")
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(True)
        elif not is_running and "启动" not in self.btn_toggle.cget("text"):
            self.btn_toggle.configure(text="启动加速", bg="#28a745", activebackground="#218838")
            if last_error:
                self.var_status_text.set(f"● 异常停止: {last_error[:25]}")
                self.lbl_status.configure(foreground="#dc3545")
            else:
                self.var_status_text.set("● 服务已停止")
                self.lbl_status.configure(foreground="#6c757d")
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(False)

        # Schedule next update
        self.root.after(800, self.update_stats_loop)

    # ================= System Tray =================
    def setup_tray(self):
        icon_img = create_tray_icon_image(True)
        menu = pystray.Menu(
            pystray.MenuItem("显示主界面", self.show_from_tray, default=True),
            pystray.MenuItem("启动 / 暂停加速", self.toggle_proxy_from_tray),
            pystray.MenuItem("打开缓存目录", lambda: self.open_cache_folder()),
            pystray.Menu.SEPARATOR,
            pystray.MenuItem("彻底退出", self.quit_app),
        )
        self.tray_icon = pystray.Icon("GBF_Speed_Proxy", icon_img, f"GBF 加速代理 (端口 {gbf_proxy.LISTEN_PORT})", menu)
        # Run tray in separate background thread
        threading.Thread(target=self.tray_icon.run, daemon=True).start()

    def hide_to_tray(self, notify=True):
        self.root.withdraw()
        if notify:
            try:
                if self.tray_icon:
                    self.tray_icon.notify("GBF 加速代理已最小化到系统托盘，正在后台运行。", "GBF 加速代理")
            except Exception:
                pass

    def show_from_tray(self):
        self.root.deiconify()
        self.root.lift()
        self.root.focus_force()

    def toggle_proxy_from_tray(self):
        self.root.after(0, self.toggle_proxy)

    def quit_app(self):
        gbf_proxy.stop_proxy_thread()
        system_proxy.disable_pac_proxy()
        if self.tray_icon:
            self.tray_icon.stop()
        self.root.after(0, self.root.destroy)

def main():
    root = tk.Tk()
    app = GBFAcceleratorGUI(root)
    if START_MINIMIZED:
        root.after(100, lambda: app.hide_to_tray(notify=False))
    root.mainloop()

if __name__ == "__main__":
    main()
