# tangly

[![Built with DeepSeek](https://img.shields.io/badge/built_with-DeepSeek-4D6BFE)](https://deepseek.com)

A ten-legged creature that lives on your desktop. It walks to where you click,
trails silk, makes very small noises, and can be taken apart and rebuilt live
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
| shift + drag anywhere in its corner | move that corner (and the creature with it) |
| right-click on it | quit |
| Esc | quit (when its window has focus) |
| wheel over it | volume |
| M / F | mute / always-on-top (when its window has focus) |

The creature roams a 720×540 patch of desktop. Shift+drag moves the patch.

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

**Rendering and presentation are separate.** Ebitengine draws the creature into
an offscreen image; a layered Win32 window with per-pixel alpha presents it
(`UpdateLayeredWindow`). That split is deliberate: Ebitengine can only present a
transparent window through DXGI composition, and on machines where that is
unavailable its window is created with a redirection surface and the alpha is
discarded — you get an opaque rectangle. The creature does not care; it is
rendered the same either way.

**The creature is a constraint system.** A Verlet solver with distance and rope
constraints drives everything: the silk is a rope that slips loose rather than
tethering, each hip is tied to the body by two compliant links, and the body
itself is kinematic so it can walk out from under its own feet. Each leg is a
three-bone chain solved exactly by inverse kinematics rather than iterated, so a
leg can never be asked to reach further than its own bones, and a foot that
would be is clamped and steps instead.

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
| `creature.go` | body, legs, silk, gait, behaviour |
| `solver.go` | particles, constraints, the world step |
| `render.go` | straight jointed legs, rectangular body, scriptable palette |
| `audio.go` | the four voices |
| `overlay_windows.go` | the layered window and global input |
| `script.go` | the JavaScript prompt (goja — pure Go, no cgo) |
| `tangly.js` | the creature's definition |

## Requirements

Go 1.22 or newer, on Windows. The window layer is implemented against Win32
(`overlay_windows.go`) and the program therefore only builds there today; the
model, the renderer and the REPL are portable, so another presenter could be
added behind the same interface.

## Tests

```
go test ./...
```

They cover the things that actually broke while building it: walking to a
clicked point with a sane cadence, keeping a grip (never more than two legs
airborne mid-walk), staying put when idle, scrambling and settling when dragged,
every leg taking part in a walk, evenly spaced legs at rest, a leg's bones being
able to span its length, and the script API round-tripping a definition.

## License

MIT — see [LICENSE](LICENSE).
