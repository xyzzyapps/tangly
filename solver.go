package main

import "math"

// particle is a Verlet point mass. invMass == 0 marks a kinematic point: the
// animation places it directly and neither integration nor constraint
// corrections ever move it.
type particle struct {
	pos, prev Vec
	force     Vec
	invMass   float64
	damp      float64
}

// vel returns the velocity implied by the last Verlet step, in pixels/second.
func (p *particle) vel(dt float64) Vec {
	if dt <= 0 {
		return Vec{}
	}
	return p.pos.Sub(p.prev).Mul(1 / dt)
}

// setPos places a kinematic particle without imparting velocity.
func (p *particle) setPos(pos Vec) {
	p.pos = pos
	p.prev = pos
}

// accel accumulates an acceleration (pixels/second^2) on a particle.
func accel(p *particle, a Vec) {
	if p.invMass == 0 {
		return
	}
	p.force = p.force.Add(a.Mul(1 / p.invMass))
}

type ckind uint8

const (
	// rigidLen pins the distance to exactly length.
	rigidLen ckind = iota
	// minLen pushes two points apart when they get closer than length.
	minLen
	// ropeLen pulls two points together when they get further apart than length,
	// which is how rope (silk) behaves.
	ropeLen
	// hingeLen holds the angle at b: a, b and c are rotated to keep their corners
	// the same as they were when the constraint was made. This is verlet-js's
	// AngleConstraint, and it is what gives a leg its shape and its spring.
	hingeLen
)

type constraint struct {
	a, b   *particle // for a hinge, b is the middle of the three and c the far end
	c      *particle
	length float64
	angle  float64 // the rest angle at b, for a hinge
	stiff  float64
	kind   ckind
}

// angleAt is the signed angle at b from b->a to b->c, the same measure verlet-js
// uses for its angle constraints (dot and cross, so it is stable across the wrap).
func angleAt(a, b, c Vec) float64 {
	ba, bc := a.Sub(b), c.Sub(b)
	return math.Atan2(ba.Cross(bc), ba.Dot(bc))
}

func (c *constraint) solveHinge(passes float64) {
	ang := angleAt(c.a.pos, c.b.pos, c.c.pos)
	diff := ang - c.angle
	// Take the short way round.
	if diff <= -math.Pi {
		diff += 2 * math.Pi
	} else if diff >= math.Pi {
		diff -= 2 * math.Pi
	}
	stiff := c.stiff
	if stiff < 1 {
		stiff /= passes
	}
	diff *= stiff

	// Rotate the ends about the middle, then the middle about each end, exactly as
	// verlet-js does. The two halves of the last step overlap, so the middle joint
	// takes up the slack and the chain settles instead of spinning.
	//
	// verlet-js can rotate all three because everything in it is a free particle. Here
	// the ends of a hinge can be a planted foot or the body, which the animation places
	// and nothing may move: those are put back, and the free end takes the rotation.
	ka, kb, kc := c.a.pos, c.b.pos, c.c.pos
	c.a.pos = c.a.pos.RotAbout(c.b.pos, diff)
	c.c.pos = c.c.pos.RotAbout(c.b.pos, -diff)
	c.b.pos = c.b.pos.RotAbout(c.a.pos, diff)
	c.b.pos = c.b.pos.RotAbout(c.c.pos, -diff)
	if c.a.invMass == 0 {
		c.a.pos = ka
	}
	if c.b.invMass == 0 {
		c.b.pos = kb
	}
	if c.c.invMass == 0 {
		c.c.pos = kc
	}
}

// solve applies one relaxation pass. A stiffness of 1 means rigid and is applied
// as such; anything softer is divided by the number of passes, the way verlet-js
// scales its constraints by 1/step. That keeps a soft constraint's meaning the
// same whether the solver runs four passes or forty -- more passes only converge
// it better -- while rigid constraints stay exactly rigid.
func (c *constraint) solve(passes float64) {
	if c.kind == hingeLen {
		c.solveHinge(passes)
		return
	}
	l := c.b.pos.Sub(c.a.pos).Len()
	if l < 1e-9 {
		return
	}
	switch c.kind {
	case rigidLen:
	case minLen:
		if l >= c.length {
			return
		}
	case ropeLen:
		if l <= c.length {
			return
		}
	}
	wa, wb := c.a.invMass, c.b.invMass
	w := wa + wb
	if w == 0 {
		return
	}
	stiff := c.stiff
	if stiff < 1 {
		stiff /= passes
	}
	corr := (l - c.length) / l * stiff
	d := c.b.pos.Sub(c.a.pos)
	c.a.pos = c.a.pos.Add(d.Mul(corr * wa / w))
	c.b.pos = c.b.pos.Sub(d.Mul(corr * wb / w))
}

