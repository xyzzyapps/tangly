//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/hajimehoshi/ebiten/v2"
	"golang.org/x/sys/windows"
)

// Windows presents the creature through a layered window of its own.
//
// Ebitengine can only present a transparent window through DXGI composition. On
// machines where that is unavailable the window is created with a redirection
// surface and the alpha channel is dropped, so it shows up as an opaque
// rectangle. So the creature is drawn into an offscreen Ebitengine image and
// presented here instead: a layered Win32 window with per-pixel alpha.
const overlayClassName = "tanglyOverlay"

const (
	wsPopup        = 0x80000000
	wsExLayered    = 0x00080000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080

	swShowNoActivate = 4

	ulwAlpha = 0x00000002

	acSrcOver  = 0x00
	acSrcAlpha = 0x01

	pmRemove = 0x0001

	wmDestroy     = 0x0002
	wmKeyDown     = 0x0100
	wmSysKeyDown  = 0x0104
	wmMouseWheel  = 0x020A
	wmLButtonDown = 0x0201
	wmNcHitTest   = 0x0084
	wmEraseBkgnd  = 0x0014

	htTransparent = ^uintptr(0) // -1
	htClient      = 1

	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010

	hwndTopmost    = ^uintptr(0) // -1
	hwndNotTopmost = ^uintptr(1) // -2

	smCxScreen = 0
	smCyScreen = 1

	dibRGBColors = 0
	biRGB        = 0

	vkLeftButton  = 0x01
	vkRightButton = 0x02
)

type wndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type point struct{ x, y int32 }

type winSize struct{ cx, cy int32 }

type winMsg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type blendFunction struct {
	blendOp             byte
	blendFlags          byte
	sourceConstantAlpha byte
	alphaFormat         byte
}

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procPeekMessageW        = user32.NewProc("PeekMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessageW    = user32.NewProc("DispatchMessageW")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procSetWindowPos        = user32.NewProc("SetWindowPos")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procGetAsyncKeyState    = user32.NewProc("GetAsyncKeyState")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procLoadCursorW         = user32.NewProc("LoadCursorW")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procSetProcessDPIAware  = user32.NewProc("SetProcessDpiAwarenessContext")
	procGetForegroundWindow = user32.NewProc("GetForegroundWindow")
	procGetModuleHandleW    = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetModuleHandleW")

	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procDeleteObject       = gdi32.NewProc("DeleteObject")

	theOverlay atomic.Pointer[overlay]
)

type overlay struct {
	hwnd   uintptr
	memDC  uintptr
	bitmap uintptr
	pix    []byte        // BGRA, premultiplied, top-down
	frame  *ebiten.Image // what the game drew, before conversion

	w, h       int
	posX, posY int32

	mu        sync.Mutex
	primed    bool
	seq       uint64
	queued    []overlayEvent
	closed    bool
	isTopmost bool
}

// keyState reports whether a key or mouse button is held, and whether it went
// down since the previous poll. The second value is what catches a click that is
// shorter than one tick, which a plain state poll would miss.
func keyState(vk uintptr) (held, fresh bool) {
	r, _, _ := procGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0, r&0x0001 != 0
}

// makeDPIAware keeps the overlay's pixels and pointer coordinates in the same
// space as Ebitengine's window.
func makeDPIAware() {
	const perMonitorAwareV2 = ^uintptr(3) // -4
	procSetProcessDPIAware.Call(perMonitorAwareV2)
}

// The host window is a single hidden pixel on Windows; the layered window is the
// one that shows anything.
const (
	o_hostW = 1
	o_hostH = 1
)

// newPresenter creates the layered window and starts the thread that presents it.
func newPresenter(w, h int) (presenter, error) {
	makeDPIAware()
	ebiten.SetWindowTitle("tangly host")
	ebiten.SetWindowSize(o_hostW, o_hostH)
	ebiten.SetWindowDecorated(false)
	ebiten.SetWindowPosition(0, 0)
	o := &overlay{w: w, h: h, isTopmost: true, frame: ebiten.NewImage(w, h)}
	ready := make(chan error, 1)
	go o.run(ready)
	if err := <-ready; err != nil {
		return nil, err
	}
	theOverlay.Store(o)
	return o, nil
}

// hostWindow is a single hidden pixel: the real window is the layered one.
func (o *overlay) hostWindow() (int, int) { return 1, 1 }

// pointer is the mouse, read globally: the overlay is transparent to the mouse
// almost everywhere, so its own window messages would miss most of it.
func (o *overlay) pointer() pointerState {
	cur, inside := o.cursor()
	left, leftFresh := keyState(vkLeftButton)
	right, rightFresh := keyState(vkRightButton)

	// GetAsyncKeyState's "pressed since the last call" bit means nothing on the
	// first call, and the pointer starts over the creature, so without this the
	// first frame looks like a fresh right-click on it and the pet quits at once.
	o.mu.Lock()
	if !o.primed {
		o.primed = true
		leftFresh, rightFresh = false, false
	}
	o.mu.Unlock()
	return pointerState{
		pos: cur, inside: inside,
		left: left, leftFresh: leftFresh,
		right: right, rightFresh: rightFresh,
	}
}

