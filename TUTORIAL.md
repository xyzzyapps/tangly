# The tangly REPL

A guided tour of driving the creature from the prompt. If you have used AutoLISP
in AutoCAD, or a Lisp image, this is the same idea: the thing on your desktop is
the live object, and you build it by calling functions at it.

Start the pet from a terminal:

```
go build -o tangly.exe .
./tangly.exe
```

You get a window-less creature on the desktop and a prompt in that terminal:

```
tangly awake: 10 legs, 9 silk strands, body 32x22
tangly repl: type help() for the API, or paste JavaScript
tangly>
```

Results are printed back as JSON, because almost everything here returns a
structure rather than a sentence.

---

## 1. Freeze it and look at it

```
tangly> tangly.pause()
tangly> tangly.step(120)          // advance two seconds while frozen
tangly> tangly.where()
{
  "airborne": 1,
  "angle": 222.05,
  "legs": 10,
  "planted": 9,
  "steps": 24,
  "x": 360.0,
  "y": 270.0
}
```

`pause` freezes the simulation, `step(n)` advances it exactly `n` ticks, so you
can watch a pose evolve a frame at a time. `tangly.where()` is the body's state;
`tangly.legs()` is the whole stance:

```
tangly> tangly.legs()
[
  { "index": 0, "angle": 18, "reach": 126, "limit": 104.6,
    "stance": 0.88, "steps": 3, "offLine": 1.4,
    "planted": true, "airborne": false },
  ...
]
```

- `angle` is where the leg points on the body, in degrees from the head.
- `reach` is the length you asked for; `limit` is the longest span its three
  bones can actually cover. If those two disagree badly, the leg is drawn bent.
- `stance` is where the foot is now, as a fraction of `limit`.
- `offLine` is how far the foot has drifted out of its own direction — this is
  what the resting gait corrects, so it should sit near zero when it is standing.
- `steps` counts how often that leg has stepped, which is the quickest way to
  see whether a leg is walking or only being dragged.

`tangly.resume()` starts it moving again.

---

## 2. Build one from nothing

`clear()` strips the creature back to its body; everything after that is yours.

```
tangly> tangly.pause()
tangly> tangly.clear()
tangly> tangly.body({length: 20, width: 8})       // half-extents, in pixels
tangly> tangly.leg({angle: 30, reach: 150})
tangly> tangly.leg({angle: 150, reach: 150})
tangly> tangly.leg({angle: -30, reach: 150})
tangly> tangly.leg({angle: -150, reach: 150})
tangly> tangly.silk({count: 0})
tangly> tangly.stand()
```

`leg()` adds one leg at a time, live: the angle is a direction around the body
and the reach is how long you want it. `stand()` puts every foot straight out
into its own direction, so you see the new shape immediately rather than waiting
for it to settle. A four-legged thing facing sideways is a good way to convince
yourself the body is a rectangle.

To replace the whole set at once — which is what `tangly.js` does — pass an
array:

```
tangly> tangly.legs([{angle: 20, reach: 130}, {angle: -20, reach: 130},
                     {angle: 160, reach: 130}, {angle: -160, reach: 130}])
```

`reset()` puts the shipped creature back. `def()` reads the current definition
out as an object, which is what `save()` writes to `tangly.js`.

---

## 3. Change how it looks

```
tangly> tangly.color('legCore', [255, 210, 80, 255])
tangly> tangly.color('legThread', [255, 110, 20, 200])
tangly> tangly.color('bodyFill', [40, 20, 20, 240])
tangly> tangly.color('bodyEye', [255, 80, 80, 255])
tangly> tangly.pen({leg: 2, joint: 3, halo: 6})
tangly> tangly.ring(10)               // the foot rings
tangly> tangly.feet(true)             // draw them at all (off by default)
```

Colours are straight RGBA. The parts are `legHalo`, `legBloom`, `legCore`,
`legThread`, `knee`, `ring`, `ringBloom`, `ringRim`, `bodyFill`, `bodyEdge`,
`bodyDot`, `bodyEye` and `silk`. `pen` takes any of `halo`, `bloom`, `leg`,
`thread`, `joint`, `ring`, `body`, `silk` — widths in pixels. An unknown part or
key is ignored, so a typo will just do nothing.

---

## 4. Change how it sounds

Four voices, all synthesised at startup: `step`, `chirp`, `settle`, `chatter`.

