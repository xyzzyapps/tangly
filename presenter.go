package main

import "github.com/hajimehoshi/ebiten/v2"

// presenter is the desktop surface the creature is drawn on, and the pointer it
// watches. There are two of them:
//
//   - on Windows, a layered window of our own (present_windows.go), because
//     Ebitengine can only present a transparent window through DXGI composition
//     and machines without it get an opaque rectangle;
//   - everywhere else, Ebitengine's own window with a transparent framebuffer
//     (present_ebiten.go), which Cocoa and X11 with a compositor both provide.
//
// The game and the script talk only to this interface, so the two behave the
// same from the outside.
type presenter interface {
	// present runs the drawing. screen is Ebitengine's own window surface; a
	// presenter that draws straight into it uses it, one that presents elsewhere
	// ignores it.
	present(screen *ebiten.Image, draw func(*ebiten.Image))

	// pointer is the mouse state in the creature's coordinates.
	pointer() pointerState

	// events are the key presses and wheel clicks since the last call.
	events() []overlayEvent

	// move puts the creature's patch of desktop somewhere.
	move(x, y int32)

	// rect is where that patch is, in desktop pixels.
	rect() (x, y, w, h int32)

	topmost(on bool)
	floating() bool

	// frameStats counts how much of the surface is drawn. ok is false when the
	// presenter cannot say, which is the case when Ebitengine owns the window.
	frameStats() (opaque, partial, transparent int, ok bool)

	focused() bool
	screenSize() (w, h int)
	// hostWindow is the size of Ebitengine's own window: a single hidden pixel on
	// Windows, the creature's window everywhere else.
	hostWindow() (w, h int)

	close()
}

// pointerState is the mouse, in the creature's coordinates.
type pointerState struct {
	pos        Vec
	inside     bool // the pointer is over the creature's patch of desktop
	left       bool
	leftFresh  bool // went down since the last poll, however briefly
	right      bool
	rightFresh bool
}

// keyCode is a shortcut the creature understands, named rather than numbered so
// the two presenters can report it however their platform spells it.
type keyCode uint8

const (
	keyNone keyCode = iota
	keyEscape
	keyMute
	keyTopmost
)

// overlayEvent is a shortcut or a wheel click from the presenter.
type overlayEvent struct {
	key   keyCode
	wheel int // wheel clicks, positive when rolling forward
	quit  bool
}
