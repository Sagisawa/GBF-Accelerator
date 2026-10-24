package desktop

import "fmt"

type TrayText struct {
	TooltipRunning  string
	TooltipPaused   string
	TooltipFormat   string
	MenuConsole     string
	MenuStatusRun   string
	MenuStatusPause string
	MenuToggleStop  string
	MenuToggleStart string
	MenuOpenCache   string
	MenuQuit        string
}

func getTrayText(lang string) TrayText {
	switch lang {
	case "en":
		return TrayText{
			TooltipRunning:  "Running",
			TooltipPaused:   "Paused",
			TooltipFormat:   "GBF-Accelerator (%s)\nProxy Port: %d\nConsole Port: %d",
			MenuConsole:     "GBF-Accelerator Console",
			MenuStatusRun:   "Status: Proxy Running (Port %d)",
			MenuStatusPause: "Status: Proxy Paused",
			MenuToggleStop:  "Pause Proxy",
			MenuToggleStart: "Resume Proxy",
			MenuOpenCache:   "Open Cache Folder",
			MenuQuit:        "Exit",
		}
	case "ja":
		return TrayText{
			TooltipRunning:  "稼働中",
			TooltipPaused:   "一時停止中",
			TooltipFormat:   "GBF-Accelerator (%s)\nプロキシポート: %d\nコンソールポート: %d",
			MenuConsole:     "GBF-Accelerator コンソール",
			MenuStatusRun:   "状態: プロキシ稼働中 (ポート %d)",
			MenuStatusPause: "状態: プロキシ一時停止中",
			MenuToggleStop:  "プロキシを一時停止",
			MenuToggleStart: "プロキシを再開",
			MenuOpenCache:   "ローカルキャッシュフォルダを開く",
			MenuQuit:        "終了",
		}
	default: // "zh"
		return TrayText{
			TooltipRunning:  "运行中",
			TooltipPaused:   "已暂停",
			TooltipFormat:   "GBF-Accelerator (%s)\n代理端口: %d\n控制台端口: %d",
			MenuConsole:     "GBF-Accelerator 控制台",
			MenuStatusRun:   "状态: 代理运行中 (端口 %d)",
			MenuStatusPause: "状态: 代理已暂停",
			MenuToggleStop:  "暂停代理加速",
			MenuToggleStart: "启动代理加速",
			MenuOpenCache:   "打开本地缓存目录",
			MenuQuit:        "退出程序",
		}
	}
}

func formatTrayTooltip(lang string, running bool, proxyPort, controlPort int) string {
	txt := getTrayText(lang)
	state := txt.TooltipRunning
	if !running {
		state = txt.TooltipPaused
	}
	return fmt.Sprintf(txt.TooltipFormat, state, proxyPort, controlPort)
}

func formatTrayStatusMenu(lang string, running bool, proxyPort int) string {
	txt := getTrayText(lang)
	if running {
		return fmt.Sprintf(txt.MenuStatusRun, proxyPort)
	}
	return txt.MenuStatusPause
}

func formatTrayToggleMenu(lang string, running bool) string {
	txt := getTrayText(lang)
	if running {
		return txt.MenuToggleStop
	}
	return txt.MenuToggleStart
}
