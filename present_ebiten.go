//go:build !windows

package main

import (
	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
)

// ebitenPresenter draws straight into Ebitengine's own window, which is
// transparent on macOS and on Linux wherever a compositor is running.
//
// One difference from Windows is worth knowing: Ebitengine's transparent window
// takes the mouse over its whole rectangle, where the layered window on Windows
// only takes it where the creature is actually drawn. The desktop outside the
// creature's patch is unaffected either way.
type ebitenPresenter struct {
	w, h   int
	queued []overlayEvent
}

func newPresenter(w, h int) (presenter, error) {
	ebiten.SetWindowTitle("tangly")
	ebiten.SetWindowSize(w, h)
	ebiten.SetWindowDecorated(false)
	ebiten.SetWindowFloating(true)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeDisabled)
	return &ebitenPresenter{w: w, h: h}, nil
}

func (p *ebitenPresenter) present(screen *ebiten.Image, draw func(*ebiten.Image)) {
	p.collectEvents()
	// Nothing to convert or copy: the frame is drawn where it is shown.
	draw(screen)
}
func (p *ebitenPresenter) pointer() pointerState {
	cx, cy := ebiten.CursorPosition()
	inside := cx >= 0 && cy >= 0 && cx < p.w && cy < p.h
	return pointerState{
		pos:        V(float64(cx), float64(cy)),
		inside:     inside,
		left:       ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft),
		leftFresh:  inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft),
		right:      ebiten.IsMouseButtonPressed(ebiten.MouseButtonRight),
		rightFresh: inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight),
	}
}

func (p *ebitenPresenter) collectEvents() {
	for _, k := range []struct {
		key  ebiten.Key
		code keyCode
	}{
		{ebiten.KeyEscape, keyEscape},
		{ebiten.KeyM, keyMute},
		{ebiten.KeyF, keyTopmost},
	} {
		if inpututil.IsKeyJustPressed(k.key) {
			p.queued = append(p.queued, overlayEvent{key: k.code})
		}
	}
	if _, dy := ebiten.Wheel(); dy != 0 {
		p.queued = append(p.queued, overlayEvent{wheel: int(dy)})
	}
}

func (p *ebitenPresenter) events() []overlayEvent {
	out := p.queued
	p.queued = nil
	return out
}

func (p *ebitenPresenter) move(x, y int32) {
	scale := ebiten.DeviceScaleFactor()
	ebiten.SetWindowPosition(int(float64(x)*scale), int(float64(y)*scale))
}

func (p *ebitenPresenter) rect() (x, y, w, h int32) {
	px, py := ebiten.WindowPosition()
	scale := ebiten.DeviceScaleFactor()
	if scale <= 0 {
		scale = 1
	}
	return int32(float64(px) / scale), int32(float64(py) / scale), int32(p.w), int32(p.h)
}

func (p *ebitenPresenter) topmost(on bool) {
	ebiten.SetWindowFloating(on)
}

func (p *ebitenPresenter) floating() bool { return ebiten.IsWindowFloating() }

// frameStats cannot be had without reading the frame back, which is not worth a
// round trip every second; the window rectangle still comes back.
func (p *ebitenPresenter) frameStats() (int, int, int, bool) { return 0, 0, 0, false }

func (p *ebitenPresenter) focused() bool { return ebiten.IsFocused() }

func (p *ebitenPresenter) screenSize() (w, h int) {
	return ebiten.Monitor().Size()
}

func (p *ebitenPresenter) hostWindow() (int, int) { return p.w, p.h }

func (p *ebitenPresenter) close() {}