```
tangly> tangly.voices()                            // what they are now
tangly> tangly.play('chirp')                       // audition one
tangly> tangly.sound('step', {pitch: 2.5, volume: 0.5, minGap: 1})
tangly> tangly.sound('step', {pitch: 1, decay: 4})  // very short tick
tangly> tangly.sound('chatter', {pitch: 0.5, decay: 0.3})
tangly> tangly.mute(true)
```

`pitch` multiplies the frequency, `decay` how fast it dies away (bigger is
shorter), `minGap` the number of ticks between triggers. Rebuilding a voice is
instant, so this is a cheap thing to fiddle with while the creature walks about.

---

## 5. Change how it behaves

```
tangly> tangly.gait({speed: 160, agility: 10, turnRate: 6})
tangly> tangly.moveTo(120, 120)          // walk there
tangly> tangly.place(500, 400)           // or just be there
tangly> tangly.angle(90)                 // face east
tangly> tangly.stand()                   // re-plant every foot at once
tangly> tangly.scurry()                  // startle it for a moment
tangly> tangly.watch(false)              // stop it following the pointer
tangly> tangly.wander(true)              // let it potter about on its own
tangly> tangly.wander({every: 5, chance: 0.9, radius: 40, radiusMax: 120, speed: 0.3})
```

- `gait` takes `speed`, `agility` (how hard it accelerates), `turnRate` (radians
  per second), `arrive` (how close counts as arrived), `drag` (how strongly taut
  legs hold it back) and `shinBend` (the interior angle at the second knee, in
  radians — lower is more bent).
- `wander` with no argument toggles it; with an object it turns it on and
  configures it. It is **off** by default: a standing creature should stand.
- `watch` controls whether the head turns to follow the pointer when it is over
  the creature's corner of the desktop.
- `settle(seconds)` sets how long it spends stepping its feet back into line
  after a walk.

---

## 6. Keep what you like

```
tangly> tangly.save()          // writes tangly.js from the live creature
tangly> tangly.load()          // and reads it back
```

`tangly.js` reloads by itself whenever its timestamp changes, so the other half
of the workflow is simply: leave the pet running, open `tangly.js` in an editor,
change a colour or a leg, save, and watch it come back different. Nothing is
lost by editing it by hand — it is an ordinary script of the same calls.

---

## 7. Going lower

The API is layered, so you can work at whatever level the question is at:

| layer | calls |
| --- | --- |
| simulation | `pause` `resume` `step` `iters` `particles` `tps` |
| pose | `place` `angle` `stand` `settle` |
| body and legs | `body` `leg` `legs` `silk` `clear` `reset` |
| behaviour | `wander` `watch` `scurry` `gait` `moveTo` `grab` `drop` |
| look | `color` `pen` `ring` `feet` |
| sound | `sound` `voices` `play` `mute` `volume` |
| window | `window` `screen` `topmost` `frame` |
| inspection | `def` `where` `legs` `frame` `help` |

`iters(n)` is the number of relaxation passes the constraint solver gets per
tick — drop it to 3 and watch the silk go slack and rubbery, raise it to 40 and
the hips hold tighter. `particles()` counts what the solver is carrying.

`frame()` reports how much of the surface is actually drawn plus the overlay's
rectangle — the fastest way to tell "nothing is drawn" from "the window is
somewhere I am not looking":

```
tangly> tangly.frame()
{ "opaque": 6837, "partial": 2298, "transparent": 379665,
  "window": { "x": 440, "y": 180, "w": 720, "h": 540 } }
```

`screen()` is the desktop size, and `window({x, y})` moves the creature's corner
of it.

---

## Troubleshooting

**No prompt.** It only appears when stdin is a terminal; launched from a file
manager you get the creature and no prompt. Run it from a console.

**The creature is not where I left it.** It roams a 720×540 patch. Shift+drag
moves the patch, and `tangly.window({x: 100, y: 100})` moves it from the prompt.
If you have lost it entirely, `tangly.frame()` gives you the rectangle.

**It is behind another window.** `tangly.topmost(true)`. Fullscreen applications
can still cover it.

**It will not stop moving.** `tangly.wander(false)`, and `tangly.watch(false)`
if it is turning to follow your pointer. Standing still it should take no steps
at all: `tangly.legs()` will show you if something is cycling.

**No sound.** `tangly.mute()` reports the state, `tangly.volume(0.2)` sets it.
The creature is deliberately quiet, and it only plays while the pointer is over
its corner of the desktop or its window has focus.

**It quit on me.** Esc or a right-click on its body does that. Both need the
creature itself, so an idle desktop will not do it by accident.