func (o *overlay) run(ready chan error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	hInst, _, _ := procGetModuleHandleW.Call(0)
	wndProc := windows.NewCallback(overlayWndProc)

	className, err := windows.UTF16PtrFromString(overlayClassName)
	if err != nil {
		ready <- err
		return
	}
	wc := wndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wndClassEx{})),
		lpfnWndProc:   wndProc,
		hInstance:     windows.Handle(hInst),
		hCursor:       loadCursor(32512), // IDC_ARROW
		lpszClassName: className,
	}
	if atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		ready <- fmt.Errorf("overlay: RegisterClassExW: %w", callErr)
		return
	}

	screenW, _, _ := procGetSystemMetrics.Call(smCxScreen)
	screenH, _, _ := procGetSystemMetrics.Call(smCyScreen)
	o.posX = (int32(screenW) - int32(o.w)) / 2
	o.posY = (int32(screenH) - int32(o.h)) / 2

	hwnd, _, callErr := procCreateWindowExW.Call(
		wsExLayered|wsExTopmost|wsExToolWindow,
		uintptr(unsafe.Pointer(className)),
		0, 0, // no title, no menu: it belongs on the desktop, not in the taskbar
		uintptr(o.posX), uintptr(o.posY), uintptr(o.w), uintptr(o.h),
		0, 0, hInst, 0,
	)
	if hwnd == 0 {
		ready <- fmt.Errorf("overlay: CreateWindowExW: %w", callErr)
		return
	}
	o.hwnd = hwnd

	if err := o.createSurface(); err != nil {
		ready <- err
		return
	}
	procShowWindow.Call(hwnd, swShowNoActivate)
	ready <- nil

	var shown uint64
	for {
		o.pumpMessages()

		o.mu.Lock()
		closed := o.closed
		if !closed && o.seq != shown {
			shown = o.seq
			o.blit()
		}
		o.mu.Unlock()
		if closed {
			break
		}
		time.Sleep(4 * time.Millisecond)
	}
	o.destroySurface()
	procDestroyWindow.Call(hwnd)
	o.pumpMessages()
}

func loadCursor(id uintptr) windows.Handle {
	h, _, _ := procLoadCursorW.Call(0, id)
	return windows.Handle(h)
}

func (o *overlay) createSurface() error {
	screenDC, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screenDC)

	memDC, _, _ := procCreateCompatibleDC.Call(screenDC)
	if memDC == 0 {
		return fmt.Errorf("overlay: CreateCompatibleDC failed")
	}
	o.memDC = memDC

	bmi := bitmapInfoHeader{
		biSize:        uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		biWidth:       int32(o.w),
		biHeight:      -int32(o.h), // top-down, matching Ebitengine's pixel order
		biPlanes:      1,
		biBitCount:    32,
		biCompression: biRGB,
	}
	var bits unsafe.Pointer
	bmp, _, _ := procCreateDIBSection.Call(screenDC, uintptr(unsafe.Pointer(&bmi)), dibRGBColors,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == nil {
		return fmt.Errorf("overlay: CreateDIBSection failed")
	}
	o.bitmap = bmp
	procSelectObject.Call(memDC, bmp)
	o.pix = unsafe.Slice((*byte)(bits), o.w*o.h*4)
	return nil
}

func (o *overlay) destroySurface() {
	if o.memDC != 0 {
		procDeleteDC.Call(o.memDC)
	}
	if o.bitmap != 0 {
		procDeleteObject.Call(o.bitmap)
	}
}

// present draws a frame offscreen, converts it to the layout the layered window
// wants and hands it over. The screen Ebitengine offers is not used: the creature
// is shown through our own window.
func (o *overlay) present(_ *ebiten.Image, draw func(*ebiten.Image)) {
	o.frame.Clear()
	draw(o.frame)
	o.withPixels(func(pix []byte) {
		o.frame.ReadPixels(pix) // RGBA, premultiplied
		swizzleToBGRA(pix)
	})
}

// blit pushes the current surface to the screen.
func (o *overlay) blit() {
	screenDC, _, _ := procGetDC.Call(0)
	defer procReleaseDC.Call(0, screenDC)

	dst := point{o.posX, o.posY}
	src := point{0, 0}
	size := winSize{int32(o.w), int32(o.h)}
	blend := blendFunction{blendOp: acSrcOver, sourceConstantAlpha: 255, alphaFormat: acSrcAlpha}
	procUpdateLayeredWindow.Call(o.hwnd, screenDC,
		uintptr(unsafe.Pointer(&dst)), uintptr(unsafe.Pointer(&size)),
		o.memDC, uintptr(unsafe.Pointer(&src)), 0,
		uintptr(unsafe.Pointer(&blend)), ulwAlpha)
}

