package main

import (
	"image/color"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/vector"
)

// style is the creature's palette. Every colour is script-settable, so the look
// can be tuned live.
type style struct {
	legHalo   color.RGBA
	legBloom  color.RGBA
	legCore   color.RGBA
	legThread color.RGBA
	knee      color.RGBA
	ring      color.RGBA
	ringBloom color.RGBA
	ringRim   color.RGBA
	bodyFill  color.RGBA
	bodyEdge  color.RGBA
	bodyDot   color.RGBA
	bodyEye   color.RGBA
	silk      color.RGBA

	// Stroke widths and sizes, so the linework can be tuned live too.
	haloWidth   float32
	bloomWidth  float32
	legWidth    float32
	threadWidth float32
	jointRadius float32
	ringRadius  float32
	ringWidth   float32
	bodyWidth   float32
	silkWidth   float32
}

var defaultStyle = style{
	legHalo:   color.RGBA{2, 6, 4, 135},
	legBloom:  color.RGBA{168, 228, 190, 48},
	legCore:   color.RGBA{232, 248, 236, 235},
	legThread: color.RGBA{150, 242, 170, 95},
	knee:      color.RGBA{232, 246, 236, 210},
	ring:      color.RGBA{44, 74, 230, 235},
	ringBloom: color.RGBA{52, 84, 236, 20},
	ringRim:   color.RGBA{0, 0, 0, 120},
	bodyFill:  color.RGBA{22, 24, 15, 236},
	bodyEdge:  color.RGBA{206, 224, 186, 190},
	bodyDot:   color.RGBA{196, 252, 128, 190},
	bodyEye:   color.RGBA{214, 255, 140, 235},
	silk:      color.RGBA{214, 238, 218, 255},

	haloWidth:   4.6,
	bloomWidth:  2.4,
	legWidth:    1.2,
	threadWidth: 0.8,
	jointRadius: 2.0,
	ringRadius:  7.4,
	ringWidth:   1.8,
	bodyWidth:   1.3,
	silkWidth:   0.7,
}

// painter draws the creature. It owns reusable path buffers so that a frame
// costs no allocations, and a dim factor that fades the whole pet when it is in
// click-through mode.
type painter struct {
	st     style
	dim    float64
	feet   bool // draw the ring at each foot
	halo   vector.Path
	core   vector.Path
	thread vector.Path
	body   vector.Path
	silk   vector.Path
}

func (p *painter) rgba(r, g, b, a uint8) color.RGBA {
	if p.dim < 1 {
		a = uint8(float64(a) * clamp01(p.dim))
	}
	return color.RGBA{R: r, G: g, B: b, A: a}
}

// dimmed applies the current dimming to a palette colour.
func (p *painter) dimmed(c color.RGBA) color.RGBA {
	return p.rgba(c.R, c.G, c.B, c.A)
}

func colorRGBA(r, g, b, a uint8) color.RGBA { return color.RGBA{R: r, G: g, B: b, A: a} }

func fx(v Vec) float32 { return float32(v.X) }
func fy(v Vec) float32 { return float32(v.Y) }

func strokePath(dst *ebiten.Image, path *vector.Path, width float32, clr color.Color) {
	so := &vector.StrokeOptions{Width: width, LineJoin: vector.LineJoinRound, LineCap: vector.LineCapRound}
	do := &vector.DrawPathOptions{AntiAlias: true}
	do.ColorScale.ScaleWithColor(clr)
	vector.StrokePath(dst, path, so, do)
}

func fillPath(dst *ebiten.Image, path *vector.Path, clr color.Color) {
	do := &vector.DrawPathOptions{AntiAlias: true}
	do.ColorScale.ScaleWithColor(clr)
	vector.FillPath(dst, path, nil, do)
}

// catmullPath appends a smooth curve through pts, using the standard
// Catmull-Rom to cubic Bézier conversion.
func catmullPath(path *vector.Path, pts []Vec) {
	if len(pts) < 2 {
		return
	}
	path.MoveTo(fx(pts[0]), fy(pts[0]))
	for i := range len(pts) - 1 {
		p1, p2 := pts[i], pts[i+1]
		p0 := p1.Mul(2).Sub(p2)
		if i > 0 {
			p0 = pts[i-1]
		}
		p3 := p2.Mul(2).Sub(p1)
		if i+2 < len(pts) {
			p3 = pts[i+2]
		}
		c1 := p1.Add(p2.Sub(p0).Mul(1.0 / 6))
		c2 := p2.Sub(p3.Sub(p1).Mul(1.0 / 6))
		path.CubicTo(fx(c1), fy(c1), fx(c2), fy(c2), fx(p2), fy(p2))
	}
}

func cubicTo(path *vector.Path, c1, c2, end Vec) {
	path.CubicTo(fx(c1), fy(c1), fx(c2), fy(c2), fx(end), fy(end))
}

