package main

import (
	"math"
	"math/rand/v2"
	"testing"
)

const testDt = 1.0 / 60

func testCreature(seed uint64) *creature {
	w := &world{bounds: rect{0, 0, windowW, windowH}, iters: 20}
	return newCreature(w, V(windowW/2, windowH/2), rand.New(rand.NewPCG(seed, 0x51de)))
}

// checkSane asserts the invariants the constraint system must never break.
func checkSane(t *testing.T, c *creature) {
	t.Helper()
	for i, p := range c.w.parts {
		if math.IsNaN(p.pos.X) || math.IsNaN(p.pos.Y) || math.IsInf(p.pos.X, 0) || math.IsInf(p.pos.Y, 0) {
			t.Fatalf("particle %d is not finite: %+v", i, p.pos)
		}
		if !c.w.bounds.contains(p.pos, -1.5) {
			t.Fatalf("particle %d escaped the window: %+v", i, p.pos)
		}
	}
	for i, lg := range c.legs {
		if span := lg.root.pos.Sub(lg.tip.pos).Len(); span > lg.limit*1.10 {
			t.Fatalf("leg %d stretched to %.1f, beyond its %.1f of bone", i, span, lg.limit)
		}
		bones := [3][2]*particle{{lg.root, lg.knee}, {lg.knee, lg.shin}, {lg.shin, lg.tip}}
		for b, pair := range bones {
			got := pair[0].pos.Sub(pair[1].pos).Len()
			// The hinges are soft on purpose, so a loaded leg bends and gives, and the
			// bones are put back at the end of the tick. The give is up to a few pixels
			// on legs about a hundred pixels long, which is the spring: a
			// bone must not turn into rubber, but neither should it be welded, or the
			// leg would not spring at all. The budget is absolute rather than a
			// fraction, or the short bone at the hip would look worst for the same give.
			if math.Abs(got-lg.rest[b]) > 8.0 {
				t.Fatalf("leg %d bone %d is %.2f, expected %.2f", i, b, got, lg.rest[b])
			}
		}
	}
}

func TestWalksToClickedTarget(t *testing.T) {
	for _, seed := range []uint64{1, 7, 99} {
		c := testCreature(seed)
		start := c.abdomen.pos
		target := c.w.bounds.clampVec(start.Add(V(230, 130)), 40)
		arrived := false
		c.onArrive = func() { arrived = true }
		c.SetTarget(target)

		frames := 0
		for range 60 * 20 {
			c.Update(testDt)
			frames++
			checkSane(t, c)
			if arrived {
				break
			}
		}
		if !arrived {
			t.Fatalf("seed %d: never arrived, %.1f px short of the click", seed, c.abdomen.pos.Sub(target).Len())
		}
		if frames > 60*12 {
			t.Fatalf("seed %d: took %d frames to cross %.0f px", seed, frames, target.Sub(start).Len())
		}
		// It arrives within arriveRadius and then settles, which may shift it a little;
		// the legs are springs now, not shoves.
		if d := c.abdomen.pos.Sub(target).Len(); d > 30 {
			t.Fatalf("seed %d: stopped %.1f px from the click", seed, d)
		}
		// Feet must actually step along the way: gliding there means the gait is
		// broken, and a stutter of tiny steps means the step geometry is broken.
		rate := float64(c.stepCount) / (float64(frames) / 60)
		if rate < 2 {
			t.Fatalf("seed %d: %.1f steps/s over %.0f px: it glided", seed, rate, target.Sub(start).Len())
		}
		if rate > 16 {
			t.Fatalf("seed %d: %.1f steps/s: the gait is scrambling", seed, rate)
		}
	}
}

func TestWalkingKeepsAGrip(t *testing.T) {
	c := testCreature(3)
	c.SetTarget(c.w.bounds.clampVec(c.abdomen.pos.Add(V(-210, -120)), 40))

	maxAir, minPlanted := 0, legCount
	steps := 0
	for range 60 * 14 {
		c.Update(testDt)
		checkSane(t, c)
		// Only while it is on the move: the settling gait afterwards may have one
		// leg more in the air.
		if c.hasTarget {
			maxAir = max(maxAir, c.airCount())
		}
		minPlanted = min(minPlanted, c.plantedCount())
		if c.airCount() == 0 {
			steps++
		}
	}
	if maxAir > 2 {
		t.Fatalf("%d legs airborne at once while walking", maxAir)
	}
	if minPlanted < 4 {
		t.Fatalf("only %d legs planted at the worst moment", minPlanted)
	}
	if steps == 60*14 {
		t.Fatal("no leg ever moved")
	}
}

