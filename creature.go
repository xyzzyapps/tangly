package main

import (
	"fmt"
	"math"
	"math/rand/v2"
)

const (
	// legCount and strandCount describe the shipped creature; a script can build
	// any number of either.
	legCount    = 8
	strandCount = 9
	strandNodes = 6

	// bodyMass and legMass are inverse masses (particle weights). The body is
	// heavy on purpose: eight planted legs pull on it, and a light body ends up
	// being steered by its own feet instead of the other way round.
	bodyMass = 0.2
	legMass  = 1.6
	// hipStiff is the stiffness of the link between a hip and the body. Rigid:
	// the body is kinematic, so the correction can only move the hip, and a hip
	// that trails behind makes the stance measurements lie.
	hipStiff = 1.0

	bodySpan = 20 // distance from abdomen to head
	// The body is a rectangle: defaultBodyLength is half of it along the body
	// axis, defaultBodyWidth half of it across, as in the reference.
	defaultBodyLength = 16.0
	defaultBodyWidth  = 11.0
	// bodyMargin keeps the whole stance, not just the body, inside the window.
	bodyMargin = 60.0
	// defaultShinBend is the interior angle at the shin joint. Sharing the bend
	// between the knee and the shin is what makes a leg a smooth tendril instead
	// of a straight stick with a corner in it.
	defaultShinBend = 2.15

	// walkTrigger is how far a planted leg may stretch, as a fraction of the span
	// its bones can cover, before it steps. landingScale is where the foot lands.
	walkTrigger = 0.9
	idleTrigger = 0.95
	// idleStance leaves the chain with a visible elbow at the knee, the way the
	// reference legs are jointed, instead of stretching them into straight rods.
	idleStance   = 0.88
	landingScale = 0.86
	// foldLimit and stretchLimit bound the hip-to-foot span: below the first the
	// chain would have to loop back on itself, above the second the bones would
	// have to stretch.
	foldLimit    = 0.78
	stretchLimit = 0.97
	// How a walking foot is placed, as fractions of the leg's reach: legLead ahead
	// of the hip along the direction of travel, legSide out to its own side. The
	// pair has to land inside walkTrigger or the foot steps again on touchdown, and
	// the lead wants to be as long as the geometry allows, so that a step covers
	// ground instead of flickering. restSpread is how far a planted foot may drift
	// out of line before the creature steps it back.
	legLead = 0.55
	legSide = 0.68
	// maxTrail is how far a drawn joint may lag its bone, in pixels.
	maxTrail   = 10.0
	restSpread = 8.0
	// dragFrom is the span at which a planted leg starts to hold the body back,
	// and settleFor is how long the creature spends putting its legs back in line
	// after a walk.
	dragFrom  = 0.9
	settleFor = 2.5
)

// legSpec places one leg in body space: forward runs along the body axis
// (positive towards the head), lateral across it, reach is the leg's natural
// length when it is stretched out.
type legSpec struct {
	angle   float64 // degrees from the head
	forward float64 // where the leg leaves the shell
	lateral float64
	reach   float64 // natural length
}

// The legs fan out evenly around the body, as in the reference, instead of
// bunching up along its sides. Angles are measured from the head, the four on
// each side mirroring the other, and each leg gets its own length so the
// silhouette stays uneven and alive.
// leg is a three-bone constraint chain anchored to the body. The tip is
// kinematic: the animation plants it in the world while the body walks away
// from it, and the rigid bones drag the knee and shin along behind.
type leg struct {
	spec  legSpec
	index int
	root  *particle
	knee  *particle // placed by inverse kinematics, never integrated
	shin  *particle
	tip   *particle
	rest  [3]float64 // bone lengths: root-knee, knee-shin, shin-tip
	reach [2]float64 // [0] = root-knee, [1] = end-to-end reach of the shin pair
	side  float64    // which way the knee bows, +1 or -1
	limit float64    // longest hip-to-foot span the bones allow

	anchor   Vec // where the planted foot is pinned
	from, to Vec
	swing    float64 // 0..1 while the foot is airborne, -1 when planted
	dur      float64
	// drawn marks are where the joints are shown, which trails the exact solution
	// by a few milliseconds so the leg carries some weight as it moves.
	drawnKnee Vec
	drawnShin Vec

	air       bool
	planted   bool
	settling  bool    // this step is putting the leg back in line
	restTimer float64 // seconds before it may step again
	nextStep  float64 // seconds until its turn in the walking cycle
	stepCount int     // how many times it has stepped
}

// strand is a rope of silk: node 0 rides on the body and the far node is pinned
// until the strand goes taut, at which point the far end slips loose and the
// strand fades out instead of tethering the creature.
type strand struct {
	nodes  []*particle
	phase  []float64
	maxLen float64
	taut   float64
	fade   float64
	dying  bool
}

