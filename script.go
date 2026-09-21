package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"
	"github.com/hajimehoshi/ebiten/v2"
)

// scriptHost embeds a JavaScript engine (goja, pure Go, no cgo) and exposes the
// creature to it, AutoLISP style: everything the pet is made of can be built,
// inspected and changed by typing at a prompt while it runs.
//
// Evaluations are handed to the game loop, so a script mutates the live creature
// on the same goroutine that simulates it and the result shows up on the desktop
// immediately.
type scriptHost struct {
	game *game
	vm   *goja.Runtime

	mu      sync.Mutex
	queue   []evalRequest
	results chan string

	scriptPath string
	scriptMod  time.Time
}

type evalRequest struct {
	src  string
	from string // "repl" or "file", for the log
}

// newScriptHost builds the engine and its API, and starts reading the prompt.
func newScriptHost(g *game, path string) *scriptHost {
	h := &scriptHost{
		game:       g,
		vm:         goja.New(),
		results:    make(chan string, 64),
		scriptPath: path,
	}
	h.vm.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	h.install()

	// Load the script once, before the prompt starts, and remember its timestamp so
	// the watcher only fires on later edits: otherwise a command typed at startup
	// would be overwritten by the first poll.
	if src, err := os.ReadFile(path); err == nil {
		h.submit(string(src), "file")
	} else if st, err := os.Stat(path); err == nil {
		h.scriptMod = st.ModTime()
	}
	if st, err := os.Stat(path); err == nil {
		h.scriptMod = st.ModTime()
	}

	go h.repl()
	go h.watchFile()
	return h
}

// evaluate runs a snippet on the game goroutine and returns what it produced.
func (h *scriptHost) evaluate(src string) string {
	v, err := h.vm.RunString(src)
	if err != nil {
		return fmt.Sprintf("! %v", err)
	}
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	// Maps and slices read better as JSON at the prompt.
	switch reflect.ValueOf(v.Export()).Kind() {
	case reflect.Map, reflect.Slice, reflect.Struct:
		if b, err := json.MarshalIndent(v.Export(), "", "  "); err == nil {
			return string(b)
		}
	}
	return v.String()
}

// submit queues a snippet for the game loop.
func (h *scriptHost) submit(src, from string) {
	h.mu.Lock()
	h.queue = append(h.queue, evalRequest{src: src, from: from})
	h.mu.Unlock()
}

// drain evaluates everything queued since the last tick. Called from Update.
func (h *scriptHost) drain() {
	h.mu.Lock()
	pending := h.queue
	h.queue = nil
	h.mu.Unlock()

	for _, req := range pending {
		out := h.evaluate(req.src)
		if req.from == "file" {
			if strings.HasPrefix(out, "! ") {
				log.Printf("tangly.js: %s", strings.TrimPrefix(out, "! "))
			} else {
				log.Printf("tangly.js reloaded (%s)", h.creature().summary())
			}
			continue
		}
		select {
		case h.results <- out:
		default:
		}
	}
}

func (h *scriptHost) creature() *creature { return h.game.c }

// repl reads the prompt: one snippet per line, result printed back.
func (h *scriptHost) repl() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	fmt.Println("tangly repl: type help() for the API, or paste JavaScript")
	for {
		fmt.Print("tangly> ")
		if !in.Scan() {
			return
		}
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		if line == "exit" || line == "quit" {
			h.game.requestQuit()
			return
		}
		h.submit(line, "repl")
		select {
		case out := <-h.results:
			if out != "" {
				fmt.Println(out)
			}
		case <-time.After(2 * time.Second):
			fmt.Println("! no answer from the game loop")
		}
	}
}

// watchFile reloads tangly.js whenever it changes on disk, so a creature can be
// edited in an editor as well as at the prompt.
func (h *scriptHost) watchFile() {
	for {
		time.Sleep(400 * time.Millisecond)
		st, err := os.Stat(h.scriptPath)
		if err != nil {
			continue
		}
		if st.ModTime().Equal(h.scriptMod) {
			continue
		}
		h.scriptMod = st.ModTime()
		src, err := os.ReadFile(h.scriptPath)
		if err != nil {
			continue
		}
		h.submit(string(src), "file")
	}
}

