// Package uitheme is a Claude-like re-skin of Fyne's default theme: a warm
// neutral background/foreground and a terracotta accent, in both light and
// dark variants. Only colors are overridden — fonts, icons, and sizes stay
// stock Fyne (no bundled font assets).
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

// Accent is Claude's terracotta brand color, used both by the theme and by
// hand-drawn canvas elements (e.g. the usage chart) so they match exactly.
var Accent = colorHex("#CC785C")

// GridLine is a faint line color for chart gridlines, distinct per variant.
var (
	GridLineLight = colorHex("#E0DDD3")
	GridLineDark  = colorHex("#3A3833")
)

// CurrentGridLine returns the gridline color matching the app's current
// light/dark setting, for hand-drawn canvas elements (e.g. the usage chart)
// that can't look this up through the normal widget theming path.
func CurrentGridLine() color.Color {
	if fyne.CurrentApp().Settings().ThemeVariant() == theme.VariantDark {
		return GridLineDark
	}
	return GridLineLight
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

// New returns a Claude-like theme wrapping Fyne's built-in adaptive default.
func New() fyne.Theme {
	return claudeTheme{base: theme.DefaultTheme()}
}

func (t claudeTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	dark := variant == theme.VariantDark
	switch name {
	case theme.ColorNameBackground:
		if dark {
			return colorHex("#1F1E1D")
		}
		return colorHex("#F5F4EF")
	case theme.ColorNamePrimary, theme.ColorNameHyperlink, theme.ColorNameFocus, theme.ColorNameSelection:
		return Accent
	case theme.ColorNameForeground:
		if dark {
			return colorHex("#E8E6DC")
		}
		return colorHex("#3D3929")
	case theme.ColorNameInputBackground, theme.ColorNameButton, theme.ColorNameMenuBackground:
		if dark {
			return colorHex("#2A2926")
		}
		return colorHex("#EDEAE1")
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
			return colorHex("#A6A196")
		}
		return colorHex("#6B675C")
	}
	return t.base.Color(name, variant)
}

func (t claudeTheme) Font(style fyne.TextStyle) fyne.Resource    { return t.base.Font(style) }
func (t claudeTheme) Icon(name fyne.ThemeIconName) fyne.Resource { return t.base.Icon(name) }
func (t claudeTheme) Size(name fyne.ThemeSizeName) float32       { return t.base.Size(name) }
