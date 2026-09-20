package main

import (
	"log"
	"math/rand/v2"
	"sync/atomic"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
)

const (
	// windowW and windowH are the creature's world, and the size of the overlay
	// it walks around on.
	windowW = 720
	windowH = 540

	// The host window is where Ebitengine runs its loop. Its pixels are thrown
	// away: everything visible is presented through the overlay.
	hostW = 1
	hostH = 1

	vkEscape = 0x1B
	vkM      = 0x4D
	vkF      = 0x46

	// solverIters is how many relaxation passes each tick gets.
	solverIters = 20
	// scriptPath is the creature definition, reloaded whenever it changes.
	scriptPath = "tangly.js"
)

type game struct {
	c        *creature
	bank     *soundBank
	ov       *overlay
	paint    painter
	frame    *ebiten.Image
	dt       float64
	script   *scriptHost
	quitting atomic.Bool

	leftDown  bool
	rightDown bool
	grab      bool
	winDrag   bool
	dragMouse Vec
	dragWin   [2]int32
	quit      bool

	// paused freezes the simulation; step runs a few ticks while frozen, which is
	// how a pose can be examined at the prompt.
	paused bool
	step   int
}

func newGame(bank *soundBank, ov *overlay) *game {
	c := newCreatureAt(V(windowW/2, windowH/2))
	g := &game{
		c:     c,
		bank:  bank,
		ov:    ov,
		paint: painter{dim: 1, st: defaultStyle},
		frame: ebiten.NewImage(windowW, windowH),
		dt:    1.0 / ebiten.DefaultTPS,
	}
	g.attach(c)
	return g
}

// newCreatureAt builds a creature in a fresh world.
func newCreatureAt(center Vec) *creature {
	w := &world{bounds: rect{0, 0, windowW, windowH}, iters: solverIters}
	rng := rand.New(rand.NewPCG(uint64(time.Now().UnixNano()), 0x9e3779b97f4a7c15))
	return newCreature(w, center, rng)
}

// attach hooks the presentation layer onto a creature, so the model stays
// unaware of sound and windowing.
func (g *game) attach(c *creature) {
	c.onPlant = func(strength float64) {
		g.withSound(func() { g.bank.play(g.bank.step, 0.55+0.45*strength) })
	}
	c.onArrive = func() {
		g.withSound(func() { g.bank.play(g.bank.settle, 1) })
	}
	c.onChatter = func() {
		g.withSound(func() { g.bank.play(g.bank.chatter, 0.8) })
	}
}

// rebuild replaces the live creature with one built by fn, keeping where it was
// standing. This is how a script swaps the creature out from under the running
// simulation.
func (g *game) rebuild(fn func(*creature)) {
	old := g.c
	c := newCreatureAt(old.pos)
	c.angle = old.angle
	c.vel = old.vel
	fn(c)
	g.attach(c)
	g.c = c
}

// requestQuit asks the game loop to stop, from any goroutine.
func (g *game) requestQuit() { g.quitting.Store(true) }

// withSound plays only while the creature's corner of the desktop is in use, so
// it never chirps away in the background.
func (g *game) withSound(fn func()) {
	if _, inside := g.ov.cursor(); inside || g.ov.focused() {
		fn()
	}
}

func (g *game) Update() error {
	g.bank.beginFrame()

	for _, ev := range g.ov.drainEvents() {
		switch {
		case ev.key == vkEscape:
			g.quit = true
		case ev.key == vkM:
			g.bank.muted = !g.bank.muted
			log.Printf("muted: %v", g.bank.muted)
		case ev.key == vkF:
			g.ov.setTopmost(!g.ov.floating())
			log.Printf("stay on top: %v", g.ov.floating())
		case ev.wheel != 0:
			log.Printf("volume: %.2f", g.bank.setVolume(g.bank.master+0.02*float64(ev.wheel)))
		}
	}

	// The pointer is read globally: the overlay is transparent to the mouse
	// almost everywhere, so the creature watches and reacts to the pointer
	// wherever it is.
	cur, inside := g.ov.cursor()
	// The creature only watches the pointer while it is over its own corner of the
	// desktop, so it does not turn to follow the mouse all over the screen.
	g.c.SetCursor(cur, inside)
	g.handleMouse(cur, inside)

	switch {
	case !g.paused:
		g.c.Update(g.dt)
	case g.step > 0:
		g.step--
		g.c.Update(g.dt)
	}

	if g.script != nil {
		g.script.drain()
	}
	if g.quit || g.quitting.Load() {
		return ebiten.Termination
	}
	return nil
}