// saveScript writes the current creature out as a script, ready to reload.
func (h *scriptHost) saveScript() error {
	c := h.creature()
	var b strings.Builder
	b.WriteString("// The creature, as it stands. Edit it, or reload with tangly.load().\n")
	fmt.Fprintf(&b, "tangly.body({length: %g, width: %g});\n", c.bodyLength, c.bodyWidth)
	b.WriteString("tangly.legs([\n")
	for _, lg := range c.legs {
		fmt.Fprintf(&b, "  {angle: %g, reach: %g},\n", lg.spec.angle, lg.spec.reach)
	}
	b.WriteString("]);\n")
	fmt.Fprintf(&b, "tangly.gait({speed: %g, agility: %g, turnRate: %g, arrive: %g});\n",
		c.maxSpeed, c.agility, c.turnRate, c.arriveRadius)
	return os.WriteFile(h.scriptPath, []byte(b.String()), 0o644)
}

// install publishes the spider object and the small helpers a session needs.
func (h *scriptHost) install() {
	vm := h.vm
	tangly := vm.NewObject()

	// -- structure ---------------------------------------------------------
	must(tangly.Set("clear", func(goja.FunctionCall) goja.Value {
		d := h.creature().def()
		d.Legs = nil
		h.game.rebuild(func(c *creature) { c.applyDef(d) })
		return vm.ToValue(h.creature().summary())
	}))

	must(tangly.Set("reset", func(goja.FunctionCall) goja.Value {
		h.game.rebuild(func(c *creature) { c.applyDef(defaultDef()) })
		return vm.ToValue(h.creature().summary())
	}))

	must(tangly.Set("body", func(call goja.FunctionCall) goja.Value {
		d := h.creature().def()
		switch v := call.Argument(0); {
		case v != nil && !goja.IsUndefined(v) && v.ExportType() != nil && v.ExportType().Kind() == reflect.Map:
			// {length, width} for a rectangle; a bare number gives a square.
			obj := v.ToObject(vm)
			d.BodyLength = numField(vm, obj, "length", d.BodyLength)
			d.BodyWidth = numField(vm, obj, "width", d.BodyWidth)
		default:
			half := floatArg(call, 0, d.BodyLength)
			d.BodyLength, d.BodyWidth = half, half
		}
		h.game.rebuild(func(c *creature) { c.applyDef(d) })
		return vm.ToValue(h.creature().summary())
	}))

	must(tangly.Set("leg", func(call goja.FunctionCall) goja.Value {
		lg := legFromObject(vm, call.Argument(0))
		if lg.Reach <= 0 {
			lg.Reach = 120
		}
		c := h.creature()
		c.addLeg(lg)
		return vm.ToValue(c.summary())
	}))

	must(tangly.Set("legs", func(call goja.FunctionCall) goja.Value {
		if v := call.Argument(0); goja.IsUndefined(v) || goja.IsNull(v) {
			// Called with nothing, it reads the legs back instead of replacing them.
			return vm.ToValue(h.creature().LegStates())
		}
		d := h.creature().def()
		d.Legs = legsFromArray(vm, call.Argument(0))
		h.game.rebuild(func(c *creature) { c.applyDef(d) })
		return vm.ToValue(h.creature().summary())
	}))

	// -- look and behaviour ------------------------------------------------
	must(tangly.Set("color", func(call goja.FunctionCall) goja.Value {
		part := call.Argument(0).String()
		r, g, b, a := colorArgs(vm, call.Argument(1))
		if !setStyleColor(&h.game.paint.st, part, r, g, b, a) {
			return vm.ToValue("unknown part: " + part)
		}
		return vm.ToValue(part + " recoloured")
	}))

	must(tangly.Set("feet", func(call goja.FunctionCall) goja.Value {
		on := true
		if v := call.Argument(0); v != nil && !goja.IsUndefined(v) {
			on = v.ToBoolean()
		}
		h.game.paint.feet = on
		return vm.ToValue(on)
	}))

	must(tangly.Set("gait", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(vm)
		c := h.creature()
		c.maxSpeed = numField(vm, obj, "speed", c.maxSpeed)
		c.agility = numField(vm, obj, "agility", c.agility)
		c.turnRate = numField(vm, obj, "turnRate", c.turnRate)
		c.arriveRadius = numField(vm, obj, "arrive", c.arriveRadius)
		c.dragGain = numField(vm, obj, "drag", c.dragGain)
		c.shinBend = numField(vm, obj, "shinBend", c.shinBend)
		return vm.ToValue("gait updated")
	}))

	must(tangly.Set("sound", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		obj := call.Argument(1).ToObject(vm)
		spec := soundSpec{
			Volume: numField(vm, obj, "volume", 0.25),
			Pitch:  numField(vm, obj, "pitch", 1),
			Decay:  numField(vm, obj, "decay", 1),
			MinGap: int(numField(vm, obj, "minGap", 4)),
		}
		if !h.game.bank.rebuild(name, spec) {
			return vm.ToValue("unknown voice: " + name)
		}
		return vm.ToValue(name + " rebuilt")
	}))

	must(tangly.Set("play", func(call goja.FunctionCall) goja.Value {
		name := call.Argument(0).String()
		return vm.ToValue(h.game.bank.playNamed(name, floatArg(call, 1, 1)))
	}))

	// -- interaction -------------------------------------------------------
	must(tangly.Set("moveTo", func(call goja.FunctionCall) goja.Value {
		h.creature().SetTarget(V(floatArg(call, 0, windowW/2), floatArg(call, 1, windowH/2)))
		return vm.ToValue("walking")
	}))

	must(tangly.Set("grab", func(call goja.FunctionCall) goja.Value {
		c := h.creature()
		c.BeginDrag(V(floatArg(call, 0, c.pos.X), floatArg(call, 1, c.pos.Y)))
		return vm.ToValue("held")
	}))

	must(tangly.Set("drop", func(goja.FunctionCall) goja.Value {
		h.creature().EndDrag()
		return vm.ToValue("released")
	}))

	must(tangly.Set("window", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(vm)
		x := int32(numField(vm, obj, "x", 0))
		y := int32(numField(vm, obj, "y", 0))
		h.game.p.move(x, y)
		return vm.ToValue("moved")
	}))

	// -- inspection --------------------------------------------------------
	must(tangly.Set("def", func(goja.FunctionCall) goja.Value {
		d := h.creature().def()
		legs := make([]any, 0, len(d.Legs))
		for _, ld := range d.Legs {
			legs = append(legs, map[string]any{"angle": ld.Angle, "reach": ld.Reach})
		}
		return vm.ToValue(map[string]any{
			"bodyLength": d.BodyLength,
			"bodyWidth":  d.BodyWidth,
			"legs":       legs,
		})
	}))

	must(tangly.Set("where", func(goja.FunctionCall) goja.Value {
		c := h.creature()
		return vm.ToValue(map[string]any{
			"x": c.pos.X, "y": c.pos.Y,
			"angle": c.angle * 180 / 3.141592653589793,
			"legs":  len(c.legs), "steps": c.stepCount,
			"airborne": c.airCount(), "planted": c.plantedCount(),
		})
	}))

	must(tangly.Set("save", func(goja.FunctionCall) goja.Value {
		if err := h.saveScript(); err != nil {
			return vm.ToValue("! " + err.Error())
		}
		return vm.ToValue("saved " + h.scriptPath)
	}))

	must(tangly.Set("load", func(goja.FunctionCall) goja.Value {
		src, err := os.ReadFile(h.scriptPath)
		if err != nil {
			return vm.ToValue("! " + err.Error())
		}
		h.submit(string(src), "file")
		return vm.ToValue("loading " + h.scriptPath)
	}))

	// -- simulation --------------------------------------------------------
	must(tangly.Set("pause", func(goja.FunctionCall) goja.Value {
		h.game.paused = true
		return vm.ToValue("paused")
	}))

	must(tangly.Set("resume", func(goja.FunctionCall) goja.Value {
		h.game.paused = false
		h.game.step = 0
		return vm.ToValue("running")
	}))

	must(tangly.Set("step", func(call goja.FunctionCall) goja.Value {
		n := int(floatArg(call, 0, 1))
		h.game.paused = true
		h.game.step += n
		return vm.ToValue(fmt.Sprintf("stepping %d ticks", n))
	}))

	must(tangly.Set("iters", func(call goja.FunctionCall) goja.Value {
		n := int(floatArg(call, 0, solverIters))
		if n < 1 {
			n = 1
		}
		h.creature().w.iters = n
		return vm.ToValue(n)
	}))

	must(tangly.Set("particles", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(len(h.creature().w.parts))
	}))

	must(tangly.Set("tps", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(ebiten.TPS())
	}))

	// -- pose --------------------------------------------------------------
	must(tangly.Set("place", func(call goja.FunctionCall) goja.Value {
		h.creature().Place(V(floatArg(call, 0, windowW/2), floatArg(call, 1, windowH/2)))
		return vm.ToValue("placed")
	}))

	must(tangly.Set("angle", func(call goja.FunctionCall) goja.Value {
		h.creature().Face(floatArg(call, 0, 0))
		return vm.ToValue("facing")
	}))

	must(tangly.Set("stand", func(goja.FunctionCall) goja.Value {
		h.creature().Stand()
		return vm.ToValue("standing")
	}))

	must(tangly.Set("follow", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(vm)
		c := h.creature()
		c.legFollow = math.Max(0, numField(vm, obj, "legs", c.legFollow))
		c.bodyFollow = math.Max(0, numField(vm, obj, "body", c.bodyFollow))
		return vm.ToValue(map[string]any{"legs": c.legFollow, "body": c.bodyFollow})
	}))

	must(tangly.Set("settle", func(call goja.FunctionCall) goja.Value {
		v := floatArg(call, 0, settleFor)
		h.creature().settleTimer = v
		return vm.ToValue(v)
	}))

	must(tangly.Set("wander", func(call goja.FunctionCall) goja.Value {
		c := h.creature()
		v := call.Argument(0)
		switch {
		case v == nil || goja.IsUndefined(v):
			c.wanderOn = !c.wanderOn
		case v.Export() != nil && v.ToObject(vm) != nil && v.ExportType() != nil && v.ExportType().Kind() == reflect.Map:
			obj := v.ToObject(vm)
			c.wanderOn = true
			c.wanderEvery = [2]float64{
				numField(vm, obj, "every", c.wanderEvery[0]),
				numField(vm, obj, "everyMax", c.wanderEvery[1]),
			}
			c.wanderChance = numField(vm, obj, "chance", c.wanderChance)
			c.wanderRadius = [2]float64{
				numField(vm, obj, "radius", c.wanderRadius[0]),
				numField(vm, obj, "radiusMax", c.wanderRadius[1]),
			}
			c.wanderSpeed = numField(vm, obj, "speed", c.wanderSpeed)
		default:
			c.wanderOn = v.ToBoolean()
		}
		return vm.ToValue(c.wanderOn)
	}))

	must(tangly.Set("watch", func(call goja.FunctionCall) goja.Value {
		c := h.creature()
		if v := call.Argument(0); v != nil && !goja.IsUndefined(v) {
			c.watching = v.ToBoolean()
		} else {
			c.watching = !c.watching
		}
		return vm.ToValue(c.watching)
	}))

	must(tangly.Set("scurry", func(goja.FunctionCall) goja.Value {
		h.creature().scurry = 0.5
		return vm.ToValue("startled")
	}))

	// -- look --------------------------------------------------------------
	must(tangly.Set("ring", func(call goja.FunctionCall) goja.Value {
		h.game.paint.st.ringRadius = float32(floatArg(call, 0, 7.4))
		return vm.ToValue(h.game.paint.st.ringRadius)
	}))

	must(tangly.Set("pen", func(call goja.FunctionCall) goja.Value {
		obj := call.Argument(0).ToObject(vm)
		st := &h.game.paint.st
		set := func(name string, dst *float32, def float64) {
			*dst = float32(numField(vm, obj, name, float64(*dst)))
		}
		set("halo", &st.haloWidth, 4.6)
		set("bloom", &st.bloomWidth, 2.4)
		set("leg", &st.legWidth, 1.2)
		set("thread", &st.threadWidth, 0.8)
		set("joint", &st.jointRadius, 2.0)
		set("ring", &st.ringWidth, 1.8)
		set("body", &st.bodyWidth, 1.3)
		return vm.ToValue("pen updated")
	}))

	// -- sound -------------------------------------------------------------
	must(tangly.Set("voices", func(goja.FunctionCall) goja.Value {
		out := map[string]any{}
		for name, spec := range defaultVoices() {
			out[name] = map[string]any{
				"volume": spec.Volume, "pitch": spec.Pitch,
				"decay": spec.Decay, "minGap": spec.MinGap,
			}
		}
		return vm.ToValue(out)
	}))

	// -- window ------------------------------------------------------------
	must(tangly.Set("topmost", func(call goja.FunctionCall) goja.Value {
		on := true
		if v := call.Argument(0); v != nil && !goja.IsUndefined(v) {
			on = v.ToBoolean()
		}
		h.game.p.topmost(on)
		return vm.ToValue(on)
	}))

	must(tangly.Set("mute", func(call goja.FunctionCall) goja.Value {
		if v := call.Argument(0); v != nil && !goja.IsUndefined(v) {
			h.game.bank.muted = v.ToBoolean()
		} else {
			h.game.bank.muted = !h.game.bank.muted
		}
		return vm.ToValue(h.game.bank.muted)
	}))

	must(tangly.Set("volume", func(call goja.FunctionCall) goja.Value {
		return vm.ToValue(h.game.bank.setVolume(floatArg(call, 0, h.game.bank.master)))
	}))

	must(tangly.Set("frame", func(goja.FunctionCall) goja.Value {
		opaque, partial, transparent, ok := h.game.p.frameStats()
		x, y, w, hh := h.game.p.rect()
		out := map[string]any{"window": map[string]any{"x": x, "y": y, "w": w, "h": hh}}
		if ok {
			out["opaque"], out["partial"], out["transparent"] = opaque, partial, transparent
		}
		return vm.ToValue(out)
	}))

	must(tangly.Set("screen", func(goja.FunctionCall) goja.Value {
		w, h := h.game.p.screenSize()
		return vm.ToValue(map[string]any{"width": w, "height": h})
	}))

	must(tangly.Set("help", func(goja.FunctionCall) goja.Value {
		return vm.ToValue(helpText)
	}))

	must(vm.Set("tangly", tangly))
	must(vm.Set("help", func(goja.FunctionCall) goja.Value { return vm.ToValue(helpText) }))
}

