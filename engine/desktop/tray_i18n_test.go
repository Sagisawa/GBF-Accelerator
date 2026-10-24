package desktop

import (
	"strings"
	"testing"
)

func TestTrayLocalization(t *testing.T) {
	langs := []string{"zh", "en", "ja", "unknown"}

	for _, lang := range langs {
		txt := getTrayText(lang)
		if txt.MenuConsole == "" || txt.MenuQuit == "" || txt.MenuOpenCache == "" {
			t.Errorf("getTrayText(%q) has empty menu items: %+v", lang, txt)
		}

		tooltipRunning := formatTrayTooltip(lang, true, 8124, 8125)
		tooltipPaused := formatTrayTooltip(lang, false, 8124, 8125)
		if tooltipRunning == "" || tooltipPaused == "" {
			t.Errorf("formatTrayTooltip(%q) returned empty", lang)
		}
		if tooltipRunning == tooltipPaused {
			t.Errorf("formatTrayTooltip(%q) running and paused should differ", lang)
		}

		statusRun := formatTrayStatusMenu(lang, true, 8124)
		statusPause := formatTrayStatusMenu(lang, false, 8124)
		if statusRun == "" || statusPause == "" {
			t.Errorf("formatTrayStatusMenu(%q) returned empty", lang)
		}
		if !strings.Contains(statusRun, "8124") {
			t.Errorf("formatTrayStatusMenu(%q, true, 8124) does not contain port: %s", lang, statusRun)
		}

		togStop := formatTrayToggleMenu(lang, true)
		togStart := formatTrayToggleMenu(lang, false)
		if togStop == "" || togStart == "" || togStop == togStart {
			t.Errorf("formatTrayToggleMenu(%q) invalid: stop=%s, start=%s", lang, togStop, togStart)
		}
	}

	// Verify specific texts
	enTxt := getTrayText("en")
	if enTxt.MenuConsole != "GBF-Accelerator Console" {
		t.Errorf("expected English console title, got %q", enTxt.MenuConsole)
	}
	if enTxt.MenuQuit != "Exit" || enTxt.MenuToggleStop != "Pause Proxy" || enTxt.MenuToggleStart != "Resume Proxy" || enTxt.MenuOpenCache != "Open Cache Folder" {
		t.Errorf("unexpected English tray texts: %+v", enTxt)
	}

	jaTxt := getTrayText("ja")
	if jaTxt.MenuConsole != "GBF-Accelerator コンソール" {
		t.Errorf("expected Japanese console title, got %q", jaTxt.MenuConsole)
	}
	if jaTxt.TooltipPaused != "一時停止中" || jaTxt.MenuStatusPause != "状態: プロキシ一時停止中" || jaTxt.MenuQuit != "終了" || jaTxt.MenuToggleStop != "プロキシを一時停止" || jaTxt.MenuToggleStart != "プロキシを再開" {
		t.Errorf("unexpected Japanese tray texts: %+v", jaTxt)
	}

	zhTxt := getTrayText("zh")
	if zhTxt.MenuConsole != "GBF-Accelerator 控制台" {
		t.Errorf("expected Chinese console title, got %q", zhTxt.MenuConsole)
	}
}
