# tangly

[![Built with DeepSeek](https://img.shields.io/badge/built_with-DeepSeek-4D6BFE)](https://deepseek.com)

A eight-legged creature that lives on your desktop. It walks to where you click,
makes very small noises, and can be taken apart and rebuilt live
from a REPL while it is running.

No window frame, no black box: the creature is drawn straight onto the desktop
over whatever is behind it, and the desktop stays clickable right up to its legs.

```
go build -o tangly.exe .
./tangly.exe
```

Start it from a terminal — that terminal becomes the creature's prompt
(see [TUTORIAL.md](TUTORIAL.md)).

## Controls

| action | what happens |
| --- | --- |
| click inside its corner of the desktop | it walks there |
| drag its body | pick it up and carry it |
| right-click on it | quit |
| Esc | quit (when its window has focus) |
| wheel over it | volume |
| M / F | mute / always-on-top (when its window has focus) |

The creature roams a 720×540 patch of desktop; `tangly.window({x, y})` at the
prompt moves that patch.

## The REPL

`tangly.js` is the creature's definition and reloads the moment you save it.
The prompt talks to the same creature the moment you press enter:

```
tangly> tangly.pause()
tangly> tangly.step(60)
tangly> tangly.legs()                      // every leg: stance, off-line, steps
tangly> tangly.leg({angle: 200, reach: 150})
tangly> tangly.color('ring', [255, 60, 60, 255])
tangly> tangly.sound('step', {pitch: 2, decay: 0.6})
tangly> tangly.save()                      // write tangly.js back out
```

`help()` lists everything, grouped in layers from the simulation up:
simulation (pause/step/iters/particles) → pose → body and legs → behaviour →
look → sound → window. [TUTORIAL.md](TUTORIAL.md) walks through it.

## How it works

**Rendering and presentation are separate.** The game draws into whatever surface
the platform hands it, behind a small `presenter` interface. On Windows that is a
layered Win32 window of our own with per-pixel alpha (`UpdateLayeredWindow`),
because Ebitengine can only present a transparent window through DXGI
composition, and on machines where that is unavailable its window is created with
a redirection surface and the alpha is discarded — you get an opaque rectangle.
On macOS and on Linux with a compositor, Ebitengine's own transparent window is
used directly. The creature is drawn identically either way.

**The creature is a constraint system.** A Verlet solver with distance, rope and
angle constraints drives everything. The body is kinematic so the creature can walk
out from under its own feet, each hip is a point placed on the shell rather than a
simulated particle, and a planted foot is held exactly where it was put.

The legs follow verlet-js's spider: three bones held by three **angle
constraints** (its stiffnesses, 1.0, 0.4 and 0.9, translated into this solver's
terms), with the foot tethered to its target by a constraint of length zero --
the way that engine ties a foot to a node of its web. A step is nothing more than
moving the target and letting the leg spring after it, so the leg bends and
springs as it moves. The bones are projected back to their lengths at the end of
every tick, so the give shows up as the leg bending rather than as rubber.

**The gait is a cycle plus a trigger.** While walking, every leg takes its turn in
a staggered wave around the body, so all ten walk rather than only the ones the
direction of travel happens to stretch. Being stretched still makes a leg step
early. Standing still, the creature steps only to put a foot back in its own
slice of the fan, which keeps the rest pose evenly spread.

**Sound is synthesised**, not sampled: each voice is a few milliseconds of noise
and sine generated at startup, tiny by design, and rebuilt on demand by the
script.

## Layout

| file | role |
| --- | --- |
| `main.go` | game shell, input, window plumbing, the eval queue |
| `creature.go` | body, legs, gait, behaviour |
| `solver.go` | particles, constraints, the world step |
| `render.go` | straight jointed legs, rectangular body, scriptable palette |
| `audio.go` | the four voices |
| `presenter.go` | the interface the creature is shown through |
| `present_windows.go` | the layered window, per-pixel alpha, global input |
| `present_ebiten.go` | Ebitengine's own transparent window (macOS, Linux) |
| `script.go` | the JavaScript prompt (goja — pure Go, no cgo) |
| `tangly.js` | the creature's definition |

## Platform support

| platform | window | notes |
| --- | --- | --- |
| Windows | layered window of our own | per-pixel alpha, and transparent pixels pass the mouse through to the desktop |
| macOS | Ebitengine's window | transparent framebuffer; the whole window takes the mouse |
| Linux (X11, with a compositor) | Ebitengine's window | as above; without a compositor the window will be opaque |

Go 1.22 or newer. The build is cgo-free, so it cross-compiles:

```
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o tangly-mac .
CGO_ENABLED=0 GOOS=linux  GOARCH=amd64 go build -o tangly-linux .
```

Both of those compile from Windows, which is how they are checked. Only the
Windows build has been run — the macOS and Linux paths are written against the
documented behaviour of Ebitengine's transparent window, not verified on the
machines.

## Tests

```
go test ./...
```

They cover the things that actually broke while building it: walking to a
clicked point with a sane cadence, keeping a grip (never more than two legs
airborne mid-walk), staying put when idle, scrambling and settling when dragged,
every leg taking part in a walk, evenly spaced legs at rest, a leg's bones being
able to span its length, and the script API round-tripping a definition.

## Influences

**[verlet-js](https://github.com/subprotocol/verlet-js)** by Sub Protocol
([subprotocol.com](http://subprotocol.com/)), MIT — a small Verlet engine of
particles, distance/pin/angle constraints and composites, and the thing this
creature's legs are modelled on.

Two of its ideas are in the solver itself. Its frame loop scales every constraint
by `1/step`, so the iteration count affects only how well the solver converges
rather than how stiff everything is; tangly does the same (`solve` in
`solver.go`), which is why `iters` is a convergence knob and rigid constraints
stay rigid. Its `PinConstraint` is the same idea as a planted foot, and its
"relax, then bounds" loop is the shape of `world.step`.

Its **spider** is in
[`examples/spiderweb.html`](https://github.com/subprotocol/verlet-js/blob/master/examples/spiderweb.html)
(`VerletJS.prototype.spider` and `crawl`), not in `lib/`. It is a web-dweller:
the spider is dropped onto a net of pinned nodes under gravity, its three body
particles — head, thorax, abdomen — are held in line by an angle constraint, each
leg is four particles held by three angle constraints, and a step picks a free web
node inside that leg's quadrant and re-ties the foot to it with a rest-length-zero
distance constraint. The stride order is every third leg.

The legs here are that, adapted to a creature that walks instead of hanging:

- the `AngleConstraint` itself — see `hinge` and `solveHinge` in `solver.go`,
  which rotate `a`, `c` and `b` the way it does;
- three hinges a leg, with its stiffnesses 1.0, 0.4 and 0.9, converted into this
  solver's terms (it relaxes 16 times a frame with everything scaled by `1/step`,
  so its numbers are 1/16 per pass; this solver's are `16/solverIters`);
- the foot tethered to its target by a rest-length-zero constraint, as it ties a
  foot to a web node, so a foot is a particle rather than a placed dot;
- stepping by moving the target and letting the leg spring after it, rather than
  driving the foot along a scripted path;
- its stride order, every third leg, so consecutive steps are never on
  neighbouring legs.

What is not borrowed: the web and the gravity (this one walks about a desktop),
and the soft bones. A leg here has three bones that are projected back to their
lengths at the end of every tick, so the hinges give and the leg springs, but a
bone does not stretch into rubber.

## Known issues

**A leg can stretch during a walk.** Measured across eight seeds and three modes,
the walk reaches 1.42 times a leg's bone length with up to 24 px of give on a
leg about a hundred long, in one seed of eight. Idle is fine (0.93, and the bones
hold to a hundredth of a pixel), and feet land exactly where they are aimed in
every case. The likely cause is a hinged chain being asked to reach an aim while
it is still swinging -- the foot is carried to its target, and the hinges hold a
bent shape that the bones give up length to satisfy. `TestWalkMechanics` and the
bounds in `checkSane` record the current worst case, so anything worse fails.

## License

MIT — see [LICENSE](LICENSE).