type creature struct {
	w   *world
	rng *rand.Rand

	head    *particle
	abdomen *particle
	legs    []*leg
	strands []*strand

	bodyLength float64 // half the body's length, along the axis
	bodyWidth  float64 // half its width, across the axis
	shinBend   float64 // interior angle at the shin joint

	dt    float64
	time  float64
	pos   Vec // body centre
	vel   Vec // body velocity, pixels/second
	angle float64

	target    Vec
	hasTarget bool
	strolling bool // walking to a spot it picked itself rather than a click

	arriveRadius float64
	maxSpeed     float64
	agility      float64
	turnRate     float64
	dragGain     float64

	cursor      Vec
	cursorValid bool

	dragging bool
	dragTo   Vec

	scurry      float64
	settleTimer float64 // seconds left of putting the legs back in line
	wasWalking  bool

	// Follow rates for the drawn pose, in "how quickly it catches up" per second.
	// The physics is untouched by them: the feet stay exactly where they are
	// planted and only the joints and the shell trail.
	legFollow  float64
	bodyFollow float64
	drawnPos   Vec
	drawnAngle float64

	// Wandering is off by default: a standing creature should stand. It is here
	// for when you want it to potter about on its own.
	wanderOn     bool
	wanderEvery  [2]float64 // seconds between decisions
	wanderChance float64    // how often it decides to go somewhere
	wanderRadius [2]float64 // how far it picks to go
	wanderSpeed  float64    // fraction of full speed
	// watching makes the head follow the pointer when it is over the creature's
	// corner of the desktop.
	watching    bool
	stepCount   int
	wanderTimer float64
	stepCursor  int

	// Hooks let the presentation layer react without the model knowing about it.
	onPlant   func(strength float64)
	onArrive  func()
	onChatter func()

	chatterTimer float64
}

// legDef is one leg as a script describes it: a direction around the body and a
// natural length.
type legDef struct {
	Angle float64 // degrees from the head, positive to one side
	Reach float64 // natural length in pixels
}

// creatureDef is the recipe for a creature. The script builds it, and reset
// replays it.
type creatureDef struct {
	BodyLength float64
	BodyWidth  float64
	Legs       []legDef
	SilkCount  int
	SilkLen    float64
}

// defaultDef is the creature as it ships: ten legs fanned around a square body
// with a trail of silk.
func defaultDef() creatureDef {
	// Four legs a side, 36 degrees apart, mirror images of each other across the
	// body. Nothing at the very back: the pair that used to sit there crowded the
	// ones in front of it.
	angles := []float64{18, 54, 90, 126, -126, -90, -54, -18}
	reaches := []float64{126, 118, 102, 114, 114, 102, 118, 126}
	d := creatureDef{BodyLength: defaultBodyLength, BodyWidth: defaultBodyWidth, SilkCount: 9, SilkLen: 300}
	for i, a := range angles {
		d.Legs = append(d.Legs, legDef{Angle: a, Reach: reaches[i]})
	}
	return d
}

func newCreature(w *world, center Vec, rng *rand.Rand) *creature {
	c := &creature{
		w:            w,
		rng:          rng,
		dt:           1.0 / 60,
		pos:          center,
		arriveRadius: 16,
		maxSpeed:     62,
		agility:      7,
		turnRate:     4,
		dragGain:     3.2,
		wanderTimer:  5 + rng.Float64()*8,
		chatterTimer: 6 + rng.Float64()*10,

		bodyLength: defaultBodyLength,
		bodyWidth:  defaultBodyWidth,
		shinBend:   defaultShinBend,

		// How quickly the drawn joints and shell catch up with the physics. The
		// joints trail a foot's swing by a dozen pixels or so, which is what gives
		// the legs some weight; the shell trails a little more than that.
		legFollow:  55,
		bodyFollow: 16,

		wanderEvery:  [2]float64{20, 50},
		wanderChance: 0.35,
		wanderRadius: [2]float64{25, 70},
		wanderSpeed:  0.45,
		watching:     true,
	}

	c.angle = rng.Float64() * 2 * math.Pi
	c.drawnAngle = c.angle
	c.drawnPos = center
	axis := V(math.Cos(c.angle), math.Sin(c.angle))

	c.abdomen = w.newParticle(center.Sub(axis.Mul(5)), 0, 1)
	c.head = w.newParticle(center.Add(axis.Mul(10)), 0, 1)
	c.linkBody()
	c.applyDef(defaultDef())
	return c
}

// applyDef rebuilds the creature from a definition.
func (c *creature) applyDef(d creatureDef) {
	c.clear()
	if d.BodyLength > 0 {
		c.bodyLength = d.BodyLength
	}
	if d.BodyWidth > 0 {
		c.bodyWidth = d.BodyWidth
	}
	for _, ld := range d.Legs {
		c.addLeg(ld)
	}
	if d.SilkCount > 0 {
		c.addSilk(d.SilkCount, d.SilkLen)
	}
}

// def describes the creature as it stands, so a script can read it back out.
func (c *creature) def() creatureDef {
	d := creatureDef{
		BodyLength: c.bodyLength,
		BodyWidth:  c.bodyWidth,
		SilkCount:  len(c.strands),
	}
	for _, s := range c.strands {
		d.SilkLen = math.Max(d.SilkLen, s.maxLen)
	}
	for _, lg := range c.legs {
		d.Legs = append(d.Legs, legDef{Angle: lg.spec.angle, Reach: lg.spec.reach})
	}
	return d
}

