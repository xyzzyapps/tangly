package main

import "math"

// Vec is a 2D vector in window space (logical pixels).
type Vec struct{ X, Y float64 }

func V(x, y float64) Vec { return Vec{X: x, Y: y} }

func (v Vec) Add(o Vec) Vec     { return Vec{v.X + o.X, v.Y + o.Y} }
func (v Vec) Sub(o Vec) Vec     { return Vec{v.X - o.X, v.Y - o.Y} }
func (v Vec) Mul(s float64) Vec { return Vec{v.X * s, v.Y * s} }
func (v Vec) Dot(o Vec) float64 { return v.X*o.X + v.Y*o.Y }
func (v Vec) Cross(o Vec) float64 {
	return v.X*o.Y - v.Y*o.X
}
func (v Vec) Len2() float64 { return v.X*v.X + v.Y*v.Y }
func (v Vec) Len() float64  { return math.Hypot(v.X, v.Y) }

// Norm returns the unit vector, or the zero vector when the length is negligible.
func (v Vec) Norm() Vec {
	l := v.Len()
	if l < 1e-9 {
		return Vec{}
	}
	return Vec{v.X / l, v.Y / l}
}

// Perp returns v rotated a quarter turn.
func (v Vec) Perp() Vec { return Vec{-v.Y, v.X} }

func (v Vec) Rot(ang float64) Vec {
	c, s := math.Cos(ang), math.Sin(ang)
	return Vec{v.X*c - v.Y*s, v.X*s + v.Y*c}
}

func (v Vec) Lerp(o Vec, t float64) Vec { return v.Add(o.Sub(v).Mul(t)) }

func (v Vec) IsZero() bool { return v.X == 0 && v.Y == 0 }

func clampf(v, lo, hi float64) float64 {
	if lo > hi {
		lo, hi = hi, lo
	}
	return math.Min(math.Max(v, lo), hi)
}

func clamp01(v float64) float64 { return clampf(v, 0, 1) }

func lerpf(a, b, t float64) float64 { return a + (b-a)*t }

func easeInOut(t float64) float64 {
	t = clamp01(t)
	if t < 0.5 {
		return 4 * t * t * t
	}
	return 1 - math.Pow(-2*t+2, 3)/2
}

func sign(f float64) float64 {
	if f < 0 {
		return -1
	}
	return 1
}