func TestIdleCreatureKeepsItsFooting(t *testing.T) {
	c := testCreature(11)
	start := c.abdomen.pos
	maxAir, minPlanted := 0, legCount
	for range 60 * 15 {
		c.Update(testDt)
		checkSane(t, c)
		maxAir = max(maxAir, c.airCount())
		minPlanted = min(minPlanted, c.plantedCount())
	}
	if minPlanted < 4 {
		t.Fatalf("idle creature dropped to %d planted legs", minPlanted)
	}
	if maxAir > 3 {
		// Three is the settling gait; more than that is a scramble.
		t.Fatalf("%d legs airborne while idle", maxAir)
	}
	if drift := c.abdomen.pos.Sub(start).Len(); drift > 260 {
		t.Fatalf("idle creature drifted %.0f px from where it started", drift)
	}
}

func TestDraggedCreatureScramblesAndStaysInside(t *testing.T) {
	c := testCreature(5)
	c.BeginDrag(c.abdomen.pos)
	start := c.stepCount
	for i := range 60 * 4 {
		// Swing the cursor around the window in a circle.
		ang := float64(i) / (60 * 4) * 2 * math.Pi
		c.DragTo(V(windowW/2+math.Cos(ang)*220, windowH/2+math.Sin(ang)*150))
		c.Update(testDt)
		checkSane(t, c)
	}
	c.EndDrag()
	if c.stepCount-start < 8 {
		t.Fatalf("only %d steps while being dragged around", c.stepCount-start)
	}
	// After release it must calm down instead of staying in a scramble.
	settle := c.stepCount
	peakAir, peakSpeed := 0, 0.0
	for i := range 60 * 6 {
		c.Update(testDt)
		checkSane(t, c)
		if i < 30 { // let the drag throw itself out
			continue
		}
		peakAir = max(peakAir, c.airCount())
		peakSpeed = max(peakSpeed, c.vel.Len())
	}
	if peakAir > 3 {
		// Three is the settling gait; four is the drag scramble it must come out of.
		t.Fatalf("%d legs airborne at once after release: still scrambling", peakAir)
	}
	if rate := float64(c.stepCount-settle) / 6; rate > 9 {
		t.Fatalf("%.1f steps/s after release: it never settled", rate)
	}
	if peakSpeed > c.maxSpeed*0.7 {
		t.Fatalf("still moving at %.0f px/s after release", peakSpeed)
	}
}

// Left alone, the creature should stand still: no wandering, no shuffling.
func TestIdleCreatureStaysPut(t *testing.T) {
	c := testCreature(51)
	start := c.pos
	for range 60 * 10 {
		c.Update(testDt)
		checkSane(t, c)
	}
	if drift := c.pos.Sub(start).Len(); drift > 20 {
		t.Fatalf("idle creature drifted %.1f px", drift)
	}
	if c.stepCount > 12 {
		t.Fatalf("idle creature took %d steps in ten seconds", c.stepCount)
	}
}

// A leg's bones have to be able to span the length it was asked for. Getting this
// wrong crushes every foot towards the body and makes the creature shuffle.
func TestLegsCanReachTheirLength(t *testing.T) {
	c := testCreature(41)
	for i, lg := range c.legs {
		if lg.limit < lg.spec.reach*0.8 {
			t.Fatalf("leg %d can only span %.1f of its %.1f reach", i, lg.limit, lg.spec.reach)
		}
		if lg.limit > lg.spec.reach*1.1 {
			t.Fatalf("leg %d spans %.1f, more than its %.1f reach", i, lg.limit, lg.spec.reach)
		}
	}
}

// The script API builds creatures out of definitions, so a definition must
// survive being read back out and replayed.
func TestDefinitionRoundTrip(t *testing.T) {
	c := testCreature(21)
	before := c.def()
	if len(before.Legs) != legCount {
		t.Fatalf("default creature has %d legs, want %d", len(before.Legs), legCount)
	}

	c.applyDef(before)
	after := c.def()
	if len(after.Legs) != len(before.Legs) || after.BodyLength != before.BodyLength ||
		after.BodyWidth != before.BodyWidth {
		t.Fatalf("round trip changed the creature: %d legs -> %d, body %gx%g -> %gx%g",
			len(before.Legs), len(after.Legs), before.BodyLength, before.BodyWidth,
			after.BodyLength, after.BodyWidth)
	}
	for i := range before.Legs {
		if math.Abs(before.Legs[i].Angle-after.Legs[i].Angle) > 1e-9 ||
			math.Abs(before.Legs[i].Reach-after.Legs[i].Reach) > 1e-9 {
			t.Fatalf("leg %d changed: %+v -> %+v", i, before.Legs[i], after.Legs[i])
		}
	}
}