// clear takes the creature apart, leaving just the body.
func (c *creature) clear() {
	dead := map[*particle]bool{}
	for _, lg := range c.legs {
		if lg == nil {
			continue
		}
		dead[lg.root], dead[lg.knee], dead[lg.shin], dead[lg.tip] = true, true, true, true
	}
	for _, s := range c.strands {
		for _, n := range s.nodes {
			dead[n] = true
		}
	}
	c.w.forget(dead)
	c.legs = c.legs[:0]
	c.strands = c.strands[:0]
	c.stepCursor = 0
}

// addLeg grows one leg out of the body: the hip is where its direction leaves the
// square shell, the knee sits close in, and the rest of the leg runs almost
// straight to the foot.
func (c *creature) addLeg(d legDef) *leg {
	axis := c.bodyAxis()
	perp := axis.Perp()
	a := d.Angle * math.Pi / 180
	cos, sin := math.Cos(a), math.Sin(a)
	edge := math.Min(c.bodyLength/math.Max(math.Abs(cos), 1e-6),
		c.bodyWidth/math.Max(math.Abs(sin), 1e-6))
	spec := legSpec{
		angle:   d.Angle,
		forward: cos * edge,
		lateral: sin * edge,
		reach:   d.Reach,
	}

	attach := c.abdomen.pos.Add(axis.Mul(spec.forward)).Add(perp.Mul(spec.lateral))
	outward := V(cos, sin).Rot(c.angle)

	chord := outward.Mul(spec.reach * 0.9)
	n := chord.Perp().Norm()
	// Every knee bows towards the head. A leg pointing straight out to the side has
	// no "outwards" perpendicular to bow along -- its perpendicular runs fore and
	// aft -- so the bow is defined against the body's axis instead. That is the same
	// rule for both sides, which is what keeps a leg and its mirror image mirrored.
	if n.Dot(axis) < 0 {
		n = n.Mul(-1)
	}
	// The bowed layout sets the proportions of the three bones; the foot itself
	// starts inside the span those bones can actually cover, so the leg begins
	// with a visible bend instead of being planted past its own length.
	hipPos := attach
	kneePos := hipPos.Add(chord.Mul(0.18)).Add(n.Mul(spec.reach * 0.06))
	shinPos := hipPos.Add(chord.Mul(0.62)).Add(n.Mul(spec.reach * 0.04))
	tipPos := hipPos.Add(chord)

	// Only the hip is a physical particle: knee and shin are placed by IK every
	// tick, which is what keeps the bones from stretching like rubber.
	root := c.w.newParticle(hipPos, legMass, 0.995)
	knee := c.w.newParticle(kneePos, 0, 1)
	shin := c.w.newParticle(shinPos, 0, 1)
	tip := c.w.newParticle(tipPos, 0, 1)

	// Two compliant links to two body points place the hip, without constraining
	// the body's orientation.
	c.w.link(root, c.abdomen, root.pos.Sub(c.abdomen.pos).Len(), hipStiff, rigidLen)
	c.w.link(root, c.head, root.pos.Sub(c.head.pos).Len(), hipStiff, rigidLen)

	lg := &leg{spec: spec, index: len(c.legs), root: root, knee: knee, shin: shin, tip: tip,
		anchor: tipPos, swing: -1, planted: true}
	lg.rest[0] = kneePos.Sub(hipPos).Len()
	lg.rest[1] = shinPos.Sub(kneePos).Len()
	lg.rest[2] = tipPos.Sub(shinPos).Len()
	// End-to-end reach of the (knee-shin, shin-tip) pair at its resting bend.
	lg.reach[0] = lg.rest[0]
	lg.reach[1] = math.Sqrt(lg.rest[1]*lg.rest[1] + lg.rest[2]*lg.rest[2] -
		2*lg.rest[1]*lg.rest[2]*math.Cos(c.shinBend))
	lg.limit = lg.reach[0] + lg.reach[1]
	lg.side = sign(chord.Cross(kneePos.Sub(hipPos)))

	tipPos = hipPos.Add(outward.Mul(lg.limit * idleStance))
	tip.setPos(tipPos)
	lg.anchor = tipPos
	lg.drawnKnee = kneePos
	lg.drawnShin = shinPos

	c.legs = append(c.legs, lg)
	return lg
}

// addSilk hangs count strands of the given length around the body.
func (c *creature) addSilk(count int, maxLen float64) {
	if maxLen <= 0 {
		maxLen = 300
	}
	for range count {
		c.strands = append(c.strands, c.newStrand(maxLen))
	}
}

// linkBody keeps the head at a fixed distance ahead of the abdomen.
func (c *creature) linkBody() {
	c.w.link(c.abdomen, c.head, bodySpan, 1, rigidLen)
}

func (c *creature) newStrand(maxLen float64) *strand {
	s := &strand{maxLen: maxLen, fade: 0}
	seg := s.maxLen / float64(strandNodes-1)
	for i := range strandNodes {
		p := c.w.newParticle(c.abdomen.pos, 0.4, 0.99)
		if i == strandNodes-1 {
			p.invMass = 0
		}
		s.nodes = append(s.nodes, p)
		s.phase = append(s.phase, c.rng.Float64()*100)
	}
	c.w.link(s.nodes[0], c.abdomen, 1, 1, rigidLen) // node 0 rides on the body
	for i := range strandNodes - 1 {
		c.w.link(s.nodes[i], s.nodes[i+1], seg, 1, ropeLen)
	}
	c.layStrand(s)
	return s
}

