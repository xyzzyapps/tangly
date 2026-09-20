package main

import (
	"encoding/binary"
	"log"
	"math"
	"math/rand/v2"

	"github.com/hajimehoshi/ebiten/v2/audio"
)

const (
	sampleRate = 44100
	volumeCap  = 0.4
)

// samples converts a duration in seconds into a frame count.
func samples(seconds float64) int { return int(seconds * sampleRate) }

// sound is a pool of players over a handful of pre-rendered variants, so that
// triggering a sound never allocates and rapid triggers do not cut each other
// off.
type sound struct {
	players  []*audio.Player
	next     int
	vol      float64
	minGap   int // minimum ticks between triggers
	lastTick int
}

type soundBank struct {
	ctx     *audio.Context
	master  float64
	muted   bool
	tick    int
	step    *sound
	chirp   *sound
	settle  *sound
	chatter *sound

	rng *rand.Rand
}

// soundSpec is what a script can change about a voice.
type soundSpec struct {
	Volume float64 // relative to the other voices
	Pitch  float64 // frequency multiplier, 1 = as designed
	Decay  float64 // how fast it dies away, 1 = as designed
	MinGap int     // ticks between triggers
}

// newSoundBank builds every voice up front. If the platform has no usable audio
// device it returns nil and the creature simply runs silent.
func newSoundBank() (bank *soundBank) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("tangly: audio unavailable (%v), running silent", r)
			bank = nil
		}
	}()

	ctx := audio.NewContext(sampleRate)
	b := &soundBank{ctx: ctx, master: 0.13, rng: rand.New(rand.NewPCG(0x5eed1d, 0xc0ffee))}
	// Tiny on purpose: the creature is small, so its noises are barely there.
	for name, spec := range defaultVoices() {
		b.rebuild(name, spec)
	}
	return b
}

// defaultVoices is the shipped set of noises.
func defaultVoices() map[string]soundSpec {
	return map[string]soundSpec{
		"step":    {Volume: 0.34, Pitch: 1, Decay: 1, MinGap: 2},
		"chirp":   {Volume: 0.24, Pitch: 1, Decay: 1, MinGap: 4},
		"settle":  {Volume: 0.2, Pitch: 1, Decay: 1, MinGap: 20},
		"chatter": {Volume: 0.18, Pitch: 1, Decay: 1, MinGap: 90},
	}
}

// rebuild renders a voice again from its spec, which is how a script changes how
// the creature sounds.
func (b *soundBank) rebuild(name string, spec soundSpec) bool {
	gen, variants := voiceGen(name)
	if gen == nil {
		return false
	}
	if spec.Pitch <= 0 {
		spec.Pitch = 1
	}
	if spec.Decay <= 0 {
		spec.Decay = 1
	}
	old := b.voice(name)
	if old != nil {
		for _, p := range old.players {
			_ = p.Close()
		}
	}
	s := &sound{vol: spec.Volume, minGap: spec.MinGap, lastTick: -1 << 30}
	if s.minGap < 0 {
		s.minGap = 0
	}
	for v := range variants {
		data := f32Bytes(gen(b.rng, v, spec.Pitch, spec.Decay))
		for range 2 { // two players per variant, so repeats can overlap
			s.players = append(s.players, b.ctx.NewPlayerF32FromBytes(data))
		}
	}
	switch name {
	case "step":
		b.step = s
	case "chirp":
		b.chirp = s
	case "settle":
		b.settle = s
	case "chatter":
		b.chatter = s
	}
	return true
}

func (b *soundBank) voice(name string) *sound {
	switch name {
	case "step":
		return b.step
	case "chirp":
		return b.chirp
	case "settle":
		return b.settle
	case "chatter":
		return b.chatter
	}
	return nil
}

// playNamed triggers a voice by name, which is how a script auditions a sound.
func (b *soundBank) playNamed(name string, scale float64) bool {
	v := b.voice(name)
	if v == nil {
		return false
	}
	b.play(v, scale)
	return true
}