// A leg grown at the prompt has to be a real leg: planted, stepped, and drawn
// from the same bones as the rest.
func TestLegAddedAtRuntimeWalks(t *testing.T) {
	c := testCreature(22)
	added := c.addLeg(legDef{Angle: 240, Reach: 140})
	if got := len(c.legs); got != legCount+1 {
		t.Fatalf("creature has %d legs after adding one, want %d", got, legCount+1)
	}
	if added.limit <= 0 {
		t.Fatal("added leg has no reach")
	}

	c.SetTarget(c.w.bounds.clampVec(c.pos.Add(V(-200, 80)), bodyMargin))
	stepped := false
	for range 60 * 10 {
		c.Update(testDt)
		checkSane(t, c)
		if c.stepCount > 0 && added.anchor != added.to {
			stepped = true
		}
	}
	if !stepped {
		t.Fatal("the added leg never stepped")
	}

	// And it must come off again cleanly.
	before := len(c.w.parts)
	c.clear()
	if len(c.legs) != 0 || len(c.strands) != 0 {
		t.Fatalf("clear left %d legs and %d strands", len(c.legs), len(c.strands))
	}
	if len(c.w.parts) >= before {
		t.Fatalf("clear left particles behind: %d -> %d", before, len(c.w.parts))
	}
	for range 60 {
		c.Update(testDt)
		checkSane(t, c)
	}
}

// The shipped fan: legs evenly spaced within each side, each the mirror of the one
// across the body, and nothing crowding at the back.
func TestLegFanIsEvenAndClearAtTheBack(t *testing.T) {
	d := defaultDef()
	n := len(d.Legs)
	if n%2 != 0 {
		t.Fatalf("%d legs do not come in pairs", n)
	}
	perSide := n / 2

	for i := range perSide {
		a, b := d.Legs[i], d.Legs[n-1-i]
		if math.Abs(a.Angle+b.Angle) > 0.01 || math.Abs(a.Reach-b.Reach) > 0.01 {
			t.Fatalf("leg %d (%g deg, %g) is not the mirror of leg %d (%g deg, %g)",
				i, a.Angle, a.Reach, n-1-i, b.Angle, b.Reach)
		}
	}
	for i := range perSide - 1 {
		gap := d.Legs[i+1].Angle - d.Legs[i].Angle
		if math.Abs(gap-36) > 0.5 {
			t.Fatalf("legs %d and %d are %g degrees apart, want 36", i, i+1, gap)
		}
	}

	// The gap between the two rearmost legs: wide enough that they cannot foul each
	// other, which is why the pair that used to sit there was taken out.
	tailGap := 360 - math.Abs(d.Legs[perSide-1].Angle) - math.Abs(d.Legs[perSide].Angle)
	if tailGap < 90 {
		t.Fatalf("the rear legs are only %g degrees apart", tailGap)
	}
}

// Even spacing has to survive the simulation, not just the definition: every
// planted foot has to sit in its own slice of the fan, which is what the
// creature settles back into after walking somewhere.
func TestRestingStanceStaysEven(t *testing.T) {
	c := testCreature(31)
	for range 60 * 6 {
		c.Update(testDt)
	}
	// Stop it where it stands and let it settle into its resting stance.
	c.SetTarget(c.pos)
	for range 60 * 5 {
		c.Update(testDt)
	}

	body := c.abdomen.pos
	planted := 0
	for i, lg := range c.legs {
		if lg.air {
			continue
		}
		planted++
		onBody := c.attachPoint(lg).Sub(body)
		foot := lg.tip.pos.Sub(body)
		if onBody.Len2() < 1 || foot.Len2() < 1 {
			continue
		}
		off := math.Abs(math.Atan2(onBody.Cross(foot), onBody.Dot(foot))) * 180 / math.Pi
		if off > 15 {
			t.Fatalf("leg %d is planted %.1f degrees out of its own direction", i, off)
		}
	}
	if planted < len(c.legs)/2 {
		t.Fatalf("only %d feet planted to measure", planted)
	}
}