// layStrand re-lays a strand as a slack rope leaving the body in a fresh
// direction, and pins its far end.
func (c *creature) layStrand(s *strand) {
	body := c.abdomen.pos
	seg := s.maxLen / float64(strandNodes-1)
	ang := c.rng.Float64() * 2 * math.Pi
	// Prefer a direction whose far end lands inside the window.
	for range 8 {
		if c.w.bounds.contains(body.Add(V(math.Cos(ang), math.Sin(ang)).Mul(s.maxLen)), 12) {
			break
		}
		ang += 0.6
	}
	dir := V(math.Cos(ang), math.Sin(ang))
	for i, p := range s.nodes {
		p.setPos(c.w.bounds.clampVec(body.Add(dir.Mul(seg*float64(i))), 6))
	}
	s.nodes[strandNodes-1].invMass = 0
	s.dying = false
	s.fade = 0
	s.taut = 0
}

// constrainFoot keeps the foot inside the span the leg's bones allow: never
// further than they reach, and never so close that the chain has to fold back on
// itself into a loop. A planted foot that hits either bound skids along the silk
// instead of tearing the leg apart; the step already on its way re-plants it.
func (lg *leg) constrainFoot(bounds rect) {
	d := lg.tip.pos.Sub(lg.root.pos)
	l := d.Len()
	if l < 1e-6 {
		return
	}
	span := clampf(l, lg.limit*foldLimit, lg.limit*stretchLimit)
	if span == l {
		return
	}
	p := bounds.clampVec(lg.root.pos.Add(d.Mul(span/l)), 3)
	lg.tip.setPos(p)
	if lg.planted {
		lg.anchor = p
	}
}

// resolve places the knee and shin for a given hip and foot by direct geometry:
// the same three bones, but solved instead of iterated, so a leg can never be
// asked to reach further than its own length.
func (lg *leg) resolve(bounds rect) {
	b0, r2 := lg.reach[0], lg.reach[1]
	b1, b2 := lg.rest[1], lg.rest[2]
	d := lg.tip.pos.Sub(lg.root.pos)
	dist := clampf(d.Len(), math.Abs(b0-r2)+0.01, lg.limit-0.01)
	dir := d.Norm()
	if dir.IsZero() {
		dir = V(1, 0)
	}
	cosA := clampf((b0*b0+dist*dist-r2*r2)/(2*b0*dist), -1, 1)
	knee := lg.root.pos.Add(dir.Rot(lg.side * math.Acos(cosA)).Mul(b0))
	lg.knee.setPos(bounds.clampVec(knee, 2))

	if kt := lg.tip.pos.Sub(knee); kt.Len() > 1e-6 {
		l := kt.Len()
		cosB := clampf((b1*b1+l*l-b2*b2)/(2*b1*l), -1, 1)
		shin := knee.Add(kt.Mul(1 / l).Rot(lg.side * math.Acos(cosB)).Mul(b1))
		lg.shin.setPos(bounds.clampVec(shin, 2))
	}
}

func (c *creature) bodyAxis() Vec {
	axis := c.head.pos.Sub(c.abdomen.pos).Norm()
	if axis.IsZero() {
		return V(1, 0)
	}
	return axis
}

// attachPoint is where a leg meets the body shell, used for drawing.
func (c *creature) attachPoint(lg *leg) Vec {
	axis := c.bodyAxis()
	return c.abdomen.pos.Add(axis.Mul(lg.spec.forward)).Add(axis.Perp().Mul(lg.spec.lateral))
}

// SetTarget walks the creature to a point, e.g. a mouse click.
func (c *creature) SetTarget(at Vec) {
	c.target = c.w.bounds.clampVec(at, bodyMargin)
	c.hasTarget = true
	c.strolling = false
	c.scurry = 0.5
}

func (c *creature) BeginDrag(at Vec) {
	c.dragging = true
	c.hasTarget = false
	c.dragTo = at
	// Yanking the creature rips the silk loose.
	for _, s := range c.strands {
		if !s.dying {
			s.dying = true
			s.nodes[strandNodes-1].invMass = 0.4
		}
	}
}

func (c *creature) DragTo(at Vec) { c.dragTo = at }

func (c *creature) EndDrag() {
	c.dragging = false
	c.settleTimer = settleFor
}

func (c *creature) NearBody(p Vec, r float64) bool {
	return c.abdomen.pos.Sub(p).Len() <= r || c.head.pos.Sub(p).Len() <= r
}

func (c *creature) SetCursor(p Vec, valid bool) {
	c.cursor = p
	c.cursorValid = valid
}

// Place puts the body somewhere, keeping its legs where they are; the legs then
// settle back into their even stance.
func (c *creature) Place(at Vec) {
	c.pos = c.w.bounds.clampVec(at, bodyMargin)
	c.vel = Vec{}
	c.hasTarget = false
	c.settleTimer = settleFor
}

// Face turns the body to a heading in degrees.
func (c *creature) Face(deg float64) { c.angle = deg * math.Pi / 180 }

