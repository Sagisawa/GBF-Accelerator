import os
import sys
import time
import datetime
import re
import threading
import subprocess
import urllib.parse
import webbrowser
import collections
import zipfile
from typing import Optional, List, Tuple, Dict, Any
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
from PIL import Image, ImageDraw, ImageFont, ImageTk
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
    normalize_cache_dir,
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
MONO_FONT = "Menlo" if sys.platform == "darwin" else "Consolas"

def open_path(path: Any, select: bool = False):
    """Cross-platform helper to open a file or directory in file manager / default app."""
    try:
        p = Path(path).resolve()
        if sys.platform == "win32":
            if select and p.is_file():
                try:
                    subprocess.Popen(["explorer", f"/select,{str(p)}"])
                    return
                except Exception:
                    pass
            os.startfile(str(p))
        elif sys.platform == "darwin":
            if select and p.is_file():
                subprocess.Popen(["open", "-R", str(p)])
            else:
                subprocess.Popen(["open", str(p)])
        else:
            subprocess.Popen(["xdg-open", str(p)])
    except Exception:
        pass

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

# Tk's own emoji font metrics don't match the rendered glyph width, which pushes
# button text off-center. Emoji are therefore rendered to fixed-size bitmaps and
# attached via image+compound so centering stays exact.
_EMOJI_FONT_PATHS = (
    r"C:\Windows\Fonts\seguiemj.ttf",
    r"C:\Windows\Fonts\seguisym.ttf",
)