// addBody appends the body: a rectangle, turned with the creature, exactly as in
// the reference.
func addBody(path *vector.Path, abd, axis, perp Vec, halfLen, halfWid float64) {
	pt := func(x, y float64) Vec { return abd.Add(axis.Mul(x)).Add(perp.Mul(y)) }
	c := [4]Vec{pt(halfLen, halfWid), pt(halfLen, -halfWid), pt(-halfLen, -halfWid), pt(-halfLen, halfWid)}
	path.MoveTo(fx(c[0]), fy(c[0]))
	for _, p := range c[1:] {
		path.LineTo(fx(p), fy(p))
	}
	path.Close()
}

func drawCreature(dst *ebiten.Image, c *creature, p *painter) {
	pos, axis := c.drawnBody()
	perp := axis.Perp()

	for _, s := range c.strands {
		drawStrand(dst, s, p)
	}
	drawLegs(dst, c, pos, axis, perp, p)
	drawBody(dst, c, pos, axis, perp, p)
	if p.feet {
		for _, lg := range c.legs {
			drawFoot(dst, lg.tip.pos, p)
		}
	}
}

func drawLegs(dst *ebiten.Image, c *creature, pos, axis, perp Vec, p *painter) {
	p.halo.Reset()
	p.core.Reset()
	p.thread.Reset()

	// Legs are straight segments between their joints: hip, two knees, foot.
	var buf [4]Vec
	for _, lg := range c.legs {
		buf[0] = pos.Add(axis.Mul(lg.spec.forward)).Add(perp.Mul(lg.spec.lateral))
		buf[1] = lg.knee.pos
		buf[2] = lg.shin.pos
		buf[3] = lg.tip.pos
		for _, path := range [...]*vector.Path{&p.halo, &p.core, &p.thread} {
			path.MoveTo(fx(buf[0]), fy(buf[0]))
			for _, q := range buf[1:] {
				path.LineTo(fx(q), fy(q))
			}
		}
	}

	// Dark rim first so the pale legs stay readable over light desktops, then a
	// soft bloom, then the bright core with a green thread down the middle.
	strokePath(dst, &p.halo, p.st.haloWidth, p.dimmed(p.st.legHalo))
	strokePath(dst, &p.core, p.st.bloomWidth, p.dimmed(p.st.legBloom))
	strokePath(dst, &p.core, p.st.legWidth, p.dimmed(p.st.legCore))
	strokePath(dst, &p.thread, p.st.threadWidth, p.dimmed(p.st.legThread))

	// A node at each joint, so both knees read as joints.
	for _, lg := range c.legs {
		for _, joint := range [...]Vec{lg.knee.pos, lg.shin.pos} {
			vector.DrawFilledCircle(dst, fx(joint), fy(joint), p.st.jointRadius, p.dimmed(p.st.knee), true)
		}
	}
}

func drawBody(dst *ebiten.Image, c *creature, abd, axis, perp Vec, p *painter) {

	p.body.Reset()
	addBody(&p.body, abd, axis, perp, c.bodyLength, c.bodyWidth)

	fillPath(dst, &p.body, p.dimmed(p.st.bodyFill))
	strokePath(dst, &p.body, p.st.bodyWidth, p.dimmed(p.st.bodyEdge))

	// A pair of eyes at the front and a couple of knobs behind them, inside the
	// square, as in the reference.
	for _, s := range [2]float64{-1, 1} {
		eye := abd.Add(axis.Mul(5.5)).Add(perp.Mul(5.0 * s))
		vector.DrawFilledCircle(dst, fx(eye), fy(eye), 2.2, p.dimmed(p.st.bodyEye), true)
		dot := abd.Add(axis.Mul(-3.5)).Add(perp.Mul(5.0 * s))
		vector.DrawFilledCircle(dst, fx(dot), fy(dot), 1.6, p.dimmed(p.st.bodyDot), true)
	}
}

func drawFoot(dst *ebiten.Image, pos Vec, p *painter) {
	x, y := fx(pos), fy(pos)
	r := p.st.ringRadius
	vector.StrokeCircle(dst, x, y, r*1.25, r*0.49, p.dimmed(p.st.ringBloom), true)
	vector.StrokeCircle(dst, x, y, r, r*0.46, p.dimmed(p.st.ringRim), true) // rim, for light desktops
	vector.StrokeCircle(dst, x, y, r, p.st.ringWidth, p.dimmed(p.st.ring), true)
}

func drawStrand(dst *ebiten.Image, s *strand, p *painter) {
	if s.fade <= 0.02 {
		return
	}
	p.silk.Reset()
	var buf [strandNodes]Vec
	for i, n := range s.nodes {
		buf[i] = n.pos
	}
	catmullPath(&p.silk, buf[:])

	// Tight silk shines, slack silk barely shows.
	alpha := (16 + 96*s.taut) * s.fade
	c := p.st.silk
	strokePath(dst, &p.silk, p.st.silkWidth*1.7, p.rgba(c.R*3/4, c.G*3/4, c.B*3/4, uint8(alpha*0.55)))
	strokePath(dst, &p.silk, p.st.silkWidth, p.rgba(c.R, c.G, c.B, uint8(alpha)))
}