// Stand puts every foot straight back out into its own slice of the fan, without
// waiting for the creature to step there.
func (c *creature) Stand() {
	for _, lg := range c.legs {
		outward := c.attachPoint(lg).Sub(c.abdomen.pos).Norm()
		if outward.IsZero() {
			continue
		}
		to := c.w.bounds.clampVec(c.attachPoint(lg).Add(outward.Mul(lg.limit*idleStance)), 6)
		lg.anchor = to
		lg.tip.setPos(to)
		lg.planted, lg.air, lg.swing = true, false, -1
		lg.restTimer = 0.4 + c.rng.Float64()*0.4
	}
}

// legState is one leg as a script sees it.
type legState struct {
	Index    int     `json:"index"`
	Angle    float64 `json:"angle"`   // its direction on the body, in degrees
	Reach    float64 `json:"reach"`   // natural length
	Limit    float64 `json:"limit"`   // longest span its bones allow
	Stance   float64 `json:"stance"`  // current hip-to-foot span, as a fraction of limit
	Steps    int     `json:"steps"`   // how many times it has stepped
	OffLine  float64 `json:"offLine"` // degrees out of its own direction
	Planted  bool    `json:"planted"`
	Airborne bool    `json:"airborne"`
}

// LegStates describes every leg, for inspection at the prompt.
func (c *creature) LegStates() []legState {
	out := make([]legState, 0, len(c.legs))
	for i, lg := range c.legs {
		onBody := c.attachPoint(lg).Sub(c.abdomen.pos)
		foot := lg.anchor.Sub(c.abdomen.pos)
		off := 0.0
		if onBody.Len2() > 1 && foot.Len2() > 1 {
			off = math.Atan2(onBody.Cross(foot), onBody.Dot(foot)) * 180 / math.Pi
		}
		out = append(out, legState{
			Index: i, Angle: lg.spec.angle, Reach: lg.spec.reach, Limit: lg.limit,
			Stance:  c.attachPoint(lg).Sub(lg.anchor).Len() / lg.limit,
			Steps:   lg.stepCount,
			OffLine: off, Planted: lg.planted, Airborne: lg.air,
		})
	}
	return out
}

// silkLen is the length of the strands currently hung.
func (c *creature) silkLen() float64 {
	longest := 0.0
	for _, s := range c.strands {
		longest = math.Max(longest, s.maxLen)
	}
	return longest
}

// summary is a one-line description, handy at the prompt.
func (c *creature) summary() string {
	return fmt.Sprintf("%d legs, %d silk strands, body %gx%g", len(c.legs), len(c.strands),
		c.bodyLength*2, c.bodyWidth*2)
}

func (c *creature) plantedCount() int {
	n := 0
	for _, lg := range c.legs {
		if lg.planted {
			n++
		}
	}
	return n
}

func (c *creature) airCount() int {
	n := 0
	for _, lg := range c.legs {
		if lg.air {
			n++
		}
	}
	return n
}

// Update advances the creature by one fixed tick.
func (c *creature) Update(dt float64) {
	c.dt = dt
	c.time += dt
	c.scurry = math.Max(0, c.scurry-dt)
	c.settleTimer = math.Max(0, c.settleTimer-dt)

	walkDir, walking := c.desiredMotion(dt)
	c.steerBody(dt, walkDir, walking)
	c.placeBody()
	c.updateLegs(dt, walkDir, walking)
	c.updateStrands(dt)
	c.w.step(dt)
	for _, lg := range c.legs {
		lg.constrainFoot(c.w.bounds)
		lg.resolve(c.w.bounds)
		lg.follow(dt, c.legFollow)
	}
	c.followBody(dt)
	c.updateChatter(dt)
}

// desiredMotion picks where the body wants to travel: towards a click target, or
// a slow stroll to a spot it picked itself.
func (c *creature) desiredMotion(dt float64) (Vec, bool) {
	if c.dragging {
		return Vec{}, false
	}
	if c.hasTarget {
		d := c.target.Sub(c.pos)
		if d.Len() <= c.arriveRadius {
			c.hasTarget = false
			c.strolling = false
			c.settleTimer = settleFor
			c.wanderTimer = 6 + c.rng.Float64()*9
			if c.onArrive != nil {
				c.onArrive()
			}
			return Vec{}, false
		}
		return d.Norm(), true
	}
	if !c.wanderOn {
		return Vec{}, false
	}
	c.wanderTimer -= dt
	if c.wanderTimer <= 0 {
		c.wanderTimer = lerpf(c.wanderEvery[0], c.wanderEvery[1], c.rng.Float64())
		if c.rng.Float64() < c.wanderChance {
			ang := c.rng.Float64() * 2 * math.Pi
			r := lerpf(c.wanderRadius[0], c.wanderRadius[1], c.rng.Float64())
			c.target = c.w.bounds.clampVec(c.pos.Add(V(math.Cos(ang), math.Sin(ang)).Mul(r)), bodyMargin+20)
			c.hasTarget = true
			c.strolling = true
			return c.target.Sub(c.pos).Norm(), true
		}
	}
	return Vec{}, false
}