const helpText = `-- simulation
tangly.pause()  tangly.resume()  tangly.step(n)   freeze, run, advance n ticks
tangly.iters(n)                       solver relaxation passes per tick
tangly.particles()  tangly.tps()      how big and how fast it is running

-- pose
tangly.place(x, y)  tangly.angle(deg) put the body somewhere, facing somewhere
tangly.stand()                        every foot straight back to its own spot
tangly.settle(seconds)                how long it re-settles after a walk
tangly.follow({legs, body})           how much the joints and shell trail

-- body and legs
tangly.body(half)                     square body, or body({length, width})
tangly.leg({angle, reach})            add one leg, angle in degrees from the head
tangly.legs([{angle, reach}, ...])    replace every leg
tangly.legs()                         read every leg back: stance, off-line, planted
tangly.clear()  tangly.reset()        strip it back, or rebuild the shipped creature

-- behaviour
tangly.wander(true|false)             idle pottering about, off by default
tangly.wander({every, everyMax, chance, radius, radiusMax, speed})
tangly.watch(true|false)              head follows the pointer over its corner
tangly.scurry()                       startle it
tangly.gait({speed, agility, turnRate, arrive, drag, shinBend})
tangly.moveTo(x, y)  tangly.grab(x, y)  tangly.drop()

-- look
tangly.color(part, [r,g,b,a])         part: legCore, legBloom, legThread, legHalo,
                                      knee, ring, ringBloom, ringRim, bodyFill,
                                      bodyEdge, bodyDot, bodyEye
tangly.pen({halo, bloom, leg, thread, joint, ring, body, silk})
tangly.ring(radius)  tangly.feet(true|false)

-- sound
tangly.sound(name, {volume, pitch, decay, minGap})
                                      name: step, chirp, settle, chatter
tangly.voices()  tangly.play(name)  tangly.mute(true|false)  tangly.volume(v)

-- window
tangly.window({x, y})  tangly.screen()  tangly.topmost(true|false)  tangly.frame()
tangly.def()  tangly.where()  tangly.save()  tangly.load()  tangly.help()
edit tangly.js and it reloads by itself; 'exit' quits`

