package main

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
)

type constraint struct {
	a, b   *particle
	length float64
	stiff  float64
	kind   ckind
}

func (c *constraint) solve() {
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
	corr := (l - c.length) / l * c.stiff
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

// step integrates every particle and then relaxes the constraint graph.
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
	for i := 0; i < w.iters; i++ {
		for j := range w.cons {
			w.cons[j].solve()
		}
		for _, p := range w.parts {
			w.clampInside(p)
		}
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
	if p.invMass == 0 {
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