func voiceGen(name string) (genFunc, int) {
	switch name {
	case "step":
		return tickGen, 4
	case "chirp":
		return chirpGen, 4
	case "settle":
		return settleGen, 3
	case "chatter":
		return chatterGen, 3
	}
	return nil, 0
}

type genFunc func(rng *rand.Rand, variant int, pitch, decay float64) []float32

func (b *soundBank) beginFrame() {
	if b == nil {
		return
	}
	b.tick++
}

func (b *soundBank) play(s *sound, scale float64) {
	if b == nil || s == nil || b.muted || len(s.players) == 0 {
		return
	}
	if b.tick-s.lastTick < s.minGap {
		return
	}
	s.lastTick = b.tick

	p := s.players[s.next]
	s.next = (s.next + 1) % len(s.players)
	_ = p.Rewind()
	p.SetVolume(clampf(b.master*s.vol*scale, 0, volumeCap))
	p.Play()
}

func (b *soundBank) setVolume(v float64) float64 {
	if b == nil {
		return 0
	}
	b.master = clampf(v, 0, volumeCap)
	return b.master
}

// tickGen is the footfall: a very short bright click.
func tickGen(rng *rand.Rand, variant int, pitch, decay float64) []float32 {
	f := (1650 + float64(variant)*190 + rng.Float64()*160) * pitch
	n := samples(0.013)
	out := make([]float32, n*2)
	phase := rng.Float64() * 2 * math.Pi
	var lp float64
	for i := range n {
		t := float64(i) / sampleRate
		env := math.Exp(-t * 400 * decay)
		noise := rng.Float64()*2 - 1
		hp := noise - lp // crude high pass: makes the click crisp
		lp += hp * 0.4
		s := (math.Sin(phase+2*math.Pi*f*t)*0.45 + hp*0.55) * env * 0.5
		out[2*i], out[2*i+1] = float32(s), float32(s)
	}
	return out
}

// chirpGen is the acknowledgement when the creature is sent somewhere.
func chirpGen(rng *rand.Rand, variant int, pitch, decay float64) []float32 {
	f0 := (900 + float64(variant)*110 + rng.Float64()*50) * pitch
	f1 := f0 * (1.55 + rng.Float64()*0.25)
	const dur = 0.055
	n := samples(dur)
	out := make([]float32, n*2)
	var phase float64
	for i := range n {
		t := float64(i) / sampleRate
		u := t / dur
		phase += 2 * math.Pi * (f0 + (f1-f0)*u) / sampleRate
		env := math.Min(1, t/0.005) * math.Exp(-t*38*decay)
		s := (math.Sin(phase)*0.55 + math.Sin(phase*2.01)*0.12) * env * 0.4
		out[2*i], out[2*i+1] = float32(s), float32(s)
	}
	return out
}

// settleGen is the soft landing sound when it arrives.
func settleGen(rng *rand.Rand, variant int, pitch, decay float64) []float32 {
	f := (620 + float64(variant)*70 + rng.Float64()*30) * pitch
	n := samples(0.045)
	out := make([]float32, n*2)
	phase := rng.Float64() * 2 * math.Pi
	for i := range n {
		t := float64(i) / sampleRate
		env := math.Min(1, t/0.004) * math.Exp(-t*55*decay)
		s := (math.Sin(phase+2*math.Pi*f*t)*0.7 + math.Sin(phase*1.5)*0.1) * env * 0.38
		out[2*i], out[2*i+1] = float32(s), float32(s)
	}
	return out
}

// chatterGen is a tiny double tick, played rarely while the creature idles.
func chatterGen(rng *rand.Rand, variant int, pitch, decay float64) []float32 {
	first := tickGen(rng, variant, pitch, decay)
	gap := make([]float32, samples(0.05)*2)
	second := tickGen(rng, variant+2, pitch, decay)
	out := make([]float32, 0, len(first)+len(gap)+len(second))
	out = append(out, first...)
	out = append(out, gap...)
	out = append(out, second...)
	return out
}

// f32Bytes converts interleaved stereo samples into the little-endian 32-bit
// float format the audio context expects.
func f32Bytes(samples []float32) []byte {
	b := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(b[i*4:], math.Float32bits(s))
	}
	return b
}
