//go:build !darwin

package main

func menuPanelHeight() float32 { return panelH }

func placePanel(string, float32, float32) {}

func (a *app) panelShowing() bool {
	return a.panelWin != nil && a.panelOpen
}

func panelDragBegin(string) {}

func panelDragMove(string) {}