// steerBody drives the body towards its wanted velocity and turns it. The body
// is kinematic: the legs and the silk are constraint-solved around it, and a
// stretching leg feeds back as drag so it visibly pulls itself along.
func (c *creature) steerBody(dt float64, walkDir Vec, walking bool) {
	var want Vec
	switch {
	case c.dragging:
		want = c.dragTo.Sub(c.pos).Mul(9)
		if l := want.Len(); l > 900 {
			want = want.Mul(900 / l)
		}
	case walking && !walkDir.IsZero():
		speed := c.maxSpeed * (1 + 0.4*clamp01(c.scurry))
		if c.strolling {
			speed *= c.wanderSpeed
		}
		if dist := c.target.Sub(c.pos).Len(); dist < 120 {
			speed *= clamp01(0.22 + 0.78*dist/120)
		}
		want = walkDir.Mul(speed)
	}

	// Frame-rate independent approach to the wanted velocity, braking harder when
	// there is nowhere to go so a released creature does not coast.
	brake := c.agility
	if want.IsZero() && !c.dragging {
		brake *= 3
	}
	c.vel = c.vel.Add(want.Sub(c.vel).Mul(1 - math.Exp(-brake*dt)))
	if c.dragging || walking {
		// Only while it is on the move: a taut leg holds the creature back as it
		// pulls itself along, but a standing creature is not dragged about by its
		// own legs, which would leave it shuffling to keep its feet in line.
		c.vel = c.vel.Add(c.legDrag().Mul(-dt))
	}
	if l := c.vel.Len(); l > c.maxSpeed*2.5 {
		c.vel = c.vel.Mul(c.maxSpeed * 2.5 / l)
	}
	c.pos = c.w.bounds.clampVec(c.pos.Add(c.vel.Mul(dt)), bodyMargin)

	// Face the way it is going, or watch the cursor when it is idling.
	facing, haveFacing := Vec{}, false
	switch {
	case walking && !walkDir.IsZero():
		facing, haveFacing = walkDir, true
	case c.watching && !c.dragging && c.cursorValid:
		facing, haveFacing = c.cursor.Sub(c.pos).Norm(), true
	}
	if haveFacing && !facing.IsZero() {
		want := math.Atan2(facing.Y, facing.X)
		diff := math.Remainder(want-c.angle, 2*math.Pi)
		step := c.turnRate * dt
		c.angle += clampf(diff, -step, step)
	}
}

// legDrag sums how hard the planted legs are pulling at the body.
func (c *creature) legDrag() Vec {
	var out Vec
	for _, lg := range c.legs {
		if !lg.planted {
			continue
		}
		d := lg.anchor.Sub(c.attachPoint(lg))
		l := d.Len()
		if over := l - lg.limit*dragFrom; over > 0 {
			out = out.Add(d.Mul(over * c.dragGain / l))
		}
	}
	if l := out.Len(); l > 150 {
		out = out.Mul(150 / l)
	}
	return out
}

// placeBody writes the body rig into its particles, with a touch of hover so it
// never looks pinned to the desktop.
func (c *creature) placeBody() {
	axis := V(math.Cos(c.angle), math.Sin(c.angle))
	sway := axis.Perp().Mul(math.Sin(c.time*1.6) * 1.3)
	sway = sway.Add(axis.Mul(math.Sin(c.time*0.9+1.2) * 1.2))
	base := c.pos.Add(sway)
	c.abdomen.setPos(base)
	c.head.setPos(base.Add(axis.Mul(bodySpan)))
}