type rect struct {
	minX, minY, maxX, maxY float64
}

func (r rect) clampVec(p Vec, margin float64) Vec {
	return V(clampf(p.X, r.minX+margin, r.maxX-margin), clampf(p.Y, r.minY+margin, r.maxY-margin))
}

func (r rect) contains(p Vec, margin float64) bool {
	return p.X >= r.minX+margin && p.X <= r.maxX-margin && p.Y >= r.minY+margin && p.Y <= r.maxY-margin
}

// world owns every particle and constraint and advances them together.
type world struct {
	parts  []*particle
	cons   []constraint
	bounds rect
	iters  int
}

func (w *world) newParticle(pos Vec, invMass, damp float64) *particle {
	p := &particle{pos: pos, prev: pos, invMass: invMass, damp: damp}
	w.parts = append(w.parts, p)
	return p
}

func (w *world) link(a, b *particle, length, stiff float64, kind ckind) {
	w.cons = append(w.cons, constraint{a: a, b: b, length: length, stiff: stiff, kind: kind})
}

// hinge holds the angle at b, between a and c, at whatever it is now.
func (w *world) hinge(a, b, c *particle, stiff float64) {
	w.cons = append(w.cons, constraint{
		a: a, b: b, c: c, stiff: stiff, kind: hingeLen,
		angle: angleAt(a.pos, b.pos, c.pos),
	})
}

// step integrates every particle and then relaxes the constraint graph. The
// relaxation order is the iteration count from the world, scaled inside each
// constraint so that softness does not depend on it.
func (w *world) step(dt float64) {
	dt2 := dt * dt
	for _, p := range w.parts {
		if p.invMass == 0 {
			p.prev = p.pos
			continue
		}
		// Verlet: the previous displacement carries the velocity, so a change in
		// acceleration of a * dt^2 becomes a change in velocity of a * dt^2.
		v := p.pos.Sub(p.prev).Mul(p.damp)
		p.prev = p.pos
		p.pos = p.pos.Add(v).Add(p.force.Mul(p.invMass).Mul(dt2))
	}
	passes := float64(w.iters)
	if passes < 1 {
		passes = 1
	}
	for range w.iters {
		for j := range w.cons {
			w.cons[j].solve(passes)
		}
		for _, p := range w.parts {
			w.clampInside(p)
		}
	}
	// One last pass over the rigid constraints only. The soft ones (a leg's hinges,
	// a foot reaching for a target) give under load by design, but they drag the
	// rigid lengths with them as they go; this puts the lengths back so a bone
	// stays a bone and the give shows up as bending, not as stretching.
	// A few times: one pass leaves the first bones in a chain carrying the residual
	// of the ones after them. This does not stiffen the hinges -- they are still
	// corrected every pass, and a leg still bends and springs -- it only stops the
	// give showing up as a bone changing length.
	for range 8 {
		for j := range w.cons {
			if w.cons[j].kind != hingeLen && w.cons[j].stiff >= 1 {
				w.cons[j].solve(passes)
			}
		}
	}
	// Projections can push a joint over the edge, so the walls get the last word.
	for _, p := range w.parts {
		w.clampInside(p)
	}
	for _, p := range w.parts {
		p.force = Vec{}
	}
}

// forget drops particles and every constraint that touches them, so a script can
// take the creature apart again.
func (w *world) forget(dead map[*particle]bool) {
	kept := w.parts[:0]
	for _, p := range w.parts {
		if !dead[p] {
			kept = append(kept, p)
		}
	}
	w.parts = kept

	cons := w.cons[:0]
	for _, c := range w.cons {
		if !dead[c.a] && !dead[c.b] {
			cons = append(cons, c)
		}
	}
	w.cons = cons
}

// clampInside keeps particles in the window, killing the wall-normal velocity.
func (w *world) clampInside(p *particle) {
	// Kinematic particles are clamped too: the animation places them, and a
	// placement outside the window is still outside the window.
	if p.invMass == 0 {
		p.pos = w.bounds.clampVec(p.pos, 0)
		p.prev = p.pos
		return
	}
	if p.pos.X < w.bounds.minX {
		p.pos.X, p.prev.X = w.bounds.minX, w.bounds.minX
	}
	if p.pos.X > w.bounds.maxX {
		p.pos.X, p.prev.X = w.bounds.maxX, w.bounds.maxX
	}
	if p.pos.Y < w.bounds.minY {
		p.pos.Y, p.prev.Y = w.bounds.minY, w.bounds.minY
	}
	if p.pos.Y > w.bounds.maxY {
		p.pos.Y, p.prev.Y = w.bounds.maxY, w.bounds.maxY
	}
}
