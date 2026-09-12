import os
import sys
import time
import threading
import subprocess
import webbrowser
import tkinter as tk
from tkinter import ttk, filedialog, messagebox
from pathlib import Path

# Ensure PIL and pystray
from PIL import Image, ImageDraw
import pystray

from config_manager import (
    get_base_dir,
    config_manager,
    is_ca_installed,
    install_ca_certificate,
    auto_detect_acgpower_cache,
    auto_detect_upstream_proxy,
    is_port_open,
)
from cert_manager import ensure_ca, CA_CERT_PATH
from cache_manager import cache_manager
import gbf_proxy
import system_proxy

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
        self.root.title("GBF 极速加速器 v1.1")
        self.root.geometry("640x580")
        self.root.minsize(600, 550)

        # Center window
        self.center_window()

        # Styles
        self.setup_styles()

        # Data variables
        self.var_status_text = tk.StringVar(value="● 正在启动中...")
        self.var_hits = tk.StringVar(value="0")
        self.var_downloads = tk.StringVar(value="0")
        self.var_apis = tk.StringVar(value="0")
        self.var_cache_dir = tk.StringVar(value=str(config_manager.get_effective_cache_dir(interactive=False)))
        self.var_upstream = tk.StringVar(value=config_manager.get_effective_upstream_proxy())
        self.var_ca_status = tk.StringVar(value="检测中...")
        self.var_auto_pac = tk.BooleanVar(value=config_manager.config.get("auto_system_proxy", True))

        # Build UI
        self.build_ui()

        # System tray setup
        self.tray_icon = None
        self.setup_tray()

        # Window events
        self.root.protocol("WM_DELETE_WINDOW", self.hide_to_tray)

        # Check CA status
        self.update_ca_status()

        # Ensure helper files
        from app_main import ensure_bundled_files
        ensure_bundled_files()

        # Start proxy thread automatically
        self.start_proxy()

        # Periodic timer for stats update
        self.update_stats_loop()

    def center_window(self):
        self.root.update_idletasks()
        w = self.root.winfo_width()
        h = self.root.winfo_height()
        ws = self.root.winfo_screenwidth()
        hs = self.root.winfo_screenheight()
        x = (ws // 2) - (w // 2)
        y = (hs // 2) - (h // 2) - 30
        self.root.geometry(f"+{x}+{y}")

    def setup_styles(self):
        style = ttk.Style(self.root)
        style.theme_use("clam")

        # Backgrounds
        self.root.configure(bg="#f4f6f9")
        style.configure("TFrame", background="#f4f6f9")
        style.configure("Card.TFrame", background="#ffffff", relief="flat")
        style.configure("CardInner.TFrame", background="#ffffff")

        # Labels
        style.configure("Title.TLabel", font=("Microsoft YaHei UI", 13, "bold"), background="#ffffff", foreground="#212529")
        style.configure("Subtitle.TLabel", font=("Microsoft YaHei UI", 9), background="#ffffff", foreground="#6c757d")
        style.configure("StatNum.TLabel", font=("Microsoft YaHei UI", 16, "bold"), background="#ffffff")
        style.configure("StatLabel.TLabel", font=("Microsoft YaHei UI", 9), background="#ffffff", foreground="#6c757d")
        style.configure("Normal.TLabel", font=("Microsoft YaHei UI", 9), background="#ffffff", foreground="#333333")
        style.configure("Gray.TLabel", font=("Microsoft YaHei UI", 8), background="#ffffff", foreground="#888888")

        # Buttons
        style.configure("Primary.TButton", font=("Microsoft YaHei UI", 9, "bold"))
        style.configure("Success.TButton", font=("Microsoft YaHei UI", 9, "bold"))
        style.configure("Danger.TButton", font=("Microsoft YaHei UI", 9, "bold"))

    def build_ui(self):
        main_container = ttk.Frame(self.root, padding="16 12 16 12")
        main_container.pack(fill="both", expand=True)

        # ---------------- 1. Status & Header Card ----------------
        card_header = ttk.Frame(main_container, style="Card.TFrame", padding="16 14 16 14")
        card_header.pack(fill="x", pady=(0, 10))

        h_left = ttk.Frame(card_header, style="CardInner.TFrame")
        h_left.pack(side="left", fill="both", expand=True)

        ttk.Label(h_left, text="碧蓝幻想 GBF 极速加速器", style="Title.TLabel").pack(anchor="w")
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
            pady=6,
            cursor="hand2",
            command=self.toggle_proxy,
        )
        self.btn_toggle.pack(side="right")

        # ---------------- 2. Real-time Stats Card ----------------
        card_stats = ttk.Frame(main_container, style="Card.TFrame", padding="14 12 14 12")
        card_stats.pack(fill="x", pady=(0, 10))

        grid_frame = ttk.Frame(card_stats, style="CardInner.TFrame")
        grid_frame.pack(fill="x")
        grid_frame.columnconfigure(0, weight=1)
        grid_frame.columnconfigure(1, weight=1)
        grid_frame.columnconfigure(2, weight=1)

        # Stat 1: 0ms Hits
        c1 = ttk.Frame(grid_frame, style="CardInner.TFrame")
        c1.grid(row=0, column=0, sticky="ew")
        lbl_hits_num = ttk.Label(c1, textvariable=self.var_hits, style="StatNum.TLabel", foreground="#28a745")
        lbl_hits_num.pack(anchor="center")
        ttk.Label(c1, text="⚡ 0ms SSD 命中次数", style="StatLabel.TLabel").pack(anchor="center")

        # Stat 2: Downloads
        c2 = ttk.Frame(grid_frame, style="CardInner.TFrame")
        c2.grid(row=0, column=1, sticky="ew")
        lbl_dl_num = ttk.Label(c2, textvariable=self.var_downloads, style="StatNum.TLabel", foreground="#007bff")
        lbl_dl_num.pack(anchor="center")
        ttk.Label(c2, text="📥 在线下载缓存", style="StatLabel.TLabel").pack(anchor="center")

        # Stat 3: APIs
        c3 = ttk.Frame(grid_frame, style="CardInner.TFrame")
        c3.grid(row=0, column=2, sticky="ew")
        lbl_api_num = ttk.Label(c3, textvariable=self.var_apis, style="StatNum.TLabel", foreground="#6c757d")
        lbl_api_num.pack(anchor="center")
        ttk.Label(c3, text="🔄 游戏 API 转发", style="StatLabel.TLabel").pack(anchor="center")

        # ---------------- 3. Settings Card ----------------
        card_settings = ttk.Frame(main_container, style="Card.TFrame", padding="16 14 16 14")
        card_settings.pack(fill="both", expand=True, pady=(0, 10))

        ttk.Label(card_settings, text="配置选项", style="Title.TLabel").pack(anchor="w", pady=(0, 8))

        # Field 1: Local Cache Dir
        ttk.Label(card_settings, text="本地缓存目录（支持无缝复用 ACGPower 缓存）：", style="Normal.TLabel").pack(anchor="w")
        f_dir = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_dir.pack(fill="x", pady=(3, 8))

        self.entry_dir = ttk.Entry(f_dir, textvariable=self.var_cache_dir, font=("Consolas", 9))
        self.entry_dir.pack(side="left", fill="x", expand=True, padx=(0, 6))

        btn_browse = ttk.Button(f_dir, text="浏览...", width=8, command=self.browse_cache_dir)
        btn_browse.pack(side="left", padx=(0, 4))

        btn_acgp = ttk.Button(f_dir, text="检测 ACGP", width=11, command=self.detect_acgp)
        btn_acgp.pack(side="left")

        # Field 2: Upstream Proxy
        ttk.Label(card_settings, text="上游网络代理（Clash Verge / Clash / v2rayN）：", style="Normal.TLabel").pack(anchor="w")
        f_up = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_up.pack(fill="x", pady=(3, 8))

        self.entry_up = ttk.Entry(f_up, textvariable=self.var_upstream, font=("Consolas", 9))
        self.entry_up.pack(side="left", fill="x", expand=True, padx=(0, 6))

        btn_probe = ttk.Button(f_up, text="自动探测", width=10, command=self.probe_upstream)
        btn_probe.pack(side="left", padx=(0, 4))

        btn_save_up = ttk.Button(f_up, text="保存设置", width=9, command=self.save_settings)
        btn_save_up.pack(side="left")

        # Field 3: CA Certificate
        ttk.Label(card_settings, text="HTTPS 根证书状态（游戏静态资源解密加速必需）：", style="Normal.TLabel").pack(anchor="w")
        f_ca = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_ca.pack(fill="x", pady=(3, 4))

        self.lbl_ca = ttk.Label(f_ca, textvariable=self.var_ca_status, font=("Microsoft YaHei UI", 9, "bold"))
        self.lbl_ca.pack(side="left", padx=(0, 10))

        btn_install_ca = ttk.Button(f_ca, text="一键安装/修复根证书", command=self.install_ca)
        btn_install_ca.pack(side="left")

        # Field 4: Windows System PAC Automation
        f_sys_proxy = ttk.Frame(card_settings, style="CardInner.TFrame")
        f_sys_proxy.pack(fill="x", pady=(10, 2))
        chk_pac = ttk.Checkbutton(
            f_sys_proxy,
            text="自动配置 Windows 系统代理（开箱即玩，无需装任何浏览器插件，仅分流 GBF）",
            variable=self.var_auto_pac,
            command=self.toggle_sys_proxy_setting,
        )
        chk_pac.pack(anchor="w")

        # ---------------- 4. Bottom Action Bar ----------------
        f_bottom = ttk.Frame(main_container)
        f_bottom.pack(fill="x", pady=(2, 0))

        btn_open_folder = ttk.Button(f_bottom, text="📂 打开缓存目录", command=self.open_cache_folder)
        btn_open_folder.pack(side="left", padx=(0, 6))

        btn_proxy_guide = ttk.Button(f_bottom, text="🌐 分流与使用说明", command=self.show_guide)
        btn_proxy_guide.pack(side="left", padx=(0, 6))

        btn_tray = ttk.Button(f_bottom, text="⬇ 最小化到系统托盘", command=self.hide_to_tray)
        btn_tray.pack(side="right")

    # ================= Functional Methods =================
    def update_ca_status(self):
        if is_ca_installed():
            self.var_ca_status.set("✔ 已信任安装 (正常工作)")
            self.lbl_ca.configure(foreground="#28a745")
        else:
            self.var_ca_status.set("✖ 尚未安装信任")
            self.lbl_ca.configure(foreground="#dc3545")

    def install_ca(self):
        ensure_ca()
        if is_ca_installed():
            messagebox.showinfo("根证书提示", "根证书已在系统的【受信任的根证书颁发机构】中，无需重复安装！")
            return

        messagebox.showinfo("安装指引", "即将调起 Windows 证书导入向导，若弹出系统安全提示框，请点击【是 (Y)】允许信任。")
        install_ca_certificate(CA_CERT_PATH)
        self.update_ca_status()

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
        active = auto_detect_upstream_proxy()
        self.var_upstream.set(active)
        config_manager.config["upstream_proxy"] = active
        config_manager.save_config()
        gbf_proxy.UPSTREAM_PROXY = active
        messagebox.showinfo("上游探测结果", f"检测到可用的本地代理服务：\n{active}\n\n已成功应用设置！")

    def save_settings(self):
        up = self.var_upstream.get().strip()
        cd = self.var_cache_dir.get().strip()
        if not up:
            messagebox.showerror("错误", "上游代理地址不能为空！")
            return
        config_manager.config["upstream_proxy"] = up
        config_manager.config["cache_dir"] = cd
        config_manager.config["auto_system_proxy"] = self.var_auto_pac.get()
        config_manager.save_config()
        gbf_proxy.UPSTREAM_PROXY = up
        if cd:
            cache_manager.set_cache_base(Path(cd).resolve())
        messagebox.showinfo("保存成功", "配置已保存并即时生效！")

    def toggle_sys_proxy_setting(self):
        enabled = self.var_auto_pac.get()
        config_manager.config["auto_system_proxy"] = enabled
        config_manager.save_config()
        if gbf_proxy.PROXY_STATS.get("is_running", False):
            if enabled:
                system_proxy.enable_pac_proxy(f"http://127.0.0.1:{gbf_proxy.LISTEN_PORT}/proxy.pac")
            else:
                system_proxy.disable_pac_proxy()

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
            messagebox.showinfo("分流指引", "请使用 ZeroOmega / SwitchyOmega 导入同目录下的 SwitchyOmega_GBF.bak，或直接勾选【自动配置 Windows 系统代理】实现免插件极速游玩。")

    def toggle_proxy(self):
        if gbf_proxy.PROXY_STATS["is_running"]:
            self.stop_proxy()
        else:
            self.start_proxy()

    def start_proxy(self):
        gbf_proxy.LISTEN_HOST = config_manager.config.get("listen_host", "127.0.0.1")
        gbf_proxy.LISTEN_PORT = int(config_manager.config.get("listen_port", 8124))
        gbf_proxy.UPSTREAM_PROXY = self.var_upstream.get().strip() or "http://127.0.0.1:7897"
        cache_manager.set_cache_base(Path(self.var_cache_dir.get()).resolve())

        gbf_proxy.start_proxy_thread()

        if self.var_auto_pac.get():
            system_proxy.enable_pac_proxy(f"http://127.0.0.1:{gbf_proxy.LISTEN_PORT}/proxy.pac")

        self.var_status_text.set(f"● 运行中 (监听端口 {gbf_proxy.LISTEN_PORT}) - 0ms 极速就绪")
        self.lbl_status.configure(foreground="#28a745")
        self.btn_toggle.configure(text="停止加速", bg="#dc3545", activebackground="#bd2130")
        if self.tray_icon:
            self.tray_icon.icon = create_tray_icon_image(True)

    def stop_proxy(self):
        gbf_proxy.stop_proxy_thread()
        if self.var_auto_pac.get():
            system_proxy.disable_pac_proxy()
        self.var_status_text.set("● 已停止加速")
        self.lbl_status.configure(foreground="#6c757d")
        self.btn_toggle.configure(text="启动加速", bg="#28a745", activebackground="#218838")
        if self.tray_icon:
            self.tray_icon.icon = create_tray_icon_image(False)

    def update_stats_loop(self):
        # Update numbers from PROXY_STATS
        hits = gbf_proxy.PROXY_STATS.get("hits", 0)
        dls = gbf_proxy.PROXY_STATS.get("downloads", 0)
        apis = gbf_proxy.PROXY_STATS.get("apis", 0)
        self.var_hits.set(f"{hits:,}")
        self.var_downloads.set(f"{dls:,}")
        self.var_apis.set(f"{apis:,}")

        # Sync button text if state changed outside
        is_thread_alive = gbf_proxy.proxy_thread is not None and gbf_proxy.proxy_thread.is_alive()
        is_running = gbf_proxy.PROXY_STATS.get("is_running", False) or is_thread_alive
        last_error = gbf_proxy.PROXY_STATS.get("last_error", "")

        if is_running and "停止" not in self.btn_toggle.cget("text"):
            self.btn_toggle.configure(text="停止加速", bg="#dc3545", activebackground="#bd2130")
            self.var_status_text.set(f"● 运行中 (监听端口 {gbf_proxy.LISTEN_PORT}) - 0ms 极速就绪")
            self.lbl_status.configure(foreground="#28a745")
            if self.tray_icon:
                self.tray_icon.icon = create_tray_icon_image(True)
        elif not is_running and "启动" not in self.btn_toggle.cget("text"):
            self.btn_toggle.configure(text="启动加速", bg="#28a745", activebackground="#218838")
            if last_error:
                self.var_status_text.set(f"● 异常停止: {last_error[:25]}")
                self.lbl_status.configure(foreground="#dc3545")
            else:
                self.var_status_text.set("● 已停止加速")
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
        self.tray_icon = pystray.Icon("GBF_Speed_Proxy", icon_img, "GBF 极速加速器 (0ms 运行中)", menu)
        # Run tray in separate background thread
        threading.Thread(target=self.tray_icon.run, daemon=True).start()

    def hide_to_tray(self):
        self.root.withdraw()
        try:
            if self.tray_icon:
                self.tray_icon.notify("GBF 极速加速器已最小化到系统托盘，后台持续为你加速中。", "GBF 加速器后台运行中")
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
    root.mainloop()

if __name__ == "__main__":
    main()