// updateLegs runs the gait: airborne feet follow their arc, planted feet step
// once the body has stretched them far enough. Steps ripple outwards instead of
// firing all at once, so the creature always keeps a grip.
func (c *creature) updateLegs(dt float64, walkDir Vec, walking bool) {
	body := c.abdomen.pos
	for _, lg := range c.legs {
		if lg.restTimer > 0 {
			lg.restTimer -= dt
		}
	}

	for _, lg := range c.legs {
		if !lg.air {
			continue
		}
		lg.swing += dt / lg.dur
		t := clamp01(lg.swing)
		p := lg.from.Lerp(lg.to, easeInOut(t))
		d := lg.to.Sub(lg.from)
		n := d.Perp().Norm()
		if n.Dot(p.Sub(body)) < 0 {
			n = n.Mul(-1)
		}
		// The foot swings outwards on an arc, which is what gives the legs their
		// curved sweep.
		p = p.Add(n.Mul(math.Sin(math.Pi*t) * (d.Len()*0.25 + 6)))
		lg.tip.setPos(p)

		if lg.swing >= 1 {
			lg.air = false
			lg.planted = true
			lg.swing = -1
			lg.anchor = lg.to
			switch {
			case lg.settling:
				lg.restTimer = 0.15 + c.rng.Float64()*0.2
				lg.settling = false
			case walking || c.dragging:
				lg.restTimer = 0.12 + c.rng.Float64()*0.12
			default:
				lg.restTimer = 0.8 + c.rng.Float64()*1.6
			}
			c.stepCount++
			if c.onPlant != nil {
				c.onPlant(clamp01(0.3 + 0.7*(lg.to.Sub(body).Len()/lg.limit)))
			}
		}
	}

	maxInAir := 2
	trigger := walkTrigger
	switch {
	case c.dragging:
		maxInAir, trigger = 4, 0.8
	case !walking:
		// Idle legs are putting themselves back in line, so a couple may move at
		// once; it reads as the creature settling rather than shuffling.
		maxInAir, trigger = 3, idleTrigger
	}

	// On the move, the legs walk in a cycle: each one takes its turn, staggered
	// around the body, so every leg walks rather than only the ones being dragged
	// out of line by the direction of travel.
	gait := walking && !c.dragging
	cycle := 0.0
	if gait {
		cycle = c.strideCycle()
		if !c.wasWalking {
			c.staggerWalk(cycle)
		}
		for _, lg := range c.legs {
			if lg.nextStep > 0 {
				lg.nextStep = math.Max(0, lg.nextStep-dt)
			}
		}
	}
	c.wasWalking = walking
	if c.airCount() >= maxInAir {
		return
	}
	resting := !walking && !c.dragging
	n := len(c.legs)
	if n == 0 {
		return
	}
	for k := range n {
		i := (c.stepCursor + k) % n
		lg := c.legs[i]
		if lg.air || !lg.planted {
			continue
		}
		// Measured from where the leg meets the body, matching where steps are
		// placed. Using the hip particle instead adds its lag into the number and
		// makes legs step again the moment they land.
		stance := c.attachPoint(lg).Sub(lg.anchor).Len()
		// A walk leaves the legs drawn in and swung towards where it was going.
		// Once it stops, each leg steps back into its own slice of the fan, one at
		// a time, so the resting stance is evenly spread again.
		// Legs drawn in are only stretched back out for a while after a walk, but a
		// foot that has drifted out of its own slice is always stepped back: that is
		// what keeps the resting fan evenly spread.
		settling := resting && (c.outOfLine(lg) ||
			(c.settleTimer > 0 && stance < lg.limit*idleStance*0.95))
		if settling {
			lg.restTimer = 0
		}
		if lg.restTimer > 0 {
			continue
		}
		due := gait && lg.nextStep <= 0
		if !settling && !due && stance < lg.limit*trigger {
			continue
		}
		if c.neighbourInAir(i) {
			continue
		}
		c.startStep(lg, walkDir, walking)
		lg.settling = settling
		if gait {
			lg.nextStep = cycle
		}
		c.stepCursor = (i + 1) % n
		return
	}
}

// followBody trails the shell behind the pose, so a change of direction has some
// weight to it rather than snapping.
func (c *creature) followBody(dt float64) {
	k := 1 - math.Exp(-c.bodyFollow*dt)
	c.drawnPos = trail(c.drawnPos, c.pos, k)
	c.drawnAngle += math.Remainder(c.angle-c.drawnAngle, 2*math.Pi) * k
}

// follow trails a leg's joints behind their exact positions. The foot is not
// trailed: it stays where it was planted. A joint never trails further than
// maxTrail, so a fast swing reads as weight rather than a leg coming apart.
func (lg *leg) follow(dt, rate float64) {
	k := 1 - math.Exp(-rate*dt)
	lg.drawnKnee = trail(lg.drawnKnee, lg.knee.pos, k)
	lg.drawnShin = trail(lg.drawnShin, lg.shin.pos, k)
}

func trail(from, to Vec, k float64) Vec {
	p := from.Add(to.Sub(from).Mul(k))
	if d := p.Sub(to); d.Len() > maxTrail {
		p = to.Add(d.Norm().Mul(maxTrail))
	}
	return p
}

// drawnBody is the shell as drawn, and drawnAxis its heading.
func (c *creature) drawnBody() (Vec, Vec) {
	axis := V(math.Cos(c.drawnAngle), math.Sin(c.drawnAngle))
	return c.drawnPos, axis
}

// outOfLine reports whether a planted foot has drifted out of the leg's own
// direction around the body, which is what a walk leaves behind.
func (c *creature) outOfLine(lg *leg) bool {
	outward := c.attachPoint(lg).Sub(c.abdomen.pos).Norm()
	d := lg.anchor.Sub(c.abdomen.pos)
	if outward.IsZero() || d.Len2() < 1 {
		return false
	}
	off := math.Abs(math.Atan2(outward.Cross(d), outward.Dot(d))) * 180 / math.Pi
	return off > restSpread
}

// separateDirection turns a landing direction away from the legs on either side
// of it, so the resting legs stay a clean fan instead of crossing over one
// another.
func (c *creature) separateDirection(lg *leg, dir Vec, minGapDeg float64) Vec {
	minGap := minGapDeg * math.Pi / 180
	body := c.abdomen.pos
	n := len(c.legs)
	if n == 0 {
		return dir
	}
	for _, d := range [...]int{1, -1} {
		j := (lg.index + d + n) % n
		other := c.legs[j].anchor.Sub(body)
		if other.Len2() < 1 {
			continue
		}
		ang := math.Atan2(dir.Cross(other), dir.Dot(other))
		if gap := math.Abs(ang); gap < minGap {
			dir = dir.Rot(-sign(ang) * (minGap - gap))
		}
	}
	return dir
}

// staggerWalk hands out the walking cycle: a leg's turn comes a little later than
// the leg in front of it on the same side, and the other side runs half a cycle
// behind its mirror. That is an alternating gait -- a leg and its mirror are never
// in the air together -- rather than the whole fan marching round in one
// direction.
func (c *creature) staggerWalk(cycle float64) {
	n := len(c.legs)
	if n == 0 {
		return
	}
	perSide := n / 2
	for k, lg := range c.legs {
		idx := k % perSide
		if k >= perSide {
			// The far side is listed back to front, so count from its end.
			idx = (n - 1 - k) % perSide
		}
		phase := float64(idx) / float64(perSide)
		if k >= perSide {
			phase += 0.5
		}
		lg.nextStep = math.Mod(phase, 1) * cycle
	}
}