// Every leg has to take part in a walk: a leg the body merely drags along is not
// walking.
func TestEveryLegStepsWhileWalking(t *testing.T) {
	c := testCreature(61)
	c.SetTarget(c.w.bounds.clampVec(c.pos.Add(V(220, 120)), bodyMargin))
	for range 60 * 16 {
		c.Update(testDt)
		checkSane(t, c)
	}
	for i, lg := range c.legs {
		if lg.stepCount == 0 {
			t.Fatalf("leg %d never stepped while the creature walked", i)
		}
	}
	counts := make([]int, len(c.legs))
	for i, lg := range c.legs {
		counts[i] = lg.stepCount
	}
	lo, hi := counts[0], counts[0]
	for _, v := range counts {
		lo, hi = min(lo, v), max(hi, v)
	}
	if hi > lo*4 {
		t.Fatalf("legs stepped very unevenly: %v", counts)
	}
}

// A walk has to be made of strides, not flicks. Feet that land barely ahead of
// where they left are re-stretched at once, and the creature reads as twitching
// rather than walking.
func TestWalkTakesStrides(t *testing.T) {
	c := testCreature(9)
	c.SetTarget(c.w.bounds.clampVec(c.pos.Add(V(260, 0)), bodyMargin))

	var swings, durs []float64
	last := 0
	airborne, samples := 0, 0
	for range 60 * 25 {
		c.Update(testDt)
		checkSane(t, c)
		if c.hasTarget {
			airborne += c.airCount()
			samples++
		}
		if c.stepCount == last {
			continue
		}
		last = c.stepCount
		for _, lg := range c.legs {
			if lg.air && lg.swing < 0.05 {
				swings = append(swings, lg.to.Sub(lg.from).Len())
				durs = append(durs, lg.dur)
			}
		}
	}
	mean := func(v []float64) float64 {
		if len(v) == 0 {
			return 0
		}
		sum := 0.0
		for _, x := range v {
			sum += x
		}
		return sum / float64(len(v))
	}
	if len(swings) < 5 {
		t.Fatalf("only %d steps taken to judge", len(swings))
	}
	if got := mean(swings); got < 40 {
		t.Fatalf("feet only swing %.0f px: the walk is flickering", got)
	}
	if speed := mean(swings) / mean(durs); speed > 600 {
		t.Fatalf("feet swing at %.0f px/s: a flick, not a step", speed)
	}
	if mean := float64(airborne) / float64(samples); mean > 2 {
		t.Fatalf("%.1f legs airborne on average", mean)
	}
}

// The creature is bilaterally symmetric: a leg and its mirror image on the other
// side of the body are reflections of each other, joints and all. Building the bow
// direction from the body's midline broke the pair pointing straight sideways.
func TestLegsAreMirrorSymmetric(t *testing.T) {
	c := testCreature(5)
	body, axis := c.drawnBody()
	perp := axis.Perp()
	type joints struct{ knee, shin, tip Vec }
	read := func(lg *leg) joints {
		to := func(v Vec) Vec { d := v.Sub(body); return V(d.Dot(axis), d.Dot(perp)) }
		return joints{to(lg.knee.pos), to(lg.shin.pos), to(lg.tip.pos)}
	}
	n := len(c.legs)
	for i := range n / 2 {
		a, b := read(c.legs[i]), read(c.legs[n-1-i])
		for _, pair := range [][2]Vec{{a.knee, b.knee}, {a.shin, b.shin}, {a.tip, b.tip}} {
			fa, fb := pair[0], pair[1]
			if math.Abs(fa.X-fb.X) > 0.5 || math.Abs(fa.Y+fb.Y) > 0.5 {
				t.Fatalf("leg %d is not the mirror of leg %d: %.1f,%.1f against %.1f,%.1f",
					i, n-1-i, fa.X, fa.Y, fb.X, fb.Y)
			}
		}
	}
}

// The gait alternates: a leg and its mirror are never in the air together, which
// is what stops the walk reading as one sweep round the body.
func TestGaitAlternatesSides(t *testing.T) {
	c := testCreature(13)
	c.SetTarget(c.w.bounds.clampVec(c.pos.Add(V(240, 100)), bodyMargin))

	overlap := make([]int, len(c.legs)/2)
	for range 60 * 25 {
		c.Update(testDt)
		checkSane(t, c)
		if !c.hasTarget {
			// Only while it is on the move: settling afterwards may move a couple of
			// legs at once.
			continue
		}
		n := len(c.legs)
		for i := range n / 2 {
			if c.legs[i].air && c.legs[n-1-i].air {
				overlap[i]++
			}
		}
	}
	for i, o := range overlap {
		// The order steps every third leg, so a leg and its mirror are a half cycle
		// apart rather than never simultaneous: two legs may be in the air at once, and
		// if they are mirrors it is for a moment.
		if o > 15 {
			t.Fatalf("leg %d and its mirror were in the air together for %d ticks", i, o)
		}
	}
}
