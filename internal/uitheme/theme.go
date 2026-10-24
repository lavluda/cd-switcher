// Package uitheme is a neutral, professional re-skin of Fyne's default theme
// with a single Claude-terracotta accent reserved for on/selected states, in
// both light and dark variants. Only colors are overridden — fonts, icons,
// and sizes stay stock Fyne (no bundled font assets).
//
// This only affects Fyne-rendered surfaces (the Settings window). The system
// tray menu is OS-native chrome and zenity dialogs are native OS dialogs —
// neither picks up this theme.
package uitheme

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Accent is Claude's terracotta brand color. Used sparingly: a toggle's on
// state, the selected segment in a segmented control, a primary button, and
// hand-drawn canvas elements (e.g. the usage chart) that need to match them
// exactly. It is deliberately not used for backgrounds or body text — a
// professional settings UI reserves color for state, not decoration.
var Accent = colorHex("#CC785C")

// AccentSoft is a low-opacity wash of Accent, for badges that need to read as
// "tinted," not "filled" (e.g. the "Active" / "Most room" account markers).
var AccentSoft = color.NRGBA{R: 0xCC, G: 0x78, B: 0x5C, A: 0x22}

// GridLine is a faint line color for hairline dividers and chart gridlines,
// distinct per variant.
var (
	GridLineLight = colorHex("#E3E2DF")
	GridLineDark  = colorHex("#39383A")
)

// TrackOff is a toggle switch's "off" track color: a clearly visible neutral
// gray, distinct from both the white card behind it and the white knob on
// top of it (a too-pale track reads as a rendering glitch, not an "off"
// switch).
var (
	TrackOffLight = colorHex("#C7C6C2")
	TrackOffDark  = colorHex("#54524C")
)

// isDark reports whether the app's current theme variant is dark, the one bit
// hand-drawn canvas elements need to pick their own colors.
func isDark() bool {
	return fyne.CurrentApp().Settings().ThemeVariant() == theme.VariantDark
}

// CurrentGridLine returns the gridline color matching the app's current
// light/dark setting, for hand-drawn canvas elements (e.g. the usage chart)
// that can't look this up through the normal widget theming path.
func CurrentGridLine() color.Color {
	if isDark() {
		return GridLineDark
	}
	return GridLineLight
}

// CurrentTrackOff returns the toggle-track-off color for the current variant.
func CurrentTrackOff() color.Color {
	if isDark() {
		return TrackOffDark
	}
	return TrackOffLight
}

// CurrentForeground returns the primary text color for the current variant,
// for hand-drawn canvas.Text elements that need to match surrounding widgets.
func CurrentForeground() color.Color {
	if isDark() {
		return colorHex("#EDEDEC")
	}
	return colorHex("#201F1D")
}

// CurrentMuted returns the secondary/caption text color for the current
// variant, matching widget.LowImportance labels.
func CurrentMuted() color.Color {
	if isDark() {
		return colorHex("#9C9A96")
	}
	return colorHex("#6B6A67")
}

func colorHex(hex string) color.Color {
	var r, g, b uint8
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		panic("uitheme: invalid color " + hex)
	}
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}
}

type claudeTheme struct {
	base fyne.Theme
}

// New returns the app's theme, wrapping Fyne's built-in adaptive default.
func New() fyne.Theme {
	return claudeTheme{base: theme.DefaultTheme()}
}

func (t claudeTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	dark := variant == theme.VariantDark
	switch name {
	case theme.ColorNameBackground:
		// Near-white, not a tinted neutral — the page behind the cards is
		// meant to all but disappear, with hairline borders (not a color
		// step) doing the work of separating one card from the next.
		if dark {
			return colorHex("#1B1B1C")
		}
		return colorHex("#FAFAFA")
	case theme.ColorNamePrimary, theme.ColorNameHyperlink, theme.ColorNameFocus, theme.ColorNameSelection:
		return Accent
	case theme.ColorNameForeground:
		if dark {
			return colorHex("#EDEDEC")
		}
		return colorHex("#201F1D")
	case theme.ColorNameInputBackground:
		// Cards and panels: a surface that reads as *raised* above
		// ColorNameBackground, not a same-toned block sitting on it.
		if dark {
			return colorHex("#28282A")
		}
		return colorHex("#FFFFFF")
	case theme.ColorNameButton, theme.ColorNameMenuBackground:
		// Plain/unselected controls (an unselected segmented-control option,
		// a stock button): one step down from the card surface, so they read
		// as sitting inside a card rather than being a card themselves.
		if dark {
			return colorHex("#1F1F20")
		}
		return colorHex("#EFEFED")
	case theme.ColorNameSeparator, theme.ColorNameInputBorder:
		if dark {
			return GridLineDark
		}
		return GridLineLight
	case theme.ColorNameDisabled, theme.ColorNamePlaceHolder:
		// widget.LowImportance text (secondary labels: stats lines, chart
		// timestamps, the footer note) renders in this color — the base
		// theme's disabled gray had too little contrast against our custom
		// background and was unreadable.
		if dark {
			return colorHex("#9C9A96")
		}
		return colorHex("#6B6A67")
	}
	return t.base.Color(name, variant)
}

func (t claudeTheme) Font(style fyne.TextStyle) fyne.Resource    { return t.base.Font(style) }
func (t claudeTheme) Icon(name fyne.ThemeIconName) fyne.Resource { return t.base.Icon(name) }

func (t claudeTheme) Size(name fyne.ThemeSizeName) float32 {
	switch name {
	case theme.SizeNamePadding:
		return 6
	case theme.SizeNameInnerPadding:
		return 12
	case theme.SizeNameText:
		return 13
	case theme.SizeNameHeadingText:
		return 20
	case theme.SizeNameSubHeadingText:
		return 15
	}
	return t.base.Size(name)
}