func must(err error) {
	if err != nil {
		log.Fatalf("script: %v", err)
	}
}

func numField(vm *goja.Runtime, obj *goja.Object, name string, def float64) float64 {
	v := obj.Get(name)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return def
	}
	switch n := v.Export().(type) {
	case float64:
		return n
	case int64:
		return float64(n)
	}
	if f, err := strconv.ParseFloat(v.String(), 64); err == nil {
		return f
	}
	return def
}

func floatArg(call goja.FunctionCall, i int, def float64) float64 {
	v := call.Argument(i)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return def
	}
	if f, err := strconv.ParseFloat(v.String(), 64); err == nil {
		return f
	}
	return def
}

func legFromObject(vm *goja.Runtime, v goja.Value) legDef {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return legDef{}
	}
	obj := v.ToObject(vm)
	return legDef{
		Angle: numField(vm, obj, "angle", 0),
		Reach: numField(vm, obj, "reach", 120),
	}
}

func legsFromArray(vm *goja.Runtime, v goja.Value) []legDef {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	obj := v.ToObject(vm)
	length := int(obj.Get("length").ToInteger())
	out := make([]legDef, 0, length)
	for i := range length {
		out = append(out, legFromObject(vm, obj.Get(strconv.Itoa(i))))
	}
	return out
}

func colorArgs(vm *goja.Runtime, v goja.Value) (r, g, b, a uint8) {
	obj := v.ToObject(vm)
	get := func(i int, def float64) uint8 {
		x := numField(vm, obj, strconv.Itoa(i), def)
		return uint8(clampf(x, 0, 255))
	}
	return get(0, 255), get(1, 255), get(2, 255), get(3, 255)
}

func setStyleColor(st *style, part string, r, g, b, a uint8) bool {
	c := colorRGBA(r, g, b, a)
	switch part {
	case "legHalo":
		st.legHalo = c
	case "legBloom":
		st.legBloom = c
	case "legCore":
		st.legCore = c
	case "legThread":
		st.legThread = c
	case "knee":
		st.knee = c
	case "ring":
		st.ring = c
	case "ringBloom":
		st.ringBloom = c
	case "ringRim":
		st.ringRim = c
	case "bodyFill":
		st.bodyFill = c
	case "bodyEdge":
		st.bodyEdge = c
	case "bodyDot":
		st.bodyDot = c
	case "bodyEye":
		st.bodyEye = c
	default:
		return false
	}
	return true
}