// handleMouse drives the creature from the pointer: a click inside its world
// sends it there, a click on its body picks it up, and shift-drag carries the
// whole world (and the creature with it) somewhere else on the desktop.
func (g *game) handleMouse(cur Vec, inside bool) {
	left, leftFresh := keyState(vkLeftButton)
	right, rightFresh := keyState(vkRightButton)
	shift, _ := keyState(vkShift)

	if (leftFresh || (left && !g.leftDown)) && inside {
		switch {
		case shift:
			g.winDrag = true
			g.dragMouse = cur
			x, y, _, _ := g.ov.rect()
			g.dragWin = [2]int32{x, y}
		case g.c.NearBody(cur, 34):
			g.grab = true
			g.c.BeginDrag(cur)
		default:
			g.c.SetTarget(cur)
			g.withSound(func() { g.bank.play(g.bank.chirp, 1) })
		}
	}
	if (rightFresh || (right && !g.rightDown)) && inside && g.c.NearBody(cur, 44) {
		g.quit = true
	}
	if !left {
		g.winDrag = false
		if g.grab {
			g.grab = false
			g.c.EndDrag()
		}
	}

	if g.winDrag {
		g.ov.move(g.dragWin[0]+int32(cur.X-g.dragMouse.X), g.dragWin[1]+int32(cur.Y-g.dragMouse.Y))
	}
	if g.grab {
		g.c.DragTo(cur)
	}
	g.leftDown, g.rightDown = left, right
}

func (g *game) Draw(screen *ebiten.Image) {
	g.frame.Clear()
	drawCreature(g.frame, g.c, &g.paint)
	g.ov.withPixels(func(pix []byte) {
		g.frame.ReadPixels(pix) // RGBA, premultiplied
		swizzleToBGRA(pix)
	})
}

// Layout describes the host window, which is a single pixel.
func (g *game) Layout(outsideWidth, outsideHeight int) (int, int) {
	return hostW, hostH
}

func main() {
	log.SetFlags(0)

	makeDPIAware()
	ov, err := newOverlay(windowW, windowH)
	if err != nil {
		log.Fatal(err)
	}
	defer ov.close()

	ebiten.SetWindowTitle("tangly host")
	ebiten.SetWindowSize(hostW, hostH)
	ebiten.SetWindowDecorated(false)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeDisabled)
	ebiten.SetWindowPosition(0, 0)
	ebiten.SetTPS(ebiten.DefaultTPS)

	bank := newSoundBank()
	g := newGame(bank, ov)
	g.script = newScriptHost(g, scriptPath)

	log.Printf("tangly awake: %s", g.c.summary())
	log.Println("click its corner of the desktop: it walks there | drag its body: carry it | shift+drag: move its corner")
	log.Println("right-click on it (or Esc): quit | wheel over it: volume | M: mute | F: always on top")
	log.Printf("repl: type at this console (help() lists the api). %s reloads on save.", scriptPath)

	opts := &ebiten.RunGameOptions{ScreenTransparent: true, SkipTaskbar: true}
	if err := ebiten.RunGameWithOptions(g, opts); err != nil {
		log.Fatal(err)
	}
}

// swizzleToBGRA converts Ebitengine's RGBA order into the BGRA order that
// UpdateLayeredWindow expects. Both are premultiplied.
func swizzleToBGRA(pix []byte) {
	for i := 0; i+3 < len(pix); i += 4 {
		pix[i], pix[i+2] = pix[i+2], pix[i]
	}
}