func (o *overlay) pumpMessages() {
	var m winMsg
	for {
		r, _, _ := procPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmRemove)
		if r == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// withPixels locks the surface and hands over its raw BGRA buffer. The game
// writes one frame in there; the overlay thread presents it afterwards.
func (o *overlay) withPixels(fn func(pix []byte)) {
	o.mu.Lock()
	defer o.mu.Unlock()
	fn(o.pix)
	o.seq++
}

// events are the shortcuts and wheel clicks since the last call.
func (o *overlay) events() []overlayEvent {
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.queued) == 0 {
		return nil
	}
	out := o.queued
	o.queued = nil
	return out
}

func (o *overlay) pushEvent(e overlayEvent) {
	o.mu.Lock()
	o.queued = append(o.queued, e)
	o.mu.Unlock()
}

func (o *overlay) move(x, y int32) {
	o.mu.Lock()
	o.posX, o.posY = x, y
	o.mu.Unlock()
	procSetWindowPos.Call(o.hwnd, hwndTopmost, uintptr(x), uintptr(y), 0, 0, swpNoSize|swpNoActivate)
}

func (o *overlay) topmost(on bool) {
	o.mu.Lock()
	o.isTopmost = on
	o.mu.Unlock()
	after := hwndTopmost
	if !on {
		after = hwndNotTopmost
	}
	procSetWindowPos.Call(o.hwnd, after, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
}

// focused reports whether the overlay window itself is the active window, which
// is when its keyboard shortcuts are live.
func (o *overlay) focused() bool {
	fg, _, _ := procGetForegroundWindow.Call()
	return fg == o.hwnd
}

func (o *overlay) floating() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.isTopmost
}

// screenSize is the desktop's size in pixels.
func (o *overlay) screenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCxScreen)
	h, _, _ := procGetSystemMetrics.Call(smCyScreen)
	return int(w), int(h)
}

// rect reports where the overlay sits on the desktop.
func (o *overlay) rect() (x, y, w, h int32) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.posX, o.posY, int32(o.w), int32(o.h)
}

// frameStats counts how much of the surface is actually drawn, which is the
// quickest way to tell an empty frame from an invisible window.
func (o *overlay) frameStats() (opaque, partial, transparent int, ok bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for i := 3; i < len(o.pix); i += 4 {
		switch a := o.pix[i]; {
		case a == 0:
			transparent++
		case a == 255:
			opaque++
		default:
			partial++
		}
	}
	return opaque, partial, transparent, true
}

// cursor returns the pointer in overlay-local pixels and whether it is inside
// the overlay's rectangle. Positions outside are still reported, so the creature
// can watch the pointer anywhere on screen.
func (o *overlay) cursor() (Vec, bool) {
	var p point
	if r, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); r == 0 {
		return Vec{}, false
	}
	x, y, w, h := o.rect()
	local := V(float64(p.x-x), float64(p.y-y))
	return local, local.X >= 0 && local.Y >= 0 && local.X < float64(w) && local.Y < float64(h)
}

func (o *overlay) close() {
	o.mu.Lock()
	already := o.closed
	o.closed = true
	o.mu.Unlock()
	if !already {
		procDestroyWindow.Call(o.hwnd)
	}
}

// shortcutFor maps a virtual key to a shortcut the creature knows.
func shortcutFor(vk uintptr) keyCode {
	switch vk {
	case vkEscape:
		return keyEscape
	case vkM:
		return keyMute
	case vkF:
		return keyTopmost
	}
	return keyNone
}

func overlayWndProc(hwnd, msg, wParam, lParam uintptr) uintptr {
	o := theOverlay.Load()
	if o != nil && o.hwnd != hwnd {
		o = nil
	}

	switch msg {
	case wmDestroy:
		return 0
	case wmEraseBkgnd:
		return 1
	case wmNcHitTest:
		if o != nil {
			x := int32(int16(lParam & 0xffff))
			y := int32(int16((lParam >> 16) & 0xffff))
			ox, oy, w, h := o.rect()
			lx, ly := x-ox, y-oy
			if lx < 0 || ly < 0 || lx >= w || ly >= h {
				return htTransparent
			}
			// Transparent pixels belong to whatever is underneath, so the desktop
			// stays usable right up to the creature's legs.
			o.mu.Lock()
			alpha := byte(0)
			if o.pix != nil && int(ly)*o.w+int(lx) < o.w*o.h {
				alpha = o.pix[(int(ly)*o.w+int(lx))*4+3]
			}
			o.mu.Unlock()
			if alpha < 8 {
				return htTransparent
			}
		}
		return htClient
	case wmLButtonDown:
		if o != nil {
			procSetForegroundWindow.Call(hwnd)
		}
		return 0
	case wmKeyDown, wmSysKeyDown:
		if o != nil {
			if k := shortcutFor(wParam); k != keyNone {
				o.pushEvent(overlayEvent{key: k})
			}
		}
		return 0
	case wmMouseWheel:
		if o != nil {
			o.pushEvent(overlayEvent{wheel: int(int16((wParam>>16)&0xffff)) / 120})
		}
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(hwnd, msg, wParam, lParam)
	return r
}