def render_emoji_image(emoji: str, size: int = 15):
    """Render an emoji glyph to a transparent RGBA bitmap. None = no usable font."""
    font_path = next((p for p in _EMOJI_FONT_PATHS if Path(p).is_file()), None)
    if font_path is None:
        return None
    try:
        big = max(48, size * 4)
        font = ImageFont.truetype(font_path, big)
        canvas = Image.new("RGBA", (big * 2, big * 2), (0, 0, 0, 0))
        draw = ImageDraw.Draw(canvas)
        try:
            draw.text((big // 2, big // 2), emoji, font=font, embedded_color=True)
        except Exception:
            draw.text((big // 2, big // 2), emoji, font=font, fill=(70, 70, 70, 255))
        bbox = canvas.getbbox()
        if not bbox:
            return None
        glyph = canvas.crop(bbox)
        glyph.thumbnail((size, size), Image.LANCZOS)
        out = Image.new("RGBA", (size, size), (0, 0, 0, 0))
        out.paste(glyph, ((size - glyph.width) // 2, (size - glyph.height) // 2), glyph)
        return out
    except Exception:
        return None

# Native macOS window minimization and menu bar integration state
_MAC_MAIN_NSWINDOW = None
_MAC_GUI_INSTANCE = None
_MAC_TK_CATEGORY_INSTALLED = False
_MAC_STATUS_BAR_MANAGER = None

def _is_main_mac_window(ns_window) -> bool:
    global _MAC_MAIN_NSWINDOW, _MAC_GUI_INSTANCE
    if not ns_window or not _MAC_GUI_INSTANCE or not hasattr(_MAC_GUI_INSTANCE, "root"):
        return False
    try:
        if _MAC_MAIN_NSWINDOW is not None:
            if ns_window == _MAC_MAIN_NSWINDOW or ns_window.windowNumber() == _MAC_MAIN_NSWINDOW.windowNumber():
                return True
        main_title = _MAC_GUI_INSTANCE.root.title()
        if main_title and ns_window.title() == main_title:
            _MAC_MAIN_NSWINDOW = ns_window
            return True
    except Exception:
        pass
    return False

def _ensure_mac_tk_category():
    """Dynamically register an Objective-C Category on TKWindow to intercept minimize events.
    Prevents the main window from shrinking into the macOS Dock while redirecting
    it directly to hide into the menu bar status item. Subwindows (e.g. LogViewer)
    retain standard minimization behavior.
    """
    global _MAC_TK_CATEGORY_INSTALLED
    if _MAC_TK_CATEGORY_INSTALLED or sys.platform != "darwin":
        return
    try:
        import AppKit
        import objc
        TKWindowClass = AppKit.NSClassFromString("TKWindow")
        if not TKWindowClass:
            return

        class TKWindow(objc.Category(TKWindowClass)):
            def performMiniaturize_(self, sender):
                if _is_main_mac_window(self):
                    if _MAC_GUI_INSTANCE and hasattr(_MAC_GUI_INSTANCE, "root"):
                        _MAC_GUI_INSTANCE.root.after(10, lambda: _MAC_GUI_INSTANCE.hide_to_tray(notify=False))
                    return
                try:
                    objc.super(TKWindow, self).performMiniaturize_(sender)
                except Exception:
                    pass

            def miniaturize_(self, sender):
                if _is_main_mac_window(self):
                    if _MAC_GUI_INSTANCE and hasattr(_MAC_GUI_INSTANCE, "root"):
                        _MAC_GUI_INSTANCE.root.after(10, lambda: _MAC_GUI_INSTANCE.hide_to_tray(notify=False))
                    return
                try:
                    objc.super(TKWindow, self).miniaturize_(sender)
                except Exception:
                    pass

        _MAC_TK_CATEGORY_INSTALLED = True
    except Exception:
        pass

class MacStatusBarManager:
    """Native macOS Menu Bar Status Item integration via PyObjC AppKit.
    Integrates directly with the main Cocoa event loop on the main thread,
    attaching a native NSMenu to NSStatusItem to ensure 100% thread safety
    and crash-free operation across Python 3.14 / macOS Sequoia.
    """
    def __init__(self, gui: "GBFAcceleratorGUI"):
        global _MAC_STATUS_BAR_MANAGER
        _MAC_STATUS_BAR_MANAGER = self
        self.gui = gui
        self.status_item = None
        self._current_is_running: bool = False
        self.setup_menu_bar()

    def setup_menu_bar(self):
        try:
            import AppKit
            import objc
            status_bar = AppKit.NSStatusBar.systemStatusBar()
            self.status_item = status_bar.statusItemWithLength_(AppKit.NSVariableStatusItemLength)

            gui_ref = self.gui

            # Context menu actions (decoupled from Cocoa tracking loop via root.after)
            class MacMenuActions(AppKit.NSObject):
                @objc.IBAction
                def toggleWindow_(self, sender):
                    gui_ref.root.after(50, gui_ref.toggle_from_tray)

                @objc.IBAction
                def toggleProxy_(self, sender):
                    gui_ref.root.after(50, gui_ref.toggle_proxy_from_tray)

                @objc.IBAction
                def openCache_(self, sender):
                    gui_ref.root.after(50, gui_ref.open_cache_folder)

                @objc.IBAction
                def quitApp_(self, sender):
                    gui_ref.root.after(50, gui_ref.quit_app)

            self.menu_actions = MacMenuActions.alloc().init()
            self.menu = AppKit.NSMenu.alloc().init()

            item_show = AppKit.NSMenuItem.alloc().initWithTitle_action_keyEquivalent_("显示 / 隐藏主界面", "toggleWindow:", "")
            item_show.setTarget_(self.menu_actions)
            self.menu.addItem_(item_show)

            item_toggle = AppKit.NSMenuItem.alloc().initWithTitle_action_keyEquivalent_("启动 / 暂停加速", "toggleProxy:", "")
            item_toggle.setTarget_(self.menu_actions)
            self.menu.addItem_(item_toggle)

            item_cache = AppKit.NSMenuItem.alloc().initWithTitle_action_keyEquivalent_("打开缓存目录", "openCache:", "")
            item_cache.setTarget_(self.menu_actions)
            self.menu.addItem_(item_cache)

            self.menu.addItem_(AppKit.NSMenuItem.separatorItem())

            item_quit = AppKit.NSMenuItem.alloc().initWithTitle_action_keyEquivalent_("彻底退出", "quitApp:", "")
            item_quit.setTarget_(self.menu_actions)
            self.menu.addItem_(item_quit)

            # Native macOS menu bar integration: attach menu directly to status item
            self.status_item.setMenu_(self.menu)

            self.update_icon(gbf_proxy.PROXY_STATS.get("is_running", False))
        except Exception:
            self.status_item = None

    def update_icon(self, is_running: bool):
        self._current_is_running = is_running
        if not self.status_item:
            return
        try:
            import AppKit
            import io
            img = create_tray_icon_image(is_running)
            buf = io.BytesIO()
            img.save(buf, format="PNG")
            ns_data = AppKit.NSData.dataWithBytes_length_(buf.getvalue(), len(buf.getvalue()))
            ns_img = AppKit.NSImage.alloc().initWithData_(ns_data)
            ns_img.setSize_(AppKit.NSMakeSize(18, 18))

            btn = self.status_item.button()
            btn.setImage_(ns_img)
            status_text = "加速中" if is_running else "已暂停"
            btn.setToolTip_(f"GBF 加速代理 [{status_text}] (端口 {gbf_proxy.LISTEN_PORT})\n点击打开快捷操作菜单")
        except Exception:
            pass

    @property
    def icon(self):
        return None

    @icon.setter
    def icon(self, val):
        self.update_icon(gbf_proxy.PROXY_STATS.get("is_running", False))

    def notify(self, message: str, title: str = "GBF 加速代理"):
        try:
            msg_clean = message.replace("\\", "\\\\").replace('"', '\\"')
            title_clean = title.replace("\\", "\\\\").replace('"', '\\"')
            subprocess.Popen([
                "osascript", "-e",
                f'display notification "{msg_clean}" with title "{title_clean}"'
            ])
        except Exception:
            pass

    def stop(self):
        global _MAC_STATUS_BAR_MANAGER
        if _MAC_STATUS_BAR_MANAGER is self:
            _MAC_STATUS_BAR_MANAGER = None
        if self.status_item:
            try:
                import AppKit
                AppKit.NSStatusBar.systemStatusBar().removeStatusItem_(self.status_item)
            except Exception:
                pass
            self.status_item = None

class GBFAcceleratorGUI:
    def __init__(self, root: tk.Tk):
        self.root = root
        self.window_icon = ImageTk.PhotoImage(create_tray_icon_image(True))
        self.root.iconphoto(True, self.window_icon)
        self.root.title("GBF 加速器")

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
        self.var_allow_lan = tk.BooleanVar(value=config_manager.config.get("allow_lan", False))
        self.var_lan_ip = tk.StringVar(value=config_manager.get_lan_ip())
        self.var_ca_status = tk.StringVar(value="检测中...")
        self.var_ca_fp = tk.StringVar(value="")
        self.var_auto_pac = tk.BooleanVar(value=config_manager.config.get("auto_system_proxy", True))
        self.var_auto_start = tk.BooleanVar(
            value=bool(config_manager.config.get("auto_start", False)) or is_startup_enabled()
        )

        # Performance & Resource Controls
        self.var_ram_cache = tk.BooleanVar(value=config_manager.config.get("enable_ram_cache", True))
        self.var_browser_cache = tk.BooleanVar(value=config_manager.config.get("enable_browser_cache", True))
        self.var_auto_repair = tk.BooleanVar(value=config_manager.config.get("enable_auto_repair", True))
        self.var_prefetch = tk.BooleanVar(value=config_manager.config.get("enable_prefetch", True))
        self.var_ram_warmup = tk.BooleanVar(value=config_manager.config.get("enable_ram_warmup", True))
        self.var_ram_max_mb = tk.StringVar(value=str(config_manager.config.get("ram_cache_max_mb", 256)))
        self.var_auto_update = tk.BooleanVar(value=config_manager.config.get("auto_check_update", True))
        self.update_info: Optional[UpdateInfo] = None
        self.log_window: Optional["LogViewerWindow"] = None

        # Emoji icon cache (buttons attach icons via image+compound for exact centering)
        self._icon_cache: dict = {}
        self._icon_size = None

        # Build UI
        self.build_ui()
        self.update_shimakaze_controls()

        # Auto-fit window size to the real layout: responsive geometry suitable for
        # macOS 13" laptops (1440x900, 1280x800) as well as larger desktop displays.
        self.root.update_idletasks()
        screen_w = self.root.winfo_screenwidth()
        screen_h = self.root.winfo_screenheight()
        win_w = max(740, min(800, screen_w - 40))
        if screen_h <= 900:
            win_h = max(560, min(680, screen_h - 120))
        else:
            win_h = max(640, min(840, screen_h - 160))
        self.root.geometry(f"{win_w}x{win_h}")
        self.root.minsize(700, 420)

        # Center window after layout is constructed
        self.center_window()

        # System tray setup
        self.tray_icon = None
        self.setup_tray()

        # Window events
        self.root.protocol("WM_DELETE_WINDOW", self.hide_to_tray)
        self.root.bind("<Map>", self._on_window_map)
        self.root.bind("<MouseWheel>", self._on_main_mousewheel, add="+")
        self.root.bind("<Button-4>", self._on_main_mousewheel, add="+")
        self.root.bind("<Button-5>", self._on_main_mousewheel, add="+")
        if sys.platform == "darwin":
            try:
                self.root.createcommand("::tk::mac::ReopenApplication", self.show_from_tray)
                self.root.createcommand("::tk::mac::Quit", self.quit_app)
            except Exception:
                pass
            self.setup_mac_window_buttons()

        # Check CA status and detect legacy leaked cert
        self.update_ca_status()
        self.root.after(500, self.check_and_prompt_ca_state)

        # Ensure helper files
        from app_main import ensure_bundled_files
        ensure_bundled_files()

        # Start proxy thread automatically
        self.start_proxy()

        # Periodic timer for stats update (suspended when minimized/in tray)
        self._stats_job = None
        self._last_stats_cache = None
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

    def setup_mac_window_buttons(self):
        """Intercept the macOS standard yellow minimize button to hide directly to menu bar."""
        if sys.platform != "darwin":
            return
        global _MAC_MAIN_NSWINDOW, _MAC_GUI_INSTANCE
        _MAC_GUI_INSTANCE = self
        _ensure_mac_tk_category()

        try:
            import AppKit

            def _hook_window():
                global _MAC_MAIN_NSWINDOW
                try:
                    main_title = self.root.title()
                    for w in AppKit.NSApp.windows():
                        if w.className() == "TKWindow" and w.title() == main_title:
                            _MAC_MAIN_NSWINDOW = w
                            break
                except Exception:
                    pass

            self.root.update_idletasks()
            _hook_window()
            self.root.after(50, _hook_window)
            self.root.after(300, _hook_window)
        except Exception:
            pass

    def setup_styles(self):
        style = ttk.Style(self.root)
        available = style.theme_names()
        if "vista" in available:
            style.theme_use("vista")
        elif "winnative" in available:
            style.theme_use("winnative")
        else:
            style.theme_use("clam")
            # In clam theme, TButton defaults to width=-11 (min 11 chars wide)
            # which excessively widens buttons on macOS and Linux. Reset to natural width.
            style.configure("TButton", width=0, padding="4 2")

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
        style.configure(
            "Primary.TButton",
            background="#007bff",
            foreground="#ffffff",
            font=("Microsoft YaHei UI", 9, "bold"),
            borderwidth=0,
            padding="12 5",
        )
        style.map(
            "Primary.TButton",
            background=[("active", "#0069d9"), ("pressed", "#0069d9")],
            foreground=[("active", "#ffffff")],
        )

        style.configure(
            "Success.TButton",
            background="#28a745",
            foreground="#ffffff",
            font=("Microsoft YaHei UI", 10, "bold"),
            borderwidth=0,
            padding="14 6",
        )
        style.map(
            "Success.TButton",
            background=[("active", "#218838"), ("pressed", "#218838")],
            foreground=[("active", "#ffffff")],
        )

        style.configure(
            "Danger.TButton",
            background="#dc3545",
            foreground="#ffffff",
            font=("Microsoft YaHei UI", 10, "bold"),
            borderwidth=0,
            padding="14 6",
        )
        style.map(
            "Danger.TButton",
            background=[("active", "#bd2130"), ("pressed", "#bd2130")],
            foreground=[("active", "#ffffff")],
        )

        style.configure(
            "Warning.TButton",
            background="#ffc107",
            foreground="#212529",
            font=("Microsoft YaHei UI", 8, "bold"),
            borderwidth=0,
            padding="6 2",
        )
        style.map(
            "Warning.TButton",
            background=[("active", "#e0a800"), ("pressed", "#e0a800")],
            foreground=[("active", "#212529")],
        )

    def get_icon(self, emoji: str):
        """Cached PhotoImage for an emoji glyph, sized to match the 9pt UI font."""
        key = (emoji, self._icon_size)
        if key in self._icon_cache:
            return self._icon_cache[key]
        if self._icon_size is None:
            try:
                px = int(self.root.winfo_fpixels("9p"))
            except Exception:
                px = 16
            self._icon_size = max(14, min(px, 26))
        img = render_emoji_image(emoji, self._icon_size)
        if img is None:
            self._icon_cache[key] = False  # negative cache: no usable emoji font
            return None
        photo = ImageTk.PhotoImage(img)
        self._icon_cache[key] = photo
        return photo

    def _emoji_button(self, parent, emoji: str, text: str, command, style: str = None):
        """ttk.Button with a bitmap emoji icon so text stays perfectly centered."""
        icon = self.get_icon(emoji)
        if icon is not None:
            return ttk.Button(parent, text=f" {text}", image=icon, compound="left",
                              command=command, style=style)
        if emoji and sys.platform == "darwin":
            return ttk.Button(parent, text=f"{emoji} {text}", command=command, style=style)
        return ttk.Button(parent, text=text, command=command, style=style)

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
        if sys.platform == "darwin":
            self.btn_update_badge = ttk.Button(
                h_title_box,
                text=" 发现新版",
                image=self.get_icon("🔥"),
                compound="left",
                style="Warning.TButton",
                command=self.on_click_update_badge,
            )
        else:
            self.btn_update_badge = tk.Button(
                h_title_box,
                text=" 发现新版",
                image=self.get_icon("🔥"),
                compound="left",
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

        if sys.platform == "darwin":
            self.btn_toggle = ttk.Button(
                card_header,
                text="停止加速",
                style="Danger.TButton",
                command=self.toggle_proxy,
            )
        else:
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

        btn_open_folder = self._emoji_button(f_bottom, "📂", "缓存目录", self.open_cache_folder)
        btn_open_folder.pack(side="left", padx=(0, 3))

        btn_clear_cache = self._emoji_button(f_bottom, "🗑️", "清空缓存", self.clear_cache_dialog)
        btn_clear_cache.pack(side="left", padx=(0, 3))

        btn_proxy_guide = self._emoji_button(f_bottom, "🌐", "分流说明", self.show_guide)
        btn_proxy_guide.pack(side="left", padx=(0, 3))

        btn_github = self._emoji_button(f_bottom, "⭐", "GitHub", self.open_github)
        btn_github.pack(side="left", padx=(0, 3))

        self.btn_check_update = self._emoji_button(f_bottom, "🔄", "检查更新", self.manual_check_update)
        self.btn_check_update.pack(side="left", padx=(0, 3))

        self.btn_latency = self._emoji_button(f_bottom, "📡", "延迟测试", self.run_latency_test)
        self.btn_latency.pack(side="left", padx=(0, 3))

        self.btn_logs = self._emoji_button(f_bottom, "📜", "实时日志", self.show_log_window)
        self.btn_logs.pack(side="left", padx=(0, 3))

        tray_btn_text = "最小化到后台" if sys.platform == "darwin" else "最小化到托盘"
        btn_tray = self._emoji_button(f_bottom, "⬇", tray_btn_text, self.hide_to_tray)
        btn_tray.pack(side="right")

        # ---------------- 4. Scrollable Settings Card ----------------
        # Wrap settings inside a Canvas with vertical Scrollbar so on smaller displays
        # (e.g. 13" MacBook Air 1440x900 or 1280x800), the settings area scrolls smoothly
        # and bottom action buttons are never pushed off or clipped.
        scroll_card = ttk.Frame(main_container, style="Card.TFrame")
        scroll_card.pack(fill="both", expand=True)

        self.canvas_settings = tk.Canvas(
            scroll_card,
            bg="#ffffff",
            borderwidth=0,
            highlightthickness=0,
            yscrollincrement=10,
        )
        self.sb_settings = ttk.Scrollbar(scroll_card, orient="vertical", command=self.canvas_settings.yview)
        self.canvas_settings.configure(yscrollcommand=self.sb_settings.set)

        card_settings = ttk.Frame(self.canvas_settings, style="Card.TFrame", padding="14 10 14 10")
        self.card_settings_frame = card_settings
        self.canvas_settings_window = self.canvas_settings.create_window(
            (0, 0), window=card_settings, anchor="nw"
        )

        def _on_settings_frame_configure(event):
            self.canvas_settings.configure(scrollregion=self.canvas_settings.bbox("all"))

        card_settings.bind("<Configure>", _on_settings_frame_configure)

        def _on_settings_canvas_configure(event):
            self.canvas_settings.itemconfig(self.canvas_settings_window, width=event.width)
            if hasattr(self, "lbl_shimakaze_hint"):
                self.lbl_shimakaze_hint.configure(wraplength=max(480, event.width - 30))

        self.canvas_settings.bind("<Configure>", _on_settings_canvas_configure)

        self.canvas_settings.pack(side="left", fill="both", expand=True)
        self.sb_settings.pack(side="right", fill="y")

        ttk.Label(card_settings, text="配置选项", style="Title.TLabel").pack(anchor="w", pady=(0, 6))

        # Field 1: Local Cache Dir
        ttk.Label(card_settings, text="本地缓存目录（支持无缝复用 ACGPower 缓存）：", style="Normal.TLabel").pack(anchor="w")
        f_dir = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_dir.pack(fill="x", pady=(2, 6))

        self.entry_dir = ttk.Entry(f_dir, textvariable=self.var_cache_dir, font=(MONO_FONT, 9))
        self.entry_dir.pack(side="left", fill="x", expand=True, padx=(0, 6))

        btn_browse = ttk.Button(f_dir, text="浏览...", width=8, command=self.browse_cache_dir)
        btn_browse.pack(side="left", padx=(0, 4))

        btn_acgp = ttk.Button(f_dir, text="检测 ACGP", width=11, command=self.detect_acgp)
        btn_acgp.pack(side="left", padx=(0, 4))

        self.btn_audit_cache = self._emoji_button(f_dir, "🩺", "一键体检缓存", self.run_cache_audit)
        self.btn_audit_cache.pack(side="left")

        # Field 2: Upstream Proxy
        ttk.Label(card_settings, text="上游网络代理（Clash Verge / Clash / V2ray / 岛风GO 等）：", style="Normal.TLabel").pack(anchor="w")
        f_up = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_up.pack(fill="x", pady=(2, 6))

        self.entry_up = ttk.Entry(f_up, textvariable=self.var_upstream, font=(MONO_FONT, 9))
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
            text="提示：本软件架构升级后，日常使用可按需开启岛风GO【使用远端缓存】（可显著加快初次冷启动下载速度）；若遇游戏维护更新后新素材显示异常，在主界面点击【清理缓存】或临时关闭远端缓存即可。",
            style="Gray.TLabel",
            foreground="#1971c2",
            wraplength=560,
        )
        self.update_upstream_controls()

        # Field 3: Local Listen Port
        ttk.Label(card_settings, text="本地监听端口（默认 8124，支持自定义）：", style="Normal.TLabel").pack(anchor="w")
        f_port = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_port.pack(fill="x", pady=(2, 6))

        self.entry_port = ttk.Entry(f_port, textvariable=self.var_listen_port, font=(MONO_FONT, 9), width=10)
        self.entry_port.pack(side="left", padx=(0, 6))

        btn_reset_port = ttk.Button(f_port, text="恢复默认 (8124)", width=14, command=self.reset_port_default)
        btn_reset_port.pack(side="left", padx=(0, 6))

        btn_save = ttk.Button(f_port, text="保存配置", width=10, command=self.save_settings)
        btn_save.pack(side="left")

        # Allow LAN Checkbutton & Mobile guide button
        f_lan = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_lan.pack(fill="x", pady=(0, 4))

        self.chk_allow_lan = ttk.Checkbutton(
            f_lan,
            text="允许局域网连接 (Allow LAN) - 允许其他设备（iOS/iPad/安卓等）连接本代理（默认关闭）",
            variable=self.var_allow_lan,
            command=self.toggle_allow_lan_setting,
        )
        self.chk_allow_lan.pack(side="left")

        self.btn_lan_guide = ttk.Button(
            f_lan,
            text="📱 移动端/iOS 连接指引...",
            command=self.show_lan_guide,
        )
        self.btn_lan_guide.pack(side="left", padx=(8, 0))

        self.lbl_lan_status = ttk.Label(
            card_settings,
            text="",
            style="Gray.TLabel",
            foreground="#007bff",
        )
        self.update_lan_status_label()

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
        self.lbl_ca_fp = ttk.Label(f_ca_fp, textvariable=self.var_ca_fp, style="Gray.TLabel", font=(MONO_FONT, 8))
        self.lbl_ca_fp.pack(side="left")

        # Field 5: Windows System PAC Automation
        f_sys_proxy = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_sys_proxy.pack(fill="x", pady=(4, 2))
        pac_cb_text = "自动配置 Windows 系统 PAC 代理（开启后浏览器无需插件，仅分流 GBF 流量）" if sys.platform == "win32" else "自动配置 macOS 系统 PAC 代理（开启后浏览器无需插件，仅分流 GBF 流量）"
        chk_pac = ttk.Checkbutton(
            f_sys_proxy,
            text=pac_cb_text,
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
            text="启用内存热点缓存 (RAM Cache) - 占用约 256MB 内存，高频静态资源 0 磁盘 I/O 直接响应",
            variable=self.var_ram_cache,
            command=self.toggle_perf_settings,
        )
        chk_ram.pack(anchor="w", pady=2)

        f_ram_mb = ttk.Frame(f_perf, style="CardInner.TFrame")
        f_ram_mb.pack(anchor="w", pady=(0, 2))
        ttk.Label(f_ram_mb, text="内存缓存上限 (MB):", style="Normal.TLabel").pack(side="left")
        self.entry_ram_mb = ttk.Entry(f_ram_mb, textvariable=self.var_ram_max_mb, width=8, font=(MONO_FONT, 9))
        self.entry_ram_mb.pack(side="left", padx=(6, 6))
        self.btn_apply_ram = ttk.Button(f_ram_mb, text="应用", width=6, command=self.apply_ram_max_mb)
        self.btn_apply_ram.pack(side="left")
        self.lbl_ram_usage = ttk.Label(f_ram_mb, text="", style="Gray.TLabel")
        self.lbl_ram_usage.pack(side="left", padx=(10, 0))

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

        chk_prefetch = ttk.Checkbutton(
            f_perf,
            text="启用资源预加载 - 解析场景 JS 引用的素材并后台预热，首次进新副本/活动更流畅",
            variable=self.var_prefetch,
            command=self.toggle_perf_settings,
        )
        chk_prefetch.pack(anchor="w", pady=2)

        chk_warm = ttk.Checkbutton(
            f_perf,
            text="启动时预热内存缓存 - 把高频小文件预先载入 RAM，消除会话首读的磁盘延迟",
            variable=self.var_ram_warmup,
            command=self.toggle_perf_settings,
        )
        chk_warm.pack(anchor="w", pady=2)

    def _on_main_mousewheel(self, event):
        """Smooth mousewheel and trackpad scrolling for settings canvas."""
        try:
            if not hasattr(self, "canvas_settings") or not hasattr(self, "card_settings_frame"):
                return
            x, y = self.canvas_settings.winfo_pointerxy()
            w = self.canvas_settings.winfo_containing(x, y)
            is_inside = False
            while w:
                if w == self.canvas_settings or w == self.card_settings_frame:
                    is_inside = True
                    break
                w = getattr(w, "master", None)
            if not is_inside:
                return

            if sys.platform == "darwin":
                delta = getattr(event, "delta", 0)
                if abs(delta) < 1 and delta != 0:
                    step = -1 if delta > 0 else 1
                else:
                    step = int(-1 * round(delta))
                self.canvas_settings.yview_scroll(step, "units")
            elif sys.platform == "win32":
                self.canvas_settings.yview_scroll(int(-1 * (event.delta / 120)), "units")
            else:
                if getattr(event, "num", None) == 4:
                    self.canvas_settings.yview_scroll(-1, "units")
                elif getattr(event, "num", None) == 5:
                    self.canvas_settings.yview_scroll(1, "units")
        except Exception:
            pass

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
        is_direct = self.var_direct_mode.get()
        threading.Thread(target=self._bg_check_update, args=(False, is_direct), daemon=True).start()

    def manual_check_update(self):
        if hasattr(self, "btn_check_update") and self.btn_check_update.winfo_exists():
            self.btn_check_update.configure(text=" 检查中...", state="disabled")
        is_direct = self.var_direct_mode.get()
        threading.Thread(target=self._bg_check_update, args=(True, is_direct), daemon=True).start()

    def _bg_check_update(self, is_manual: bool, is_direct: bool):
        up = None if is_direct else gbf_proxy.UPSTREAM_PROXY
        info = update_manager.check_for_updates(upstream_proxy=up)
        try:
            self.root.after(0, self._handle_update_result, info, is_manual)
        except Exception:
            pass

    def _handle_update_result(self, info: UpdateInfo, is_manual: bool):
        self.update_info = info
        if hasattr(self, "btn_check_update") and self.btn_check_update.winfo_exists():
            self.btn_check_update.configure(text=" 检查更新", state="normal")

        if info.has_update:
            if hasattr(self, "btn_update_badge") and self.btn_update_badge.winfo_exists():
                self.btn_update_badge.configure(text=f" 发现新版 v{info.latest_version}")
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

        target_url = info.html_url or f"https://github.com/{update_manager.GITHUB_REPO}/releases/latest"

        # Adaptive window geometry
        screen_w = dialog.winfo_screenwidth()
        screen_h = dialog.winfo_screenheight()
        win_w = min(620, screen_w - 40)
        win_h = min(520, screen_h - 80)
        dialog.geometry(f"{win_w}x{win_h}")
        dialog.minsize(500, 380)
        dialog.transient(self.root)
        dialog.grab_set()

        # Center relative to parent
        self.root.update_idletasks()
        dialog.update_idletasks()
        rx = self.root.winfo_x()
        ry = self.root.winfo_y()
        rw = self.root.winfo_width()
        rh = self.root.winfo_height()
        x = max(0, rx + (rw - win_w) // 2)
        y = max(0, ry + (rh - win_h) // 2)
        dialog.geometry(f"+{x}+{y}")

        content = ttk.Frame(dialog, padding="16 14 16 14")
        content.pack(fill="both", expand=True)

        # 1. BOTTOM BUTTONS: Pack first with side="bottom" so they are ALWAYS visible
        f_btns = ttk.Frame(content)
        f_btns.pack(side="bottom", fill="x", pady=(10, 0))

        # Progress bar container (sits directly above f_btns)
        f_progress = ttk.Frame(content)
        f_progress.pack(side="bottom", fill="x", pady=(6, 2), before=f_btns)
        f_progress.pack_forget()  # Hidden until download begins

        var_prog_text = tk.StringVar(value="准备下载...")
        lbl_prog = ttk.Label(f_progress, textvariable=var_prog_text, style="Normal.TLabel")
        lbl_prog.pack(anchor="w", pady=(0, 4))

        pb_download = ttk.Progressbar(f_progress, mode="determinate", maximum=100, value=0)
        pb_download.pack(fill="x")

        download_url = info.download_url
        download_state = {
            "is_downloading": False,
            "cancel_event": None,
            "saved_path": None,
        }

        def open_browser():
            webbrowser.open(target_url)

        def copy_link():
            dialog.clipboard_clear()
            dialog.clipboard_append(target_url)
            messagebox.showinfo("已复制", "下载链接已复制到剪贴板！", parent=dialog)

        def open_download_folder():
            saved = download_state.get("saved_path")
            if saved and saved.is_file():
                open_path(saved, select=True)
            else:
                default_dir = update_manager.get_default_download_dir()
                open_path(default_dir)

        def open_download_file():
            saved = download_state.get("saved_path")
            if saved and saved.is_file():
                try:
                    open_path(saved)
                except Exception as e:
                    messagebox.showerror("打开失败", f"无法直接打开文件：\n{e}", parent=dialog)
            else:
                messagebox.showwarning("提示", "未找到已下载的文件", parent=dialog)

        def on_close_dialog():
            if download_state["is_downloading"]:
                if messagebox.askyesno("取消下载", "当前正在下载更新包，是否取消下载并关闭？", parent=dialog):
                    ev = download_state.get("cancel_event")
                    if ev:
                        ev.set()
                    dialog.destroy()
            else:
                dialog.destroy()

        dialog.protocol("WM_DELETE_WINDOW", on_close_dialog)

        btn_browser = None

        def set_btn_action_ui(mode: str, text: str, emoji_name: str, cmd):
            icon = self.get_icon(emoji_name)
            btn_txt = f" {text}" if icon else (f"{emoji_name} {text}" if sys.platform == "darwin" and emoji_name else text)
            if sys.platform == "darwin":
                style_name = "Success.TButton" if mode == "success" else ("Primary.TButton" if mode == "primary" else "TButton")
                btn_action.configure(text=btn_txt, image=icon or "", compound="left" if icon else "none", style=style_name, command=cmd)
            else:
                color_map = {
                    "success": ("#28a745", "#218838"),
                    "primary": ("#007bff", "#0069d9"),
                    "secondary": ("#6c757d", "#5a6268"),
                }
                bg_col, act_col = color_map.get(mode, ("#28a745", "#218838"))
                btn_action.configure(text=btn_txt, image=icon or "", compound="left" if icon else "none", bg=bg_col, activebackground=act_col, command=cmd)

        def start_download():
            if not download_url:
                open_browser()
                return

            if download_state["is_downloading"]:
                return

            dest_dir = update_manager.get_default_download_dir()
            filename = update_manager.get_asset_filename(download_url, fallback_version=info.latest_version)
            dest_file = dest_dir / filename

            if dest_file.is_file():
                if messagebox.askyesno(
                    "文件已存在",
                    f"检测到安装包已下载在以下路径：\n{dest_file}\n\n是否直接打开所在目录？\n（点击【否】将重新下载覆盖）",
                    parent=dialog,
                ):
                    on_download_finished(True, "文件已存在", dest_file)
                    return

            download_state["is_downloading"] = True
            cancel_ev = threading.Event()
            download_state["cancel_event"] = cancel_ev
            download_state["saved_path"] = None

            # Hide open-file button from any previous state
            btn_open_file.pack_forget()

            # Show progress bar
            f_progress.pack(side="bottom", fill="x", pady=(6, 2), before=f_btns)
            pb_download.configure(mode="determinate", value=0)
            var_prog_text.set("正在连接下载节点...")

            # Update buttons state
            set_btn_action_ui("secondary", "取消下载", "⏹️", cancel_download)
            btn_close.configure(state="disabled")
            if btn_browser:
                btn_browser.configure(state="disabled")

            up = None if self.var_direct_mode.get() else gbf_proxy.UPSTREAM_PROXY
            last_ui_update = [0.0]

            def progress_cb(downloaded: int, total: int, speed: float):
                now = time.perf_counter()
                if now - last_ui_update[0] >= 0.12 or (total > 0 and downloaded >= total):
                    last_ui_update[0] = now
                    try:
                        if dialog.winfo_exists():
                            dialog.after(0, update_ui_progress, downloaded, total, speed)
                    except Exception:
                        pass

            def update_ui_progress(downloaded: int, total: int, speed: float):
                if not dialog.winfo_exists() or not download_state["is_downloading"]:
                    return
                dl_mb = downloaded / (1024 * 1024)
                speed_str = f"{speed / (1024 * 1024):.1f} MB/s" if speed >= 1024 * 1024 else f"{speed / 1024:.0f} KB/s"
                if total > 0:
                    tot_mb = total / (1024 * 1024)
                    pct = min(100.0, (downloaded / total) * 100)
                    pb_download.configure(value=pct)
                    var_prog_text.set(f"已下载 {dl_mb:.1f} MB / {tot_mb:.1f} MB ({pct:.1f}%)  |  速度: {speed_str}")
                else:
                    pb_download.configure(mode="indeterminate")
                    var_prog_text.set(f"已下载 {dl_mb:.1f} MB  |  速度: {speed_str}")

            def on_download_finished(ok: bool, msg: str, result_path: Optional[Path]):
                download_state["is_downloading"] = False
                if not dialog.winfo_exists():
                    return
                btn_close.configure(text="关闭", state="normal")

                if ok and result_path:
                    download_state["saved_path"] = result_path
                    pb_download.configure(mode="determinate", value=100)
                    var_prog_text.set(f"✅ 下载完成！已保存至 Downloads 目录：{result_path.name}")
                    if btn_browser:
                        btn_browser.pack_forget()
                    set_btn_action_ui("primary", "打开所在文件夹", "📁", open_download_folder)
                    btn_open_file.pack(side="right", padx=(0, 6), before=btn_action)
                else:
                    pb_download.configure(mode="determinate", value=0)
                    btn_open_file.pack_forget()
                    if btn_browser and download_url:
                        btn_browser.configure(state="normal")
                        btn_browser.pack(side="right", padx=(0, 6), before=btn_action)
                    if "取消" in msg:
                        var_prog_text.set("下载已取消。")
                    else:
                        var_prog_text.set(f"❌ {msg}")
                    set_btn_action_ui("success", "立即下载更新", "🚀", start_download)

            def worker():
                ok, msg, path = update_manager.download_release_asset(
                    url=download_url,
                    dest_path=dest_file,
                    upstream_proxy=up,
                    progress_cb=progress_cb,
                    cancel_event=cancel_ev,
                )
                try:
                    if dialog.winfo_exists():
                        dialog.after(0, on_download_finished, ok, msg, path)
                except Exception:
                    pass

            threading.Thread(target=worker, daemon=True).start()

        def cancel_download():
            ev = download_state.get("cancel_event")
            if ev:
                ev.set()
            var_prog_text.set("正在取消下载...")
            btn_action.configure(state="disabled")
            dialog.after(500, lambda: btn_action.configure(state="normal") if dialog.winfo_exists() else None)

        action_text = " 立即下载更新" if download_url else " 前往 GitHub Releases"
        action_cmd = start_download if download_url else open_browser

        if sys.platform == "darwin":
            btn_action = ttk.Button(
                f_btns,
                text=f" {action_text}" if self.get_icon("🚀") else f"🚀 {action_text}",
                image=self.get_icon("🚀") or "",
                compound="left" if self.get_icon("🚀") else "none",
                style="Success.TButton",
                command=action_cmd,
            )
        else:
            btn_action = tk.Button(
                f_btns,
                text=action_text,
                image=self.get_icon("🚀"),
                compound="left",
                bg="#28a745",
                fg="#ffffff",
                activebackground="#218838",
                activeforeground="#ffffff",
                font=("Microsoft YaHei UI", 9, "bold"),
                relief="flat",
                padx=14,
                pady=6,
                cursor="hand2",
                command=action_cmd,
            )
        btn_action.pack(side="right")

        if sys.platform == "darwin":
            btn_open_file = ttk.Button(
                f_btns,
                text=f" 打开所在文件" if self.get_icon("📦") else "📦 打开所在文件",
                image=self.get_icon("📦") or "",
                compound="left" if self.get_icon("📦") else "none",
                style="Success.TButton",
                command=open_download_file,
            )
        else:
            btn_open_file = tk.Button(
                f_btns,
                text=" 打开所在文件",
                image=self.get_icon("📦"),
                compound="left",
                bg="#28a745",
                fg="#ffffff",
                activebackground="#218838",
                activeforeground="#ffffff",
                font=("Microsoft YaHei UI", 9, "bold"),
                relief="flat",
                padx=12,
                pady=6,
                cursor="hand2",
                command=open_download_file,
            )
        # Not packed initially, only packed upon download completion

        if download_url:
            btn_browser = ttk.Button(f_btns, text="浏览器下载", command=open_browser)
            btn_browser.pack(side="right", padx=(0, 6))

        btn_close = ttk.Button(f_btns, text="稍后再说", command=dialog.destroy)
        btn_close.pack(side="right", padx=(0, 6))

        btn_copy = ttk.Button(f_btns, text="复制下载链接", command=copy_link)
        btn_copy.pack(side="left")

        # 2. TOP BANNER
        f_top = ttk.Frame(content)
        f_top.pack(fill="x", pady=(0, 8))
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
            ).pack(anchor="w", pady=(3, 0))

        # Direct clickable download link
        f_link = ttk.Frame(f_top)
        f_link.pack(anchor="w", pady=(4, 0))
        ttk.Label(f_link, text="下载地址: ", style="Normal.TLabel").pack(side="left")
        lbl_link = tk.Label(
            f_link,
            text=target_url,
            font=("Microsoft YaHei UI", 9, "underline"),
            fg="#0066cc",
            cursor="hand2",
        )
        lbl_link.bind("<Button-1>", lambda e: webbrowser.open(target_url))
        lbl_link.pack(side="left")

        # 3. MIDDLE: Release notes text area (takes all remaining space)
        ttk.Label(content, text="更新内容：", style="Normal.TLabel").pack(anchor="w", pady=(0, 4))
        f_text = ttk.Frame(content)
        f_text.pack(fill="both", expand=True, pady=(0, 4))

        scrollbar = ttk.Scrollbar(f_text)
        scrollbar.pack(side="right", fill="y")

        txt_notes = tk.Text(
            f_text,
            wrap="word",
            height=8,
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

    def run_latency_test(self):
        """Measure real RTT to the game server through the current upstream route."""
        self.btn_latency.configure(state="disabled", text=" 测试中...")
        threading.Thread(target=self._bg_latency_test, daemon=True).start()

    def _bg_latency_test(self):
        import httpx

        target = "https://game.granbluefantasy.jp/"
        if self.var_direct_mode.get():
            proxy_url = None
            route_desc = "直连模式（不经过上游代理）"
        else:
            proxy_url = self.var_upstream.get().strip() or None
            route_desc = f"经上游代理 {proxy_url or '（未配置）'}"

        verify = False if gbf_proxy.SHIMAKAZE_MODE else bool(config_manager.config.get("verify_upstream_tls", True))
        cold_ms = None
        warm = []
        err = ""
        try:
            # Probe 1 (cold): includes proxy CONNECT + cross-sea TCP/TLS setup,
            # a one-time cost per connection. Probes 2-4 (warm): reuse the
            # keep-alive connection, representative of in-game API latency.
            with httpx.Client(proxy=proxy_url, verify=verify, timeout=12.0, trust_env=False, follow_redirects=False) as client:
                t0 = time.perf_counter()
                client.get(target)
                cold_ms = (time.perf_counter() - t0) * 1000
                for _ in range(3):
                    t0 = time.perf_counter()
                    client.get(target)
                    warm.append((time.perf_counter() - t0) * 1000)
        except Exception as e:
            err = str(e)

        def show():
            try:
                self.btn_latency.configure(state="normal", text=" 延迟测试")
            except Exception:
                pass
            if cold_ms is None and not warm:
                messagebox.showwarning("延迟测试失败", f"无法连通 {target}\n\n线路：{route_desc}\n错误：{err}")
                return
            lines = [
                f"目标：{target}",
                f"线路：{route_desc}",
                "",
                f"冷连接（建链 + 首个请求）：{cold_ms:.0f} ms" if cold_ms is not None else "冷连接：失败",
            ]
            if warm:
                warm.sort()
                w_min, w_mid, w_max = warm[0], warm[len(warm) // 2], warm[-1]
                lines.append(f"热连接 RTT（3 次）：最快 {w_min:.0f} ｜ 中位 {w_mid:.0f} ｜ 最慢 {w_max:.0f} ms")
            messagebox.showinfo("上游延迟测试结果", "\n".join(lines))

        try:
            self.root.after(0, show)
        except Exception:
            pass

    def install_ca(self):
        ensure_ca()
        if check_legacy_leaked_ca_installed():
            clean_legacy_leaked_ca()
        if is_ca_installed():
            if sys.platform == "darwin":
                messagebox.showinfo(
                    "根证书提示",
                    "本机专属根证书已在系统钥匙串受信任列表中！\n\n"
                    "【若 Chrome 仍提示证书不受信任】：\n"
                    "请在 Chrome 界面按快捷键 Cmd + Q 彻底退出浏览器，然后重新打开 Chrome 即可生效！\n\n"
                    "（Chrome 会将首次证书验证失败的结果缓存在内存中，直到彻底退出重启才会重新读取 macOS 钥匙串）",
                )
            else:
                messagebox.showinfo("根证书提示", "本机专属根证书已在系统的【受信任的根证书颁发机构】中，正常工作中！")
            return

        if sys.platform == "darwin":
            messagebox.showinfo(
                "安装根证书指引",
                "即将通过 macOS 钥匙串安装根证书。\n\n"
                "• 若弹出系统授权提示，请输入 Mac 密码允许信任；\n"
                "• 安装完成后，请彻底退出浏览器（按 Cmd+Q 退出 Chrome）并重新打开生效！",
            )
        else:
            messagebox.showinfo("安装指引", "即将调起 Windows 证书导入向导，若弹出系统安全提示框，请点击【是 (Y)】允许信任。")
        install_ca_certificate(CA_CERT_PATH)
        self.update_ca_status()
        if sys.platform == "darwin":
            messagebox.showinfo(
                "安装完成",
                "根证书已配置完成！\n\n"
                "【重要生效步骤】：\n"
                "请务必【完全退出 Chrome 浏览器】（在 Chrome 中按快捷键 Cmd + Q），然后重新打开 Chrome 访问游戏即可正常进入！",
            )

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
                "安装根证书后，加速器才能解密并缓存这些静态素材。\n\n"
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
            norm_p = normalize_cache_dir(p)
            self.var_cache_dir.set(str(norm_p))
            cache_manager.set_cache_base(norm_p)
            config_manager.config["cache_dir"] = str(norm_p)
            config_manager.save_config()
            was_running = gbf_proxy.PROXY_STATS.get("is_running", False)
            if was_running:
                self.stop_proxy()
                self.start_proxy()
            restart_tip = "\n\n代理服务已自动重启生效并刷新全部缓存！" if was_running else ""
            if norm_p != p:
                messagebox.showinfo(
                    "缓存设置",
                    f"检测到目录层级差异，已自动为您校正并锁定到真实缓存根目录：\n\n{norm_p}{restart_tip}"
                )
            else:
                messagebox.showinfo("缓存设置", f"缓存目录已成功更改为：\n{norm_p}{restart_tip}")

    def detect_acgp(self):
        found = auto_detect_acgpower_cache()
        if found:
            self.var_cache_dir.set(str(found))
            cache_manager.set_cache_base(found)
            config_manager.config["cache_dir"] = str(found)
            config_manager.save_config()
            was_running = gbf_proxy.PROXY_STATS.get("is_running", False)
            if was_running:
                self.stop_proxy()
                self.start_proxy()
            restart_tip = "\n\n代理服务已自动重启生效并刷新全部缓存！" if was_running else ""
            messagebox.showinfo("ACGP 探测成功", f"成功检测到 ACGPower 缓存目录！\n\n路径：{found}\n\n已自动关联，无需重新下载游戏静态资源！{restart_tip}")
        else:
            messagebox.showwarning(
                "探测结果",
                "在常用路径与后台运行进程中未找到现成的 ACGPower 缓存。\n\n"
                "• 若你使用了 ACGP，请确认 ACGP 是否已启动，或点击【浏览...】手动指定。\n"
                "• 若你未安装 ACGP，本程序已自动为你启用独立的本地缓存目录，可直接正常使用！"
            )

    def run_cache_audit(self):
        """Perform full format integrity health check and auto-repair on local cache."""
        if getattr(self, "_is_auditing_cache", False):
            messagebox.showwarning("提示", "缓存体检正在进行中，请稍候...")
            return

        if not messagebox.askyesno(
            "缓存体检与健康修复",
            "【一键体检缓存】将全量扫描本地静态缓存库，自动检测并修复：\n"
            "• 0 字节损坏空文件\n"
            "• 伪装成静态素材的 HTML 错误页面\n"
            "• 文件头损坏或被截断的异常文件（Magic Bytes 校验）\n\n"
            "检测到的损坏废件将被自动安全清理，方便游戏后续重新下载健康素材。\n\n"
            "是否立即开始体检？",
        ):
            return

        self._is_auditing_cache = True
        self.btn_audit_cache.configure(state="disabled")

        # Modal progress dialog
        dlg = tk.Toplevel(self.root)
        dlg.title("正在体检缓存...")
        dlg.transient(self.root)
        dlg.grab_set()
        dlg.resizable(False, False)

        frame = ttk.Frame(dlg, padding="20 16 20 16")
        frame.pack(fill="both", expand=True)

        ttk.Label(frame, text="🔍 正在全量体检本地静态缓存库...", font=("Microsoft YaHei UI", 10, "bold")).pack(anchor="w", pady=(0, 6))

        var_progress_text = tk.StringVar(value="正在扫描文件结构，请稍候...")
        lbl_info = ttk.Label(frame, textvariable=var_progress_text, style="Normal.TLabel")
        lbl_info.pack(anchor="w", pady=(0, 10))

        pb = ttk.Progressbar(frame, mode="indeterminate", length=360)
        pb.pack(fill="x", pady=(0, 12))
        pb.start(15)

        dlg.update_idletasks()
        pw, ph = self.root.winfo_width(), self.root.winfo_height()
        px, py = self.root.winfo_rootx(), self.root.winfo_rooty()
        dw, dh = dlg.winfo_width(), dlg.winfo_height()
        dlg.geometry(f"+{px + max(0, (pw - dw) // 2)}+{py + max(0, (ph - dh) // 2)}")

        def worker():
            def on_progress(scanned: int, corrupted: int):
                self.root.after(0, lambda: var_progress_text.set(
                    f"已扫描素材：{scanned:,} 个  |  已修复异常：{corrupted:,} 个"
                ))

            try:
                res = cache_manager.audit_and_repair_cache(progress_callback=on_progress)
            except Exception as e:
                res = {"error": str(e)}

            def on_done():
                pb.stop()
                dlg.destroy()
                self._is_auditing_cache = False
                self.btn_audit_cache.configure(state="normal")

                if "error" in res:
                    messagebox.showerror("体检失败", f"体检过程中发生错误：\n{res['error']}")
                    return

                scanned = res.get("scanned", 0)
                healthy = res.get("healthy", 0)
                corrupted = res.get("corrupted", 0)
                elapsed = res.get("elapsed", 0.0)

                if corrupted > 0:
                    msg = (
                        f"🎉 缓存体检与修复已完成！\n\n"
                        f"📁 扫描素材总数：{scanned:,} 个\n"
                        f"✅ 格式健康素材：{healthy:,} 个\n"
                        f"🛠️ 发现并修复损坏：{corrupted:,} 个\n"
                        f"⏱️ 耗时：{elapsed} 秒\n\n"
                        f"已自动清理损坏、截断与 HTML 报错垃圾文件，游戏后续将自动拉取健康素材。"
                    )
                else:
                    msg = (
                        f"🎉 缓存体检完成！\n\n"
                        f"📁 扫描素材总数：{scanned:,} 个\n"
                        f"✅ 格式健康素材：{healthy:,} 个\n"
                        f"🛡️ 损坏/截断文件：0 个（全部 100% 格式健康！）\n"
                        f"⏱️ 耗时：{elapsed} 秒\n\n"
                        f"本地所有图片、音频与动画文件均符合官方规范，未发现任何损坏。"
                    )
                messagebox.showinfo("缓存体检结果", msg)

            self.root.after(0, on_done)

        t = threading.Thread(target=worker, daemon=True)
        t.start()

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
                    "检测到上游代理为岛风GO (8099)。\n\n是否立即启用【岛风GO 兼容优化模式】？\n\n（将自动放宽超时、启用断线自愈重试、适配自签证书；日常可按需开启远端缓存加速首刷）",
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
        if cd:
            p = Path(cd).resolve()
            norm_p = normalize_cache_dir(p)
            cd = str(norm_p)
            self.var_cache_dir.set(cd)
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
        old_allow_lan = bool(config_manager.config.get("allow_lan", False))
        old_cache_dir = str(cache_manager.cache_base.resolve())
        port_changed = (port != old_port)
        upstream_changed = (up != old_up)
        direct_changed = (self.var_direct_mode.get() != old_direct)
        shimakaze_changed = (self.var_shimakaze_mode.get() != old_shimakaze)
        allow_lan_changed = (self.var_allow_lan.get() != old_allow_lan)
        cache_dir_changed = bool(cd and str(Path(cd).resolve()) != old_cache_dir)

        # Check connectivity to upstream
        up_ok, up_msg = check_upstream_connectivity(up)
        if not up_ok and not self.var_direct_mode.get():
            if not messagebox.askyesno("上游代理连通警告", f"测试连接上游代理失败：\n{up_msg}\n\n是否仍然保存该代理地址？"):
                return

        config_manager.config["upstream_proxy"] = up
        config_manager.config["direct_mode"] = self.var_direct_mode.get()
        config_manager.config["shimakaze_mode"] = self.var_shimakaze_mode.get()
        config_manager.config["allow_lan"] = self.var_allow_lan.get()
        config_manager.config["cache_dir"] = cd
        config_manager.config["listen_port"] = port
        config_manager.config["auto_system_proxy"] = self.var_auto_pac.get()
        config_manager.config["enable_ram_cache"] = self.var_ram_cache.get()
        config_manager.config["enable_browser_cache"] = self.var_browser_cache.get()
        config_manager.config["enable_auto_repair"] = self.var_auto_repair.get()
        config_manager.config["enable_prefetch"] = self.var_prefetch.get()
        config_manager.config["enable_ram_warmup"] = self.var_ram_warmup.get()
        config_manager.save_config()

        if not self.var_ram_cache.get():
            cache_manager.clear_ram_cache()

        gbf_proxy.UPSTREAM_PROXY = up
        gbf_proxy.DIRECT_MODE = self.var_direct_mode.get()
        gbf_proxy.SHIMAKAZE_MODE = self.var_shimakaze_mode.get()
        if cd:
            cache_manager.set_cache_base(Path(cd).resolve())

        self.update_lan_status_label()

        # Update local proxy.pac file
        from app_main import update_pac_file
        update_pac_file(port)

        if (port_changed or upstream_changed or direct_changed or shimakaze_changed or allow_lan_changed or cache_dir_changed) and gbf_proxy.PROXY_STATS.get("is_running", False):
            self.stop_proxy()
            gbf_proxy.LISTEN_HOST = config_manager.get_effective_listen_host()
            gbf_proxy.LISTEN_PORT = port
            gbf_proxy.UPSTREAM_PROXY = up
            self.start_proxy()
            messagebox.showinfo("保存成功", f"配置已保存！\n代理服务已自动重启生效（缓存目录：{cache_manager.cache_base}）。")
        else:
            gbf_proxy.LISTEN_HOST = config_manager.get_effective_listen_host()
            gbf_proxy.LISTEN_PORT = port
            gbf_proxy.UPSTREAM_PROXY = up
            messagebox.showinfo("保存成功", "配置已保存成功！")

    def toggle_perf_settings(self):
        config_manager.config["enable_ram_cache"] = self.var_ram_cache.get()
        config_manager.config["enable_browser_cache"] = self.var_browser_cache.get()
        config_manager.config["enable_auto_repair"] = self.var_auto_repair.get()
        config_manager.config["enable_prefetch"] = self.var_prefetch.get()
        config_manager.config["enable_ram_warmup"] = self.var_ram_warmup.get()
        config_manager.save_config()
        if not self.var_ram_cache.get():
            cache_manager.clear_ram_cache()

    def apply_ram_max_mb(self):
        """Persist and immediately enforce a new RAM cache cap entered in the GUI."""
        raw = self.var_ram_max_mb.get().strip()
        try:
            mb = int(raw)
            if not (16 <= mb <= 8192):
                raise ValueError
        except ValueError:
            messagebox.showerror("错误", "内存缓存上限必须是 16 到 8192 之间的整数 (MB)！")
            return
        config_manager.config["ram_cache_max_mb"] = mb
        config_manager.save_config()
        cache_manager.enforce_ram_limit()
        self.refresh_ram_usage_label()
        messagebox.showinfo("内存缓存", f"内存热点缓存上限已设置为 {mb} MB，并立即生效。")

    def refresh_ram_usage_label(self):
        try:
            if not hasattr(self, "lbl_ram_usage") or not self.lbl_ram_usage.winfo_exists():
                return
            _items, used = cache_manager.get_ram_cache_stats()
            cap = config_manager.config.get("ram_cache_max_mb", 256)
            self.lbl_ram_usage.configure(text=f"使用中 {used / (1024 * 1024):.0f} MB / {cap} MB")
        except Exception:
            pass

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

    def update_lan_status_label(self):
        if not hasattr(self, "lbl_lan_status") or not self.lbl_lan_status.winfo_exists():
            return
        if self.var_allow_lan.get():
            lan_ip = config_manager.get_lan_ip()
            port = self.var_listen_port.get().strip() or "8124"
            self.lbl_lan_status.configure(
                text=f"局域网代理就绪：http://{lan_ip}:{port}  |  PAC 脚本：http://{lan_ip}:{port}/proxy.pac",
            )
            self.lbl_lan_status.pack(anchor="w", pady=(0, 4), after=self.chk_allow_lan.master)
        else:
            self.lbl_lan_status.pack_forget()

    def toggle_allow_lan_setting(self):
        val = self.var_allow_lan.get()
        config_manager.config["allow_lan"] = val
        config_manager.save_config()
        self.update_lan_status_label()
        if gbf_proxy.PROXY_STATS.get("is_running", False):
            self.stop_proxy()
            self.start_proxy()

    def show_lan_guide(self):
        self.update_lan_status_label()
        lan_ip = config_manager.get_lan_ip()
        port = self.var_listen_port.get().strip() or "8124"
        pac_url = f"http://{lan_ip}:{port}/proxy.pac"
        ca_url = f"http://{lan_ip}:{port}/ca.crt"
        guide_url = f"http://{lan_ip}:{port}/"

        dialog = tk.Toplevel(self.root)
        dialog.title("移动端 / iOS 设备连接与分流指引")
        dialog.geometry("660x640")
        dialog.minsize(600, 540)
        dialog.transient(self.root)
        dialog.grab_set()

        # Center relative to parent
        self.root.update_idletasks()
        dialog.update_idletasks()
        rx = self.root.winfo_x()
        ry = self.root.winfo_y()
        rw = self.root.winfo_width()
        rh = self.root.winfo_height()
        x = max(0, rx + (rw - 660) // 2)
        y = max(0, ry + (rh - 640) // 2)
        dialog.geometry(f"+{x}+{y}")

        content = ttk.Frame(dialog, padding="16 14 16 14")
        content.pack(fill="both", expand=True)

        # Header
        f_top = ttk.Frame(content)
        f_top.pack(fill="x", pady=(0, 8))
        ttk.Label(
            f_top,
            text="移动端 / iOS 设备接入配置指引",
            font=("Microsoft YaHei UI", 11, "bold"),
            foreground="#007bff",
        ).pack(anchor="w")

        # LAN IP & Status display
        f_status_box = ttk.Frame(content, relief="solid", borderwidth=1, padding="10 8 10 8")
        f_status_box.pack(fill="x", pady=(0, 10))

        status_text = f"电脑局域网 IP：{lan_ip}    监听端口：{port}"
        if not self.var_allow_lan.get():
            status_text += "\n注意：当前尚未开启【允许局域网连接】，外部设备暂无法连接本代理。"
            lbl_st = ttk.Label(f_status_box, text=status_text, font=("Microsoft YaHei UI", 9), foreground="#dc3545")
            lbl_st.pack(anchor="w")

            def enable_now():
                self.var_allow_lan.set(True)
                self.toggle_allow_lan_setting()
                lbl_st.configure(
                    text=f"电脑局域网 IP：{lan_ip}    监听端口：{port}\n已开启局域网连接。",
                    foreground="#28a745",
                )
                btn_enb.pack_forget()

            btn_enb = ttk.Button(f_status_box, text="开启局域网连接", command=enable_now)
            btn_enb.pack(anchor="w", pady=(4, 0))
        else:
            status_text += "\n局域网服务已就绪（支持同一 Wi-Fi 下的 iPhone / iPad / Android 设备）。"
            ttk.Label(f_status_box, text=status_text, font=("Microsoft YaHei UI", 9), foreground="#28a745").pack(anchor="w")

        # Instructions scrollable text area
        f_steps = ttk.Frame(content)
        f_steps.pack(fill="both", expand=True, pady=(0, 10))

        scrollbar = ttk.Scrollbar(f_steps)
        scrollbar.pack(side="right", fill="y")

        txt = tk.Text(
            f_steps,
            wrap="word",
            font=("Microsoft YaHei UI", 9),
            yscrollcommand=scrollbar.set,
            bg="#fdfdfd",
            relief="solid",
            bd=1,
            padx=10,
            pady=8,
        )
        txt.pack(fill="both", expand=True)
        scrollbar.config(command=txt.yview)

        guide_text = f"""【第一步：安装并信任根证书（iOS 必需，Android 视情况）】
1. 确保手机与电脑连接在同一个 Wi-Fi 局域网下。
2. 手机 Safari 访问：{ca_url}
   （或访问 {guide_url} 查看网页版指引）
3. 提示时点击【允许】下载描述文件。
4. 打开手机系统【设置】->【已下载描述文件】-> 点击【安装】。
5. 系统信任证书：
   打开手机【设置】->【通用】->【关于本机】-> 底部【证书信任设置】；
   找到【GBF Local Accelerator Root CA】，打开信任开关。

───────────────────────────────────────
【第二步：配置手机 Wi-Fi 代理】
1. 打开手机系统【设置】->【无线局域网 (Wi-Fi)】。
2. 点击当前已连接 Wi-Fi 右侧的 ⓘ 图标。
3. 滑动到底部，点击【配置代理】：

方式 1：自动分流（推荐，仅游戏素材走代理）
   • 选择【自动】
   • URL 填入：{pac_url}
   • 存储。

方式 2：手动代理
   • 选择【手动】
   • 服务器填入：{lan_ip}
   • 端口填入：{port}
   • 存储。

───────────────────────────────────────
【常见问题】
• 游玩网址与客户端说明：
  建议使用手机浏览器（Safari / Chrome）直接访问：
  https://game.granbluefantasy.jp
  说明：SkyLeap 内置使用的是 gbf.game.mbga.jp，该地址主要用于账号登录和跳转，不包含游戏静态素材，无法触发本地缓存加速；在手机浏览器中访问 game.granbluefantasy.jp 才能正常走本地缓存。
• 手机打不开网页或提示连接超时？
  请检查电脑防火墙是否放行端口 {port}，并确认手机和电脑在同一个 Wi-Fi 网络。
• iOS 提示证书不受信任或白屏？
  请检查【关于本机】->【证书信任设置】中的完全信任开关是否已开启。
• 局域网 IP 变动？
  若电脑 IP 变化，请在此处查看最新 IP 并更新手机 Wi-Fi 代理设置。"""

        txt.insert("1.0", guide_text)
        txt.configure(state="disabled")

        # Bottom buttons
        f_actions = ttk.Frame(content)
        f_actions.pack(fill="x")

        def copy_pac():
            self.root.clipboard_clear()
            self.root.clipboard_append(pac_url)
            messagebox.showinfo("提示", f"PAC 脚本地址已复制：\n\n{pac_url}", parent=dialog)

        def copy_ca():
            self.root.clipboard_clear()
            self.root.clipboard_append(ca_url)
            messagebox.showinfo("提示", f"根证书下载地址已复制：\n\n{ca_url}", parent=dialog)

        def open_web():
            webbrowser.open(guide_url)

        ttk.Button(f_actions, text="复制 PAC 地址", command=copy_pac).pack(side="left", padx=(0, 6))
        ttk.Button(f_actions, text="复制证书地址", command=copy_ca).pack(side="left", padx=(0, 6))
        ttk.Button(f_actions, text="浏览器打开指引", command=open_web).pack(side="left")
        ttk.Button(f_actions, text="关闭", width=8, command=dialog.destroy).pack(side="right")

    def open_cache_folder(self):
        p = Path(self.var_cache_dir.get()).resolve()
        p.mkdir(parents=True, exist_ok=True)
        open_path(p)

    def show_guide(self):
        base_dir = get_base_dir()
        readme = base_dir / "使用说明.txt"
        if readme.is_file():
            open_path(readme)
        else:
            messagebox.showinfo("分流指引", "请使用 ZeroOmega / SwitchyOmega 导入同目录下的 SwitchyOmega_GBF.bak，或直接勾选【自动配置系统 PAC 代理】实现免插件分流。")

    def set_toggle_button_state(self, is_running: bool):
        """Update toggle button text and color across platforms."""
        if sys.platform == "darwin":
            if is_running:
                self.btn_toggle.configure(text="停止加速", style="Danger.TButton")
            else:
                self.btn_toggle.configure(text="启动加速", style="Success.TButton")
        else:
            if is_running:
                self.btn_toggle.configure(text="停止加速", bg="#dc3545", activebackground="#bd2130")
            else:
                self.btn_toggle.configure(text="启动加速", bg="#28a745", activebackground="#218838")

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

        gbf_proxy.LISTEN_HOST = config_manager.get_effective_listen_host()
        gbf_proxy.LISTEN_PORT = port
        gbf_proxy.UPSTREAM_PROXY = up
        gbf_proxy.DIRECT_MODE = self.var_direct_mode.get()
        gbf_proxy.SHIMAKAZE_MODE = self.var_shimakaze_mode.get()
        config_manager.config["listen_port"] = port
        config_manager.config["allow_lan"] = self.var_allow_lan.get()
        config_manager.config["upstream_proxy"] = up
        config_manager.config["direct_mode"] = self.var_direct_mode.get()
        config_manager.config["shimakaze_mode"] = self.var_shimakaze_mode.get()
        config_manager.config["enable_ram_cache"] = self.var_ram_cache.get()
        config_manager.config["enable_browser_cache"] = self.var_browser_cache.get()
        config_manager.config["enable_auto_repair"] = self.var_auto_repair.get()
        config_manager.config["enable_prefetch"] = self.var_prefetch.get()
        config_manager.config["enable_ram_warmup"] = self.var_ram_warmup.get()
        cd_raw = self.var_cache_dir.get().strip()
        norm_cd = normalize_cache_dir(cd_raw)
        self.var_cache_dir.set(str(norm_cd))
        config_manager.config["cache_dir"] = str(norm_cd)
        config_manager.save_config()

        cache_manager.set_cache_base(norm_cd)

        # Update local proxy.pac file
        from app_main import update_pac_file
        update_pac_file(port)

        gbf_proxy.start_proxy_thread()

        # Wait for actual socket bind success (up to 5.0 seconds primary window)
        is_ready = gbf_proxy.proxy_ready_event.wait(timeout=5.0)
        is_running = gbf_proxy.PROXY_STATS.get("is_running", False)

        # Grace convergence window: if not ready, no explicit error, and background
        # thread is still actively initializing, provide a brief convergence window (0.8s)
        # to avoid misclassifying slow startup / scheduler delay as a failure.
        if not (is_ready and is_running):
            last_err = gbf_proxy.PROXY_STATS.get("last_error", "")
            thread_alive = gbf_proxy.proxy_thread and gbf_proxy.proxy_thread.is_alive()
            if not last_err and thread_alive:
                gbf_proxy.proxy_ready_event.wait(timeout=0.8)
                is_running = gbf_proxy.PROXY_STATS.get("is_running", False)

        # Final state confirmation
        if is_running:
            if self.var_auto_pac.get():
                system_proxy.enable_pac_proxy(f"http://127.0.0.1:{gbf_proxy.LISTEN_PORT}/proxy.pac")

            self.var_status_text.set(f"● 运行中 (监听端口 {gbf_proxy.LISTEN_PORT})")
            self.lbl_status.configure(foreground="#28a745")
            self.set_toggle_button_state(True)
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(True)
        else:
            err = gbf_proxy.PROXY_STATS.get("last_error") or "端口绑定失败或超时"
            # Clean up background thread so state is consistently stopped
            gbf_proxy.stop_proxy_thread()
            self.var_status_text.set(f"● 启动失败: {err[:20]}")
            self.lbl_status.configure(foreground="#dc3545")
            self.set_toggle_button_state(False)
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(False)
            messagebox.showerror("启动失败", f"代理服务无法在端口 {port} 启动：\n{err}\n\n请尝试更换端口或检查是否有其他程序占用。")

    def stop_proxy(self):
        gbf_proxy.stop_proxy_thread()
        if self.var_auto_pac.get():
            system_proxy.disable_pac_proxy()
        self.var_status_text.set("● 服务已停止")
        self.lbl_status.configure(foreground="#6c757d")
        self.set_toggle_button_state(False)
        if self.tray_icon:
            self.tray_icon.icon = create_tray_icon_image(False)

    def _on_window_map(self, event):
        """Resume stats loop immediately when window is un-minimized or restored."""
        if event.widget == self.root and self.root.state() == "normal":
            if getattr(self, "_stats_job", None) is None:
                self.update_stats_loop()

    def update_stats_loop(self):
        # Suspend loop when window is withdrawn to tray or minimized to taskbar (0.0% CPU in background)
        if self.root.state() in ("withdrawn", "iconic"):
            self._stats_job = None
            return

        # Update numbers from PROXY_STATS with dirty check to avoid redundant widget redraws
        hits = gbf_proxy.PROXY_STATS.get("hits", 0)
        ram_hits = gbf_proxy.PROXY_STATS.get("ram_hits", 0)
        dls = gbf_proxy.PROXY_STATS.get("downloads", 0)
        apis = gbf_proxy.PROXY_STATS.get("apis", 0)
        current_stats = (hits, ram_hits, dls, apis)

        if getattr(self, "_last_stats_cache", None) != current_stats:
            self._last_stats_cache = current_stats
            if ram_hits > 0:
                self.var_hits.set(f"{hits:,} (内存 {ram_hits:,})")
            else:
                self.var_hits.set(f"{hits:,}")
            self.var_downloads.set(f"{dls:,}")
            self.var_apis.set(f"{apis:,}")
            # Live RAM cache usage next to the cap entry
            self.refresh_ram_usage_label()

        # Sync button text if state changed outside
        is_thread_alive = gbf_proxy.proxy_thread is not None and gbf_proxy.proxy_thread.is_alive()
        is_running = gbf_proxy.PROXY_STATS.get("is_running", False) or is_thread_alive
        last_error = gbf_proxy.PROXY_STATS.get("last_error", "")

        if is_running and "停止" not in self.btn_toggle.cget("text"):
            self.set_toggle_button_state(True)
            self.var_status_text.set(f"● 运行中 (监听端口 {gbf_proxy.LISTEN_PORT})")
            self.lbl_status.configure(foreground="#28a745")
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(True)
        elif not is_running and "启动" not in self.btn_toggle.cget("text"):
            self.set_toggle_button_state(False)
            if last_error:
                self.var_status_text.set(f"● 异常停止: {last_error[:25]}")
                self.lbl_status.configure(foreground="#dc3545")
            else:
                self.var_status_text.set("● 服务已停止")
                self.lbl_status.configure(foreground="#6c757d")
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(False)

        # Schedule next update
        self._stats_job = self.root.after(800, self.update_stats_loop)

    # ================= System Tray / Menu Bar =================
    def setup_tray(self):
        if sys.platform == "darwin":
            self.tray_icon = MacStatusBarManager(self)
            return

        try:
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
        except Exception:
            self.tray_icon = None

    def hide_to_tray(self, notify=True):
        if self.tray_icon or sys.platform == "darwin":
            self.root.withdraw()
            if sys.platform == "darwin":
                try:
                    import AppKit
                    AppKit.NSApplication.sharedApplication().setActivationPolicy_(
                        AppKit.NSApplicationActivationPolicyAccessory
                    )
                except Exception:
                    pass
            # Cancel running stats timer so CPU stays at absolute 0.0% in background
            if getattr(self, "_stats_job", None):
                try:
                    self.root.after_cancel(self._stats_job)
                except Exception:
                    pass
                self._stats_job = None
            if notify and self.tray_icon:
                try:
                    msg = "GBF 加速代理已最小化到顶部菜单栏，正在后台运行。" if sys.platform == "darwin" else "GBF 加速代理已最小化到系统托盘，正在后台运行。"
                    self.tray_icon.notify(msg, "GBF 加速代理")
                except Exception:
                    pass
        else:
            self.root.iconify()

    def toggle_from_tray(self, *args):
        """Toggle main window between hidden in menu bar and visible in foreground."""
        try:
            if self.root.state() in ("withdrawn", "iconic"):
                self.show_from_tray()
            else:
                self.hide_to_tray(notify=False)
        except Exception:
            self.show_from_tray()

    def show_from_tray(self, *args):
        try:
            if sys.platform == "darwin":
                try:
                    import AppKit
                    AppKit.NSApplication.sharedApplication().setActivationPolicy_(
                        AppKit.NSApplicationActivationPolicyRegular
                    )
                except Exception:
                    pass
            self.root.deiconify()
            self.root.lift()
            if sys.platform == "darwin":
                try:
                    import AppKit
                    AppKit.NSApplication.sharedApplication().activateIgnoringOtherApps_(True)
                    self.setup_mac_window_buttons()
                except Exception:
                    pass
            self.root.attributes("-topmost", True)
            self.root.after_idle(self.root.attributes, "-topmost", False)
            self.root.focus_force()
            # Immediately refresh stats and resume loop upon showing
            if getattr(self, "_stats_job", None) is None:
                self.update_stats_loop()
        except Exception:
            pass

    def toggle_proxy_from_tray(self, *args):
        try:
            self.root.after(0, self.toggle_proxy)
        except Exception:
            self.toggle_proxy()

    def show_log_window(self):
        if self.log_window is not None:
            try:
                if self.log_window.top.winfo_exists():
                    self.log_window.top.deiconify()
                    self.log_window.top.lift()
                    self.log_window.top.focus_force()
                    return
            except Exception:
                pass
        self.log_window = LogViewerWindow(self)

    def quit_app(self, *args, terminate_process: bool = True, **kwargs):
        """Cleanly and completely shut down proxy, tray icon, and all application processes."""
        if getattr(self, "_is_quitting", False):
            return
        self._is_quitting = True

        # 1. Close log viewer if open
        if self.log_window is not None:
            try:
                self.log_window.on_close()
            except Exception:
                pass
            self.log_window = None

        # 2. Restore system network settings and stop proxy background threads
        try:
            system_proxy.disable_pac_proxy()
        except Exception:
            pass
        try:
            gbf_proxy.stop_proxy_thread()
        except Exception:
            pass

        # 3. Cancel any pending Tk periodic timers
        if getattr(self, "_stats_job", None):
            try:
                self.root.after_cancel(self._stats_job)
            except Exception:
                pass
            self._stats_job = None

        # 4. Stop tray icon (immediately removes icon from Windows notification tray)
        tray = getattr(self, "tray_icon", None)
        if tray is not None:
            try:
                tray.stop()
            except Exception:
                pass
            self.tray_icon = None

        # 5. Destroy Tkinter root window and break mainloop
        def _destroy_root():
            try:
                self.root.quit()
            except Exception:
                pass
            try:
                self.root.destroy()
            except Exception:
                pass

        try:
            self.root.after(0, _destroy_root)
        except Exception:
            _destroy_root()

        # 6. Safety watchdog: ensure process terminates completely
        if terminate_process:
            def _hard_exit():
                time.sleep(0.3)
                os._exit(0)

            threading.Thread(target=_hard_exit, daemon=True).start()

class LogViewerWindow:
    """Non-modal, high-performance real-time proxy and network log viewer."""
    def __init__(self, parent: GBFAcceleratorGUI):
        self.parent = parent
        self.top = tk.Toplevel(parent.root)
        self.top.title("实时网络与转发日志 (Live Logs) - GBF 加速器")
        try:
            self.top.iconphoto(True, parent.window_icon)
        except Exception:
            pass

        # Adaptive window geometry
        # Adaptive window geometry (wide layout to ensure all toolbar buttons are fully visible)
        sw = self.top.winfo_screenwidth()
        sh = self.top.winfo_screenheight()
        w = min(1280, max(1060, sw - 60))
        h = min(720, max(520, sh - 100))
        self.top.geometry(f"{w}x{h}")
        self.top.minsize(960, 400)

        # Position slightly offset from parent
        try:
            px = parent.root.winfo_x()
            py = parent.root.winfo_y()
            self.top.geometry(f"+{max(10, px + 30)}+{max(10, py + 30)}")
        except Exception:
            pass

        self.top.protocol("WM_DELETE_WINDOW", self.on_close)

        # Thread-safe log queue and storage (worker thread only touches these pure Python queues)
        self._incoming_queue: collections.deque = collections.deque()
        self._queue_lock: threading.Lock = threading.Lock()
        self._all_records: collections.deque = collections.deque(maxlen=2500)
        self._max_records: int = 2500
        self._is_closed: bool = False
        self._is_paused: bool = False
        self._scheduled_job = None
        self._last_stats_tick: float = 0.0
        self._context_line: str = ""
        self._log_font_size: int = 10 if sys.platform == "darwin" else 9

        self.var_auto_scroll = tk.BooleanVar(value=True)
        self.var_paused = tk.BooleanVar(value=False)
        self.var_hide_connect = tk.BooleanVar(value=False)       # 默认显示全部连接，避免直接复制时缺失
        self.var_compact_domain = tk.BooleanVar(value=False)     # 默认显示完整域名，不省略遮蔽
        self.var_mute_assets = tk.BooleanVar(value=False)        # 默认显示素材
        self.var_hide_mocks = tk.BooleanVar(value=False)         # 默认显示打点
        self.var_align_format = tk.BooleanVar(value=True)        # 默认开启对齐排版
        self.var_filter_text = tk.StringVar(value="")
        self.var_filter_cat = tk.StringVar(value="全部 (All)")
        self.var_status = tk.StringVar(value="初始化中...")

        self.setup_ui()
        self.setup_tags()

        # Keyboard shortcuts on top window
        self.top.bind("<Control-f>", self._on_find)
        self.top.bind("<Control-F>", self._on_find)
        self.top.bind("<Escape>", self._on_escape)
        self.top.bind("<space>", self._on_space)
        self.top.bind("<Control-MouseWheel>", self._on_mousewheel_zoom)

        # Atomically register live listener and fetch existing history snapshot
        history = gbf_proxy.register_log_listener_with_history(self.on_live_log)
        with self._queue_lock:
            for line, lvl in history:
                self._all_records.append((line, lvl))
                self._incoming_queue.append((line, lvl))

        # Main GUI thread polling loop: NEVER call after() from worker threads
        self._poll_update()

    def setup_ui(self):
        # 1. Top Toolbar
        toolbar = ttk.Frame(self.top, padding="6 5 6 5")
        toolbar.pack(fill="x", side="top")

        # Right-side action buttons: pack FIRST so they are ALWAYS visible at the right edge
        btn_export = ttk.Button(toolbar, text="导出日志", width=8, command=self.export_logs)
        btn_export.pack(side="right", padx=(2, 0))

        btn_copy = ttk.Button(toolbar, text="复制全部", width=8, command=self.copy_all)
        btn_copy.pack(side="right", padx=(2, 0))

        btn_clear = ttk.Button(toolbar, text="清除显示", width=8, command=self.clear_display)
        btn_clear.pack(side="right", padx=(2, 0))

        self.btn_pause = ttk.Button(toolbar, text="⏸ 暂停", width=8, command=self.toggle_pause)
        self.btn_pause.pack(side="right", padx=(2, 8))

        # Left-side search and filters
        ttk.Label(toolbar, text="搜索:").pack(side="left", padx=(0, 2))
        self.entry_filter = ttk.Entry(toolbar, textvariable=self.var_filter_text, width=12, font=("Microsoft YaHei UI", 9))
        self.entry_filter.pack(side="left", padx=(0, 6))
        self.entry_filter.bind("<KeyRelease>", lambda e: self.reapply_filter())

        ttk.Label(toolbar, text="类型:").pack(side="left", padx=(0, 2))
        combo_cat = ttk.Combobox(toolbar, textvariable=self.var_filter_cat, values=[
            "全部 (All)",
            "仅战斗 API (BYPASS-API)",
            "仅缓存命中 (CACHE)",
            "仅素材拉取 (FETCH/PREFETCH)",
            "仅慢请求 (>200ms)",
            "仅重连与告警 (RETRY/ERROR)",
        ], state="readonly", width=15, font=("Microsoft YaHei UI", 9))
        combo_cat.pack(side="left", padx=(0, 6))
        combo_cat.bind("<<ComboboxSelected>>", lambda e: self.reapply_filter())

        chk_conn = ttk.Checkbutton(toolbar, text="隐藏底层握手", variable=self.var_hide_connect, command=self.reapply_filter)
        chk_conn.pack(side="left", padx=(0, 4))

        chk_compact = ttk.Checkbutton(toolbar, text="简化域名", variable=self.var_compact_domain, command=self.reapply_filter)
        chk_compact.pack(side="left", padx=(0, 4))

        chk_mute = ttk.Checkbutton(toolbar, text="隐藏静态资源", variable=self.var_mute_assets, command=self.reapply_filter)
        chk_mute.pack(side="left", padx=(0, 4))

        chk_mock = ttk.Checkbutton(toolbar, text="隐藏数据上报", variable=self.var_hide_mocks, command=self.reapply_filter)
        chk_mock.pack(side="left", padx=(0, 4))

        chk_align = ttk.Checkbutton(toolbar, text="对齐排版", variable=self.var_align_format, command=self.reapply_filter)
        chk_align.pack(side="left", padx=(0, 4))

        chk_scroll = ttk.Checkbutton(toolbar, text="自动滚屏", variable=self.var_auto_scroll)
        chk_scroll.pack(side="left", padx=(0, 4))

        # 2. Main Console Text with horizontal & vertical scrollbars
        f_body = ttk.Frame(self.top)
        f_body.pack(fill="both", expand=True, padx=8, pady=(0, 4))

        scroll_y = ttk.Scrollbar(f_body, orient="vertical")
        scroll_y.pack(side="right", fill="y")
        scroll_x = ttk.Scrollbar(f_body, orient="horizontal")
        scroll_x.pack(side="bottom", fill="x")

        self.txt_logs = tk.Text(
            f_body,
            bg="#181818",
            fg="#cccccc",
            insertbackground="#ffffff",
            selectbackground="#264f78",
            selectforeground="#ffffff",
            font=(MONO_FONT, self._log_font_size),
            wrap="none",
            xscrollcommand=scroll_x.set,
            yscrollcommand=scroll_y.set,
            state="disabled",
            relief="flat",
        )
        self.txt_logs.pack(side="left", fill="both", expand=True)
        scroll_y.config(command=self.txt_logs.yview)
        scroll_x.config(command=self.txt_logs.xview)

        # Right-click context menu
        self.context_menu = tk.Menu(self.top, tearoff=0)
        self.context_menu.add_command(label="📋 复制当前显示行", command=self._copy_selected_line)
        self.context_menu.add_command(label="📜 复制原始完整记录 (含完整域名与参数)", command=self._copy_raw_line)
        self.context_menu.add_command(label="🔗 复制完整 URL", command=self._copy_selected_url)
        self.context_menu.add_command(label="🔍 仅筛选此路径", command=self._filter_selected_path)
        self.context_menu.add_separator()
        self.context_menu.add_command(label="💾 导出全量排查诊断日志...", command=self.export_logs)
        self.context_menu.add_command(label="📄 仅导出当前屏幕显示...", command=self.export_visible_logs)
        self.context_menu.add_command(label="🧹 清除当前显示", command=self.clear_display)

        self.txt_logs.bind("<Button-3>", self._on_right_click)
        self.txt_logs.bind("<Control-f>", self._on_find)
        self.txt_logs.bind("<Control-F>", self._on_find)
        self.txt_logs.bind("<Escape>", self._on_escape)
        self.txt_logs.bind("<space>", self._on_space)
        self.txt_logs.bind("<Control-MouseWheel>", self._on_mousewheel_zoom)

        # 3. Bottom Status Bar
        f_status = ttk.Frame(self.top, padding="8 2 8 4")
        f_status.pack(fill="x", side="bottom")
        self.lbl_status = ttk.Label(f_status, textvariable=self.var_status, style="Gray.TLabel")
        self.lbl_status.pack(side="left")

        self.lbl_hints = ttk.Label(f_status, text="Ctrl+F 搜索 | 空格 暂停 | Ctrl+滚轮 缩放", style="Gray.TLabel")
        self.lbl_hints.pack(side="right")

    def setup_tags(self):
        sz = self._log_font_size
        # Row error background highlight (lowered to sit behind text and selection/search highlights)
        self.txt_logs.tag_configure("row_err_bg", background="#381a1a")
        self.txt_logs.tag_lower("row_err_bg")

        # Level tags
        self.txt_logs.tag_configure("ts", foreground="#6e7681")
        self.txt_logs.tag_configure("lvl_api", foreground="#4ec9b0", font=(MONO_FONT, sz, "bold"))
        self.txt_logs.tag_configure("lvl_cache", foreground="#89d185")
        self.txt_logs.tag_configure("lvl_fetch", foreground="#569cd6")
        self.txt_logs.tag_configure("lvl_prefetch", foreground="#c586c0")
        self.txt_logs.tag_configure("lvl_retry", foreground="#e5c07b", font=(MONO_FONT, sz, "bold"))
        self.txt_logs.tag_configure("lvl_err", foreground="#f14c4c", font=(MONO_FONT, sz, "bold"))
        self.txt_logs.tag_configure("lvl_conn", foreground="#9cdcfe")
        self.txt_logs.tag_configure("reused", foreground="#50fa7b", font=(MONO_FONT, sz, "bold"))
        self.txt_logs.tag_configure("new_conn", foreground="#e5c07b")

        # Method tags
        self.txt_logs.tag_configure("method_post", foreground="#bd93f9", font=(MONO_FONT, sz, "bold"))
        self.txt_logs.tag_configure("method_get", foreground="#569cd6")
        self.txt_logs.tag_configure("method_other", foreground="#8be9fd")

        # Status code tags
        self.txt_logs.tag_configure("status_200", foreground="#89d185")
        self.txt_logs.tag_configure("status_304", foreground="#8be9fd")
        self.txt_logs.tag_configure("status_err", foreground="#f14c4c", font=(MONO_FONT, sz, "bold"))

        # Latency threshold tags
        self.txt_logs.tag_configure("lat_fast", foreground="#50fa7b")
        self.txt_logs.tag_configure("lat_normal", foreground="#cccccc")
        self.txt_logs.tag_configure("lat_warn", foreground="#e5c07b", font=(MONO_FONT, sz, "bold"))
        self.txt_logs.tag_configure("lat_slow", foreground="#ff6b6b", background="#401818", font=(MONO_FONT, sz, "bold"))

        # Host tag & URL styling
        self.txt_logs.tag_configure("host_tag", foreground="#79c0ff")
        self.txt_logs.tag_configure("url_path", foreground="#f0f6fc", font=(MONO_FONT, sz))
        self.txt_logs.tag_configure("url_query", foreground="#6e7681")
        self.txt_logs.tag_configure("url_size", foreground="#8b949e")

        # Search match highlight
        self.txt_logs.tag_configure("search_match", background="#53441a", foreground="#ffffff")

    def _set_font_size(self, size: int):
        if size == self._log_font_size:
            return
        self._log_font_size = size
        self.txt_logs.configure(font=(MONO_FONT, size))
        for tag in ("lvl_api", "lvl_retry", "lvl_err", "reused", "method_post", "status_err", "lat_warn", "lat_slow"):
            self.txt_logs.tag_configure(tag, font=(MONO_FONT, size, "bold"))
        for tag in ("url_path",):
            self.txt_logs.tag_configure(tag, font=(MONO_FONT, size))
        self.var_status.set(f"已调整控制台字号为: {size} pt")

    def _on_mousewheel_zoom(self, event):
        if event.delta > 0:
            self._set_font_size(min(18, self._log_font_size + 1))
        elif event.delta < 0:
            self._set_font_size(max(8, self._log_font_size - 1))
        return "break"

    def _on_space(self, event):
        if event.widget == self.entry_filter:
            return
        self.toggle_pause()
        return "break"

    def _on_find(self, _event=None):
        self.entry_filter.focus_set()
        self.entry_filter.select_range(0, tk.END)
        self.entry_filter.icursor(tk.END)
        return "break"

    def _on_escape(self, _event=None):
        if self.var_filter_text.get():
            self.var_filter_text.set("")
            self.reapply_filter()
        self.txt_logs.focus_set()
        return "break"


    def toggle_pause(self):
        self._is_paused = not self._is_paused
        self.var_paused.set(self._is_paused)
        if self._is_paused:
            self.btn_pause.config(text="▶ 继续")
        else:
            self.btn_pause.config(text="⏸ 暂停")
            self.reapply_filter()
        self._update_status_label()

    def on_live_log(self, line: str, level: str):
        """Thread-safe log sink: strictly NO Tkinter widget or Variable calls here."""
        if self._is_closed:
            return
        with self._queue_lock:
            self._all_records.append((line, level))
            self._incoming_queue.append((line, level))

    def _poll_update(self):
        """High-performance 50ms polling loop executing exclusively on the main GUI thread."""
        if self._is_closed:
            return
        try:
            self._flush_queue()
            now = time.monotonic()
            if now - self._last_stats_tick >= 1.0:
                self._last_stats_tick = now
                self._update_status_label()
        except Exception:
            pass
        finally:
            if not self._is_closed:
                try:
                    self._scheduled_job = self.top.after(50, self._poll_update)
                except Exception:
                    self._scheduled_job = None

    def _flush_queue(self):
        if self._is_paused:
            return

        with self._queue_lock:
            if not self._incoming_queue:
                return
            # Bounded batching: render at most 60 lines per frame to prevent UI stutter during asset floods
            batch = []
            for _ in range(min(len(self._incoming_queue), 60)):
                batch.append(self._incoming_queue.popleft())

        filter_txt = self.var_filter_text.get().strip().lower()
        filter_cat = self.var_filter_cat.get()

        to_insert = [rec for rec in batch if self._matches_filter(rec[0], rec[1], filter_txt, filter_cat)]
        if not to_insert:
            return

        self.txt_logs.configure(state="normal")
        for line, lvl in to_insert:
            self._render_line(line, lvl)

        # Trim top lines in widget if exceeding max capacity
        try:
            num_lines = int(self.txt_logs.index("end-1c").split(".")[0])
            if num_lines > self._max_records:
                trim_end = f"{num_lines - self._max_records}.0"
                self.txt_logs.delete("1.0", trim_end)
        except Exception:
            pass

        if self.var_auto_scroll.get():
            self.txt_logs.see("end")

        self.txt_logs.configure(state="disabled")

    def _align_log_line(self, line: str, lvl: str) -> str:
        line = line.rstrip("\r\n")
        if not (line.startswith("[") and "]" in line):
            return line
        r1 = line.find("]")
        ts = line[:r1+1]
        rest = line[r1+1:].strip()
        if not (rest.startswith("[") and "]" in rest):
            return line
        r2 = rest.find("]")
        raw_lvl = rest[1:r2]
        msg = rest[r2+1:].strip()

        pad_lvl = f"[{raw_lvl:<12}]"
        use_compact = self.var_compact_domain.get()

        if "BYPASS-API" in raw_lvl:
            # e.g.: 200 POST game.granbluefantasy.jp/rest/sound/quest_map_bgm?t=123 (168ms, HTTP/1.1, reused)
            m = re.match(r"(\d+)\s+([A-Z]+)\s+([^\s\(]+)(?:\s+\((\d+)ms(?:,\s*[^,]+)?(?:,\s*(reused|new))?\))?", msg)
            if m:
                code, method, full_url, ms, reuse = m.groups()
                ms_str = f"{int(ms):>4}ms" if ms else "   -ms"
                reuse_tag = f"[{reuse:<6}]" if reuse else "[      ]"
                if use_compact:
                    if "game.granbluefantasy.jp" in full_url:
                        host_tag = "[gbf  ]"
                        path = full_url.split("game.granbluefantasy.jp", 1)[1]
                        if not path.startswith("/"):
                            path = "/" + path
                    elif "akamaized.net" in full_url:
                        host_tag = "[asset]"
                        path = "/" + full_url.split("/", 1)[1] if "/" in full_url else full_url
                    elif "mbga.jp" in full_url:
                        host_tag = "[mbga ]"
                        path = full_url.split("mbga.jp", 1)[1]
                        if not path.startswith("/"):
                            path = "/" + path
                    else:
                        if "/" in full_url:
                            h, p = full_url.split("/", 1)
                            host_tag = f"[{h[:5]:<5}]"
                            path = "/" + p
                        else:
                            host_tag = "[other]"
                            path = "/" + full_url
                    return f"{ts} {pad_lvl} {method:<4} {code:<3} {ms_str} {reuse_tag} {host_tag} {path}"
                else:
                    return f"{ts} {pad_lvl} {method:<4} {code:<3} {ms_str} {reuse_tag} {full_url}"
        elif "FETCH-ASSET" in raw_lvl:
            # e.g.: 200 OK & STREAMED (132ms) -> /assets/...
            m = re.match(r"200 OK & STREAMED \((\d+)ms\) -> (.+)", msg)
            if m:
                ms, path = m.groups()
                ms_str = f"{int(ms):>4}ms" if ms else "   -ms"
                if use_compact:
                    return f"{ts} {pad_lvl} GET  200 {ms_str} [stream] [asset] {path}"
                else:
                    return f"{ts} {pad_lvl} GET  200 {ms_str} [stream] {path}"
        elif "CACHE" in raw_lvl:
            tag = "[disk  ]" if "DISK" in raw_lvl else ("[flight]" if "FLIGHT" in raw_lvl else "[ram   ]")
            if "304 Not Modified" in msg:
                m = re.search(r"->\s+(.+)", msg)
                path = m.group(1) if m else msg
                sub_tag = "[hit304]" if "DISK" in raw_lvl else "[ram304]"
                if use_compact:
                    return f"{ts} {pad_lvl} GET  304    0ms {sub_tag} [asset] {path}"
                else:
                    return f"{ts} {pad_lvl} GET  304    0ms {sub_tag} {path}"
            elif "HIT" in raw_lvl or "HIT" in msg:
                m = re.search(r"HIT(?:\s+\((\d+(?:\.\d+)?)ms\))?\s+->\s+([^\s\(]+)(.*)", msg)
                if m:
                    ms_val, path, extra = m.groups()
                    ms_int = int(round(float(ms_val))) if ms_val else 0
                    ms_str = f"{ms_int:>4}ms"
                    if use_compact:
                        return f"{ts} {pad_lvl} GET  200 {ms_str} {tag} [asset] {path}{extra}"
                    else:
                        return f"{ts} {pad_lvl} GET  200 {ms_str} {tag} {path}{extra}"
            elif "COALESCED" in msg:
                m = re.search(r"COALESCED\s+->\s+([^\s\(]+)(.*)", msg)
                if m:
                    path, extra = m.groups()
                    if use_compact:
                        return f"{ts} {pad_lvl} GET  200    0ms [flight] [asset] {path}{extra}"
                    else:
                        return f"{ts} {pad_lvl} GET  200    0ms [flight] {path}{extra}"
        elif "PREFETCH" == raw_lvl.strip():
            # e.g.: Warmed (P1) -> prd-game-a-granbluefantasy.akamaized.net/assets/... (12,345 B)
            m = re.match(r"Warmed \(P(\d+)\)\s+->\s+([^\s\(]+)(.*)", msg)
            if m:
                prio, full_url, extra = m.groups()
                prio_tag = f"[pf-p{prio:<2}]"
                if use_compact:
                    path = "/" + full_url.split("/", 1)[1] if "/" in full_url else full_url
                    return f"{ts} {pad_lvl} GET  200    -ms {prio_tag} [asset] {path}{extra}"
                else:
                    return f"{ts} {pad_lvl} GET  200    -ms {prio_tag} {full_url}{extra}"
        elif "FALLBACK" in raw_lvl:
            m = re.search(r"Served fallback cache \(([^)]+)\)\s*->\s*(.+)", msg)
            if m:
                fb_src, path = m.groups()
                tag = "[f-ram ]" if "RAM" in fb_src.upper() else "[f-disk]"
                if use_compact:
                    return f"{ts} {pad_lvl} GET  200    0ms {tag} [asset] {path}"
                else:
                    return f"{ts} {pad_lvl} GET  200    0ms {tag} {path}"
        elif "MOCK-200" in raw_lvl:
            m = re.match(r"Direct Mock -> (.+)", msg)
            if m:
                path = m.group(1)
                if use_compact:
                    return f"{ts} {pad_lvl} MOCK 200    0ms [mock  ] [gbf  ] {path}"
                else:
                    return f"{ts} {pad_lvl} MOCK 200    0ms [mock  ] {path}"
        elif "OPTIONS" in raw_lvl:
            m = re.search(r"-> (.+)", msg)
            path = m.group(1) if m else msg
            if use_compact:
                return f"{ts} {pad_lvl} OPT  200    0ms [cors  ] [gbf  ] {path}"
            else:
                return f"{ts} {pad_lvl} OPT  200    0ms [cors  ] {path}"
        elif "CONNECT" in raw_lvl:
            # e.g.: [127.0.0.1] prd-game-a-granbluefantasy.akamaized.net:443 -> MITM
            m = re.search(r"(?:\[([^\]]+)\]\s+)?([^\s]+)\s+->\s+(\w+)", msg)
            if m:
                client_ip, target, action = m.groups()
                action_tag = f"[{action.lower():<6}]"
                proto = "TLS " if action == "MITM" else "TCP "
                if use_compact:
                    if "akamaized.net" in target:
                        host_tag = "[asset]"
                    elif "ws." in target:
                        host_tag = "[ws   ]"
                    elif "granbluefantasy.jp" in target:
                        host_tag = "[gbf  ]"
                    else:
                        host_tag = "[other]"
                    return f"{ts} {pad_lvl} {proto} ---    -ms {action_tag} {host_tag} {target}"
                else:
                    return f"{ts} {pad_lvl} {proto} ---    -ms {action_tag} {target}"
        elif "BYPASS-TCP" in raw_lvl:
            # e.g.: Tunneling ws.game.granbluefantasy.jp:11240 via upstream
            target = msg.replace("Tunneling ", "").replace(" via upstream", "").strip()
            if use_compact:
                return f"{ts} {pad_lvl} TCP  ---    -ms [tunnel] [ws   ] {target}"
            else:
                return f"{ts} {pad_lvl} TCP  ---    -ms [tunnel] {target}"

        return f"{ts} {pad_lvl} {msg}"

    def _render_line(self, raw_line: str, lvl: str):
        raw_line = raw_line.rstrip("\r\n")
        is_aligned = self.var_align_format.get()
        if is_aligned:
            line = self._align_log_line(raw_line, lvl)
        else:
            line = raw_line

        end_idx = self.txt_logs.index("end-1c")
        self.txt_logs.insert("end", line + "\n")
        line_start = end_idx

        # Check error condition for full-row highlight
        is_err_line = False
        lvl_up = lvl.upper()
        if any(k in lvl_up for k in ("ERR", "TIMEOUT", "BLOCK")):
            is_err_line = True

        use_compact = self.var_compact_domain.get()
        r1 = line.find("]")
        r2 = line.find("]", r1 + 1) if r1 != -1 else -1
        if is_aligned and r1 == 9 and r2 != -1 and line.startswith("[") and line[r1:r1+3] == "] [" and line[r2:r2+2] == "] ":
            # 1. Timestamp (0..r1+1)
            self.txt_logs.tag_add("ts", line_start, f"{line_start} + {r1 + 1}c")
            # 2. Level tag (r1+2..r2+1)
            lvl_tag = self._get_level_tag(lvl)
            self.txt_logs.tag_add(lvl_tag, f"{line_start} + {r1 + 2}c", f"{line_start} + {r2 + 1}c")
            # 3. Method tag (r2+2..r2+6)
            m_start = r2 + 2
            method_str = line[m_start:m_start+4].strip()
            if method_str == "POST":
                self.txt_logs.tag_add("method_post", f"{line_start} + {m_start}c", f"{line_start} + {m_start+4}c")
            elif method_str == "GET":
                self.txt_logs.tag_add("method_get", f"{line_start} + {m_start}c", f"{line_start} + {m_start+4}c")
            else:
                self.txt_logs.tag_add("method_other", f"{line_start} + {m_start}c", f"{line_start} + {m_start+4}c")
            # 4. Status code (m_start+5..m_start+8)
            c_start = m_start + 5
            code_str = line[c_start:c_start+3].strip()
            if code_str == "200":
                self.txt_logs.tag_add("status_200", f"{line_start} + {c_start}c", f"{line_start} + {c_start+3}c")
            elif code_str == "304":
                self.txt_logs.tag_add("status_304", f"{line_start} + {c_start}c", f"{line_start} + {c_start+3}c")
            elif code_str.startswith("5") or code_str in ("400", "403", "404", "408"):
                is_err_line = True
                self.txt_logs.tag_add("status_err", f"{line_start} + {c_start}c", f"{line_start} + {c_start+3}c")
            elif code_str in ("500", "502", "503", "504"):
                is_err_line = True
                self.txt_logs.tag_add("status_err", f"{line_start} + {c_start}c", f"{line_start} + {c_start+3}c")

            # 5. Dynamic token scanning for Latency, State, Host and URL
            b_reuse = line.find("[", c_start + 4)
            if b_reuse != -1:
                # 5a. Latency threshold coloring (between c_start+4 and b_reuse)
                lat_chunk = line[c_start+4:b_reuse].strip()
                m_ms = re.search(r"(\d+)ms", lat_chunk)
                if m_ms:
                    lat = int(m_ms.group(1))
                    if lat < 100:
                        lat_tag = "lat_fast"
                    elif lat < 250:
                        lat_tag = "lat_normal"
                    elif lat < 500:
                        lat_tag = "lat_warn"
                    else:
                        lat_tag = "lat_slow"
                    self.txt_logs.tag_add(lat_tag, f"{line_start} + {c_start+4}c", f"{line_start} + {b_reuse-1}c")

                # 5b. Reuse / state tag
                b_reuse_end = line.find("]", b_reuse)
                if b_reuse_end != -1:
                    state_str = line[b_reuse+1:b_reuse_end].strip()
                    if state_str == "reused":
                        self.txt_logs.tag_add("reused", f"{line_start} + {b_reuse}c", f"{line_start} + {b_reuse_end+1}c")
                    elif state_str == "new":
                        self.txt_logs.tag_add("new_conn", f"{line_start} + {b_reuse}c", f"{line_start} + {b_reuse_end+1}c")
                    elif state_str == "stream":
                        self.txt_logs.tag_add("lvl_fetch", f"{line_start} + {b_reuse}c", f"{line_start} + {b_reuse_end+1}c")
                    elif state_str in ("hit304", "ram304"):
                        self.txt_logs.tag_add("status_304", f"{line_start} + {b_reuse}c", f"{line_start} + {b_reuse_end+1}c")
                    elif state_str in ("disk", "ram", "f-disk", "f-ram"):
                        self.txt_logs.tag_add("lvl_cache", f"{line_start} + {b_reuse}c", f"{line_start} + {b_reuse_end+1}c")
                    elif state_str.startswith("pf") or state_str.startswith("warm"):
                        self.txt_logs.tag_add("lvl_prefetch", f"{line_start} + {b_reuse}c", f"{line_start} + {b_reuse_end+1}c")
                    elif state_str in ("flight", "mitm", "tunnel"):
                        self.txt_logs.tag_add("reused", f"{line_start} + {b_reuse}c", f"{line_start} + {b_reuse_end+1}c")

                    # 5c. Host tag & URL path/query/size
                    rest = line[b_reuse_end+1:].strip()
                    if use_compact and rest.startswith("["):
                        b_host = line.find("[", b_reuse_end + 1)
                        b_host_end = line.find("]", b_host)
                        if b_host != -1 and b_host_end != -1:
                            self.txt_logs.tag_add("host_tag", f"{line_start} + {b_host}c", f"{line_start} + {b_host_end+1}c")
                            url_offset = b_host_end + 2
                        else:
                            url_offset = b_reuse_end + 2
                    else:
                        url_offset = b_reuse_end + 2

                    if url_offset < len(line):
                        url_str = line[url_offset:]
                        q_pos = url_str.find("?")
                        size_pos = url_str.rfind(" (")
                        if q_pos != -1:
                            self.txt_logs.tag_add("url_path", f"{line_start} + {url_offset}c", f"{line_start} + {url_offset + q_pos}c")
                            q_end = size_pos if (size_pos != -1 and size_pos > q_pos) else len(url_str)
                            self.txt_logs.tag_add("url_query", f"{line_start} + {url_offset + q_pos}c", f"{line_start} + {url_offset + q_end}c")
                            if size_pos != -1 and size_pos > q_pos:
                                self.txt_logs.tag_add("url_size", f"{line_start} + {url_offset + size_pos}c", f"{line_start} + {len(line)}c")
                        else:
                            path_end = size_pos if size_pos != -1 else len(url_str)
                            self.txt_logs.tag_add("url_path", f"{line_start} + {url_offset}c", f"{line_start} + {url_offset + path_end}c")
                            if size_pos != -1:
                                self.txt_logs.tag_add("url_size", f"{line_start} + {url_offset + size_pos}c", f"{line_start} + {len(line)}c")
        else:
            # Fallback tagging for unstructured/system lines
            if line.startswith("[") and "]" in line:
                r1 = line.find("]")
                self.txt_logs.tag_add("ts", line_start, f"{line_start} + {r1 + 1}c")
                l2 = line.find("[", r1 + 1)
                r2 = line.find("]", l2) if l2 != -1 else -1
                if l2 != -1 and r2 != -1:
                    lvl_tag = self._get_level_tag(lvl)
                    self.txt_logs.tag_add(lvl_tag, f"{line_start} + {l2}c", f"{line_start} + {r2 + 1}c")
            if any(k in line for k in (" 500 ", " 502 ", " 503 ", " 504 ", "TIMEOUT", "ERR", "BLOCK")):
                is_err_line = True

        # 6. Apply full row error background highlight if error detected
        if is_err_line:
            self.txt_logs.tag_add("row_err_bg", line_start, f"{line_start} lineend + 1c")

        # 9. Search keyword match highlight
        search_q = self.var_filter_text.get().strip().lower()
        if search_q:
            lower_line = line.lower()
            idx = 0
            while True:
                pos = lower_line.find(search_q, idx)
                if pos == -1:
                    break
                self.txt_logs.tag_add("search_match", f"{line_start} + {pos}c", f"{line_start} + {pos + len(search_q)}c")
                idx = pos + len(search_q)

    def _get_level_tag(self, lvl: str) -> str:
        lvl_up = lvl.upper()
        if "API" in lvl_up:
            return "lvl_api"
        if "CACHE" in lvl_up:
            return "lvl_cache"
        if "FETCH" in lvl_up:
            return "lvl_fetch"
        if "PREFETCH" in lvl_up:
            return "lvl_prefetch"
        if "RETRY" in lvl_up or "STALE" in lvl_up or "MOCK" in lvl_up:
            return "lvl_retry"
        if "ERR" in lvl_up or "TIMEOUT" in lvl_up or "BLOCK" in lvl_up:
            return "lvl_err"
        return "lvl_conn"

    def _matches_filter(self, line: str, lvl: str, txt: str, cat: str) -> bool:
        # Checkbox: Hide connection/handshake noise
        if self.var_hide_connect.get() and ("CONNECT" in lvl or "BYPASS-TCP" in lvl or "TLS" in lvl):
            return False
        # Checkbox: Mute assets
        if self.var_mute_assets.get() and any(k in lvl for k in ("FETCH", "PREFETCH", "CACHE")):
            return False
        # Checkbox: Hide mocks
        if self.var_hide_mocks.get() and ("MOCK" in lvl or "/ob/r" in line):
            return False
        # Text search
        if txt and txt not in line.lower():
            return False
        # Category filter
        if cat == "仅战斗 API (BYPASS-API)" and "BYPASS-API" not in lvl:
            return False
        if cat == "仅缓存命中 (CACHE)" and "CACHE" not in lvl:
            return False
        if cat == "仅素材拉取 (FETCH/PREFETCH)" and not any(k in lvl for k in ("FETCH", "PREFETCH")):
            return False
        if cat == "仅重连与告警 (RETRY/ERROR)" and not any(k in lvl for k in ("RETRY", "ERR", "TIMEOUT")):
            return False
        if cat == "仅慢请求 (>200ms)":
            m = re.search(r"(\d+)\s*ms", line)
            if not m or int(m.group(1)) < 200:
                return False
        return True

    def reapply_filter(self):
        filter_txt = self.var_filter_text.get().strip().lower()
        filter_cat = self.var_filter_cat.get()

        self.txt_logs.configure(state="normal")
        self.txt_logs.delete("1.0", "end")

        with self._queue_lock:
            records = list(self._all_records)

        for line, lvl in records:
            if self._matches_filter(line, lvl, filter_txt, filter_cat):
                self._render_line(line, lvl)

        if self.var_auto_scroll.get():
            self.txt_logs.see("end")

        self.txt_logs.configure(state="disabled")
        self._update_status_label()

    def _update_status_label(self):
        total = len(self._all_records)
        try:
            if self.txt_logs.compare("1.0", "==", "end-1c"):
                num_displayed = 0
            else:
                num_displayed = int(self.txt_logs.index("end-2c").split(".")[0])
        except Exception:
            num_displayed = 0

        # Telemetry metrics
        p50_str = "--"
        p95_str = "--"
        reuse_str = "--"
        retry_str = "0"
        try:
            if hasattr(gbf_proxy, "api_telemetry"):
                t_stats = gbf_proxy.api_telemetry.get_stats()
                pct = t_stats.get("percentiles", {})
                if pct.get("count", 0) > 0:
                    p50_str = f"{pct.get('p50', 0):.0f}ms"
                    p95_str = f"{pct.get('p95', 0):.0f}ms"
                    reuse_str = f"{t_stats.get('reuse_rate', 0):.1f}%"
                    retry_str = str(t_stats.get("retry_count", 0))
        except Exception:
            pass

        cache_str = "--"
        try:
            if hasattr(gbf_proxy, "PROXY_STATS"):
                ps = gbf_proxy.PROXY_STATS
                hits = ps.get("hits", 0)
                dls = ps.get("downloads", 0)
                if hits + dls > 0:
                    cache_str = f"{hits / (hits + dls) * 100:.1f}%"
        except Exception:
            pass

        status_flag = "⏸ 已暂停刷新" if self.var_paused.get() else "● 实时监听中"
        parts = []
        if p50_str != "--":
            parts.append(f"⚡ API P50: {p50_str} (P95: {p95_str})")
            parts.append(f"长连接复用: {reuse_str}")
        if cache_str != "--":
            parts.append(f"缓存命中: {cache_str}")
        if retry_str != "0":
            parts.append(f"重试: {retry_str}")
        parts.append(f"当前显示: {num_displayed} 行 / 历史: {total} 条")
        parts.append(status_flag)

        self.var_status.set("  |  ".join(parts))


    def clear_display(self):
        self.txt_logs.configure(state="normal")
        self.txt_logs.delete("1.0", "end")
        self.txt_logs.configure(state="disabled")
        self._update_status_label()

    def copy_all(self):
        text = self.txt_logs.get("1.0", "end-1c")
        if text:
            self.top.clipboard_clear()
            self.top.clipboard_append(text)
            self.var_status.set(f"已复制当前显示的全部日志到剪贴板！({len(text):,} 字符)")

    def export_logs(self):
        """Export full, raw diagnostic log with complete domains, query strings, and environment headers."""
        with self._queue_lock:
            records = list(self._all_records)
        if not records:
            messagebox.showinfo("提示", "当前没有可导出的日志记录。", parent=self.top)
            return

        default_name = f"gbf_diagnostic_{datetime.datetime.now().strftime('%Y%m%d_%H%M%S')}.log"
        file_path = filedialog.asksaveasfilename(
            parent=self.top,
            title="导出全量排查诊断日志 (Diagnostic Log)",
            initialfile=default_name,
            defaultextension=".log",
            filetypes=[("Log Files", "*.log"), ("Text Files", "*.txt"), ("All Files", "*.*")]
        )
        if not file_path:
            return

        header = [
            "# ==============================================================================",
            "# GBF Accelerator Diagnostic Log (全量故障排查诊断日志)",
            f"# Generated At: {datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')}",
            f"# Total Records: {len(records)}",
        ]
        try:
            if hasattr(gbf_proxy, "api_telemetry"):
                t_stats = gbf_proxy.api_telemetry.get_stats()
                pct = t_stats.get("percentiles", {})
                header.append(
                    f"# Telemetry: P50={pct.get('p50', 0):.1f}ms, P95={pct.get('p95', 0):.1f}ms, "
                    f"KeepAlive Reuse={t_stats.get('reuse_rate', 0):.1f}%, Retries={t_stats.get('retry_count', 0)}"
                )
        except Exception:
            pass

        try:
            if hasattr(gbf_proxy, "PROXY_STATS"):
                ps = gbf_proxy.PROXY_STATS
                hits = ps.get("hits", 0)
                dls = ps.get("downloads", 0)
                cache_r = f"{hits / (hits + dls) * 100:.1f}%" if (hits + dls) > 0 else "0.0%"
                header.append(
                    f"# Proxy Stats: Hits={hits}, RAM Hits={ps.get('ram_hits', 0)}, Downloads={dls}, "
                    f"APIs={ps.get('apis', 0)}, Cache Rate={cache_r}"
                )
        except Exception:
            pass

        header.append("# Notice: Contains complete raw URLs, query parameters and transport-level connection events.")
        header.append("# ==============================================================================\n")

        full_content = "\n".join(header) + "\n" + "\n".join(rec[0] for rec in records) + "\n"
        try:
            with open(file_path, "w", encoding="utf-8") as f:
                f.write(full_content)
            self.var_status.set(f"已导出全量诊断日志: {os.path.basename(file_path)} (共 {len(records)} 条原始记录)")
            messagebox.showinfo(
                "导出成功",
                f"全量排查诊断日志已成功导出至：\n{file_path}\n\n该文件包含 100% 完整的原始域名、请求参数及底层连接握手，可直接发给开发者排查问题。",
                parent=self.top
            )
        except Exception as e:
            messagebox.showerror("导出失败", f"无法写入文件: {e}", parent=self.top)

    def export_visible_logs(self):
        """Export currently filtered view text as displayed on screen."""
        text = self.txt_logs.get("1.0", "end-1c")
        if not text.strip():
            messagebox.showinfo("提示", "当前屏幕上没有可见日志。", parent=self.top)
            return
        default_name = f"gbf_view_{datetime.datetime.now().strftime('%Y%m%d_%H%M%S')}.log"
        file_path = filedialog.asksaveasfilename(
            parent=self.top,
            title="导出当前屏幕显示日志",
            initialfile=default_name,
            defaultextension=".log",
            filetypes=[("Log Files", "*.log"), ("Text Files", "*.txt"), ("All Files", "*.*")]
        )
        if file_path:
            try:
                with open(file_path, "w", encoding="utf-8") as f:
                    f.write(text)
                self.var_status.set(f"已导出屏幕视图日志至: {os.path.basename(file_path)}")
            except Exception as e:
                messagebox.showerror("导出失败", f"无法写入文件: {e}", parent=self.top)

    def _on_right_click(self, event):
        try:
            idx = self.txt_logs.index(f"@{event.x},{event.y}")
            line_no = idx.split(".")[0]
            self._context_line = self.txt_logs.get(f"{line_no}.0", f"{line_no}.end").strip()
            if self._context_line:
                self.context_menu.tk_popup(event.x_root, event.y_root)
        except Exception:
            pass

    def _copy_selected_line(self):
        if self._context_line:
            self.top.clipboard_clear()
            self.top.clipboard_append(self._context_line)
            self.var_status.set("已复制当前显示行到剪贴板！")

    def _copy_raw_line(self):
        """Find the corresponding raw line from _all_records and copy to clipboard."""
        if not self._context_line:
            return
        ts_match = re.match(r"^(\[\d{2}:\d{2}:\d{2}\])", self._context_line)
        if ts_match:
            ts = ts_match.group(1)
            # Find in reversed _all_records
            url = self._extract_url_from_line(self._context_line)
            path_key = url.split("?")[0].replace("https://game.granbluefantasy.jp", "").replace("https://prd-game-a-granbluefantasy.akamaized.net", "")
            with self._queue_lock:
                for raw, _ in reversed(self._all_records):
                    if raw.startswith(ts) and (not path_key or path_key in raw):
                        self.top.clipboard_clear()
                        self.top.clipboard_append(raw)
                        self.var_status.set("已复制对应的原始排查日志记录！")
                        return
        self._copy_selected_line()

    def _copy_selected_url(self):
        if not self._context_line:
            return
        url = self._extract_url_from_line(self._context_line)
        if url:
            self.top.clipboard_clear()
            self.top.clipboard_append(url)
            self.var_status.set(f"已复制 URL: {url}")
        else:
            self._copy_selected_line()

    def _filter_selected_path(self):
        if not self._context_line:
            return
        url = self._extract_url_from_line(self._context_line)
        if url:
            clean = url.split("?")[0].strip()
            clean_path = clean.replace("https://game.granbluefantasy.jp", "").replace("https://prd-game-a-granbluefantasy.akamaized.net", "")
            self.var_filter_text.set(clean_path if clean_path else clean)
            self.reapply_filter()

    def _extract_url_from_line(self, line: str) -> str:
        # Check compact host tags
        m_gbf = re.search(r"\[gbf\s*\]\s+(/[^\s\)]+)", line)
        if m_gbf:
            return f"https://game.granbluefantasy.jp{m_gbf.group(1)}"
        m_asset = re.search(r"\[asset\s*\]\s+(/[^\s\)]+)", line)
        if m_asset:
            return f"https://prd-game-a-granbluefantasy.akamaized.net{m_asset.group(1)}"
        m_mbga = re.search(r"\[mbga\s*\]\s+(/[^\s\)]+)", line)
        if m_mbga:
            return f"https://gbf.game.mbga.jp{m_mbga.group(1)}"

        m = re.search(r"(?:https?://[^\s]+|(?:game\.granbluefantasy\.jp|[a-zA-Z0-9\.\-]+akamaized\.net)?[/][^\s\)]+)", line)
        if m:
            found = m.group(0).rstrip(")")
            if found.startswith("/"):
                return f"https://game.granbluefantasy.jp{found}"
            return found
        if "->" in line:
            return line.split("->")[-1].strip().split()[0]
        return ""

    def on_close(self):
        self._is_closed = True
        gbf_proxy.unregister_log_listener(self.on_live_log)
        if self._scheduled_job and self._scheduled_job is not True:
            try:
                self.top.after_cancel(self._scheduled_job)
            except Exception:
                pass
        self._scheduled_job = None

        try:
            self.top.destroy()
        except Exception:
            pass
        if self.parent.log_window is self:
            self.parent.log_window = None

def main():
    root = tk.Tk()
    app = GBFAcceleratorGUI(root)
    if START_MINIMIZED:
        root.after(100, lambda: app.hide_to_tray(notify=False))
    root.mainloop()

if __name__ == "__main__":
    main()