// strideCycle is how long one full walking cycle takes: long enough that the body
// covers a stride's worth of ground between a leg's turns, and quicker when the
// creature is hurrying.
func (c *creature) strideCycle() float64 {
	if len(c.legs) == 0 {
		return 1
	}
	mean := 0.0
	for _, lg := range c.legs {
		mean += lg.limit
	}
	mean /= float64(len(c.legs))
	speed := math.Max(c.vel.Len(), c.maxSpeed*0.35)
	return clampf(1.2*mean/speed, 0.5, 4)
}

func (c *creature) neighbourInAir(i int) bool {
	n := len(c.legs)
	if n == 0 {
		return false
	}
	// Neighbours either side of it in the fan, and its mirror on the other side.
	for _, d := range [...]int{1, -1, n - 1 - 2*i} {
		j := ((i+d)%n + n) % n
		if c.legs[j].air {
			return true
		}
	}
	return false
}

func (c *creature) startStep(lg *leg, walkDir Vec, walking bool) {
	// Step out along the leg's own direction on the body, not along wherever the
	// hip particle has drifted to: that keeps the fan evenly spaced instead of
	// slowly scrambling it as the hips lag under load.
	outward := c.attachPoint(lg).Sub(c.abdomen.pos).Norm()
	if outward.IsZero() {
		outward = V(1, 0)
	}
	var dir Vec
	var scale float64
	switch {
	case c.dragging:
		dir = outward.Rot((c.rng.Float64() - 0.5) * 0.9)
		scale = 0.82 + c.rng.Float64()*0.08
	case walking && !walkDir.IsZero():
		// Every leg lands ahead of its hip along the direction of travel, keeping to
		// its own side of the body. A leg that lands behind the body is stretched
		// again within a few pixels and steps at once, which is what made the walk
		// flicker: the feet have to be planted *ahead*, rear legs included.
		own := outward.Sub(walkDir.Mul(outward.Dot(walkDir)))
		if own.Len2() < 1e-6 {
			own = walkDir.Perp().Mul(sign(outward.Cross(walkDir)))
		}
		offset := walkDir.Mul(lg.limit * legLead).Add(own.Norm().Mul(lg.limit * legSide))
		dir = offset.Norm()
		scale = offset.Len() / lg.limit
	default:
		// Idling feet stand well out, so a resting creature keeps the long, fanned
		// stance of the reference while still swaying without shuffling.
		dir = outward
		// Land clearly inside the idle trigger, or the leg would step again the
		// moment it touched down.
		scale = idleStance + c.rng.Float64()*0.03
	}
	// Measured from where the leg meets the body, not from the hip particle, which
	// lags behind under load: otherwise every landing lands slightly off its own
	// line and the fan slowly skews.
	to := c.w.bounds.clampVec(c.attachPoint(lg).Add(dir.Mul(lg.limit*scale)), 6)

	lg.stepCount++
	lg.from = lg.tip.pos
	lg.to = to
	lg.swing = 0
	lg.air = true
	lg.planted = false
	// A step lasts about as long as the body needs to cover the distance, so the
	// foot is never left behind by a fast walk.
	// A long reach wants a long time: a foot that covers a stride in a tenth of a
	// second reads as a twitch rather than a step.
	lg.dur = clampf(0.10+0.15*(lg.to.Sub(lg.from).Len()/lg.limit), 0.10, 0.32)
	if c.dragging {
		lg.dur *= 0.7
	}
}

// updateStrands advances the silk. A strand that goes taut slips its anchor,
// then fades and is re-laid near the creature, leaving a trail behind it.
func (c *creature) updateStrands(dt float64) {
	for _, s := range c.strands {
		far := s.nodes[strandNodes-1]
		d := far.pos.Sub(s.nodes[0].pos).Len()
		s.taut = clamp01((d/s.maxLen - 0.35) / 0.45)
		if !s.dying && d > s.maxLen*0.78 {
			s.dying = true
			far.invMass = 0.4
		}
		if s.dying {
			s.fade -= dt / 0.7
			if s.fade <= 0 {
				c.layStrand(s)
			}
			continue
		}
		s.fade = math.Min(1, s.fade+dt*1.6)
		for i, p := range s.nodes {
			if p.invMass == 0 {
				continue
			}
			ph := s.phase[i]
			// Slow drifting force: keeps the silk wavy rather than dead straight.
			accel(p, V(math.Sin(c.time*1.1+ph), math.Cos(c.time*0.9+ph*1.7)).Mul(24))
		}
	}
}

func (c *creature) updateChatter(dt float64) {
	c.chatterTimer -= dt
	if c.chatterTimer > 0 {
		return
	}
	c.chatterTimer = 7 + c.rng.Float64()*11
	if !c.hasTarget && !c.dragging && c.rng.Float64() < 0.55 && c.onChatter != nil {
		c.onChatter()
	}
}
