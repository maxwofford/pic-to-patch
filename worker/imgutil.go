package main

import (
	"image"
	"image/color"
	"math"
	"math/rand/v2"
	"sort"
)

// GrayF is a floating-point grayscale image.
type GrayF struct {
	Pix  []float64
	W, H int
}

func newGrayF(w, h int) *GrayF {
	return &GrayF{Pix: make([]float64, w*h), W: w, H: h}
}

func newGrayFFill(w, h int, v float64) *GrayF {
	g := newGrayF(w, h)
	for i := range g.Pix {
		g.Pix[i] = v
	}
	return g
}

func (g *GrayF) at(x, y int) float64 {
	if x < 0 || x >= g.W || y < 0 || y >= g.H {
		return 0
	}
	return g.Pix[y*g.W+x]
}

func (g *GrayF) set(x, y int, v float64) {
	if x >= 0 && x < g.W && y >= 0 && y < g.H {
		g.Pix[y*g.W+x] = v
	}
}

func (g *GrayF) clone() *GrayF {
	out := newGrayF(g.W, g.H)
	copy(out.Pix, g.Pix)
	return out
}

// RGBF is a floating-point RGB image (interleaved R,G,B).
type RGBF struct {
	Pix  []float64
	W, H int
}

func newRGBF(w, h int) *RGBF {
	return &RGBF{Pix: make([]float64, w*h*3), W: w, H: h}
}

func (img *RGBF) at(x, y, c int) float64 {
	if x < 0 || x >= img.W || y < 0 || y >= img.H {
		return 0
	}
	return img.Pix[(y*img.W+x)*3+c]
}

func (img *RGBF) set(x, y, c int, v float64) {
	if x >= 0 && x < img.W && y >= 0 && y < img.H {
		img.Pix[(y*img.W+x)*3+c] = v
	}
}

func (img *RGBF) clone() *RGBF {
	out := newRGBF(img.W, img.H)
	copy(out.Pix, img.Pix)
	return out
}

func rgbfFromImage(src image.Image) *RGBF {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	img := newRGBF(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, g, bl, _ := src.At(x+b.Min.X, y+b.Min.Y).RGBA()
			idx := (y*w + x) * 3
			img.Pix[idx] = float64(r) / 257.0
			img.Pix[idx+1] = float64(g) / 257.0
			img.Pix[idx+2] = float64(bl) / 257.0
		}
	}
	return img
}

func (img *RGBF) toImage() *image.NRGBA {
	out := image.NewNRGBA(image.Rect(0, 0, img.W, img.H))
	for y := 0; y < img.H; y++ {
		for x := 0; x < img.W; x++ {
			idx := (y*img.W + x) * 3
			out.SetNRGBA(x, y, color.NRGBA{
				R: clampByte(img.Pix[idx]),
				G: clampByte(img.Pix[idx+1]),
				B: clampByte(img.Pix[idx+2]),
				A: 255,
			})
		}
	}
	return out
}

// Channel extraction
func luminance(img *RGBF) *GrayF {
	out := newGrayF(img.W, img.H)
	for i := 0; i < img.W*img.H; i++ {
		out.Pix[i] = 0.299*img.Pix[i*3] + 0.587*img.Pix[i*3+1] + 0.114*img.Pix[i*3+2]
	}
	return out
}

func meanRGB(img *RGBF) *GrayF {
	out := newGrayF(img.W, img.H)
	for i := 0; i < img.W*img.H; i++ {
		out.Pix[i] = (img.Pix[i*3] + img.Pix[i*3+1] + img.Pix[i*3+2]) / 3.0
	}
	return out
}

// Gaussian blur (separable, clamp-to-edge)
func gaussianKernel(sigma float64) []float64 {
	radius := int(math.Ceil(sigma * 3))
	if radius < 1 {
		radius = 1
	}
	size := 2*radius + 1
	kernel := make([]float64, size)
	sum := 0.0
	for i := 0; i < size; i++ {
		x := float64(i - radius)
		kernel[i] = math.Exp(-x * x / (2 * sigma * sigma))
		sum += kernel[i]
	}
	for i := range kernel {
		kernel[i] /= sum
	}
	return kernel
}

func clampIdx(v, max int) int {
	if v < 0 {
		return 0
	}
	if v >= max {
		return max - 1
	}
	return v
}

func gaussBlur(src *GrayF, sigma float64) *GrayF {
	if sigma <= 0 {
		return src.clone()
	}
	kernel := gaussianKernel(sigma)
	radius := len(kernel) / 2
	w, h := src.W, src.H

	tmp := newGrayF(w, h)
	for y := 0; y < h; y++ {
		row := y * w
		for x := 0; x < w; x++ {
			sum := 0.0
			for k := -radius; k <= radius; k++ {
				sum += src.Pix[row+clampIdx(x+k, w)] * kernel[k+radius]
			}
			tmp.Pix[row+x] = sum
		}
	}

	out := newGrayF(w, h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sum := 0.0
			for k := -radius; k <= radius; k++ {
				sum += tmp.Pix[clampIdx(y+k, h)*w+x] * kernel[k+radius]
			}
			out.Pix[y*w+x] = sum
		}
	}
	return out
}

func gaussBlurRGB(src *RGBF, sigma float64) *RGBF {
	if sigma <= 0 {
		return src.clone()
	}
	channels := make([]*GrayF, 3)
	for c := 0; c < 3; c++ {
		ch := newGrayF(src.W, src.H)
		for i := 0; i < src.W*src.H; i++ {
			ch.Pix[i] = src.Pix[i*3+c]
		}
		channels[c] = gaussBlur(ch, sigma)
	}
	out := newRGBF(src.W, src.H)
	for i := 0; i < src.W*src.H; i++ {
		out.Pix[i*3] = channels[0].Pix[i]
		out.Pix[i*3+1] = channels[1].Pix[i]
		out.Pix[i*3+2] = channels[2].Pix[i]
	}
	return out
}

// Directional gaussian blur (blur more along one axis)
func gaussBlurDirectional(src *GrayF, sigmaX, sigmaY float64) *GrayF {
	if sigmaX <= 0 && sigmaY <= 0 {
		return src.clone()
	}
	w, h := src.W, src.H

	tmp := src.clone()
	if sigmaX > 0 {
		kx := gaussianKernel(sigmaX)
		rx := len(kx) / 2
		t2 := newGrayF(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sum := 0.0
				for k := -rx; k <= rx; k++ {
					sum += tmp.Pix[y*w+clampIdx(x+k, w)] * kx[k+rx]
				}
				t2.Pix[y*w+x] = sum
			}
		}
		tmp = t2
	}
	if sigmaY > 0 {
		ky := gaussianKernel(sigmaY)
		ry := len(ky) / 2
		out := newGrayF(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sum := 0.0
				for k := -ry; k <= ry; k++ {
					sum += tmp.Pix[clampIdx(y+k, h)*w+x] * ky[k+ry]
				}
				out.Pix[y*w+x] = sum
			}
		}
		return out
	}
	return tmp
}

// Sobel filters
func sobelX(src *GrayF) *GrayF {
	out := newGrayF(src.W, src.H)
	w := src.W
	for y := 1; y < src.H-1; y++ {
		for x := 1; x < src.W-1; x++ {
			v := -src.Pix[(y-1)*w+(x-1)] + src.Pix[(y-1)*w+(x+1)] +
				-2*src.Pix[y*w+(x-1)] + 2*src.Pix[y*w+(x+1)] +
				-src.Pix[(y+1)*w+(x-1)] + src.Pix[(y+1)*w+(x+1)]
			out.Pix[y*w+x] = v
		}
	}
	return out
}

func sobelY(src *GrayF) *GrayF {
	out := newGrayF(src.W, src.H)
	w := src.W
	for y := 1; y < src.H-1; y++ {
		for x := 1; x < src.W-1; x++ {
			v := -src.Pix[(y-1)*w+(x-1)] - 2*src.Pix[(y-1)*w+x] - src.Pix[(y-1)*w+(x+1)] +
				src.Pix[(y+1)*w+(x-1)] + 2*src.Pix[(y+1)*w+x] + src.Pix[(y+1)*w+(x+1)]
			out.Pix[y*w+x] = v
		}
	}
	return out
}

// Morphological operations
func binaryDilate(src *GrayF, iterations int) *GrayF {
	cur := src.clone()
	w, h := cur.W, cur.H
	for iter := 0; iter < iterations; iter++ {
		next := newGrayF(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				hit := false
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						yy, xx := y+dy, x+dx
						if yy >= 0 && yy < h && xx >= 0 && xx < w && cur.Pix[yy*w+xx] > 0.5 {
							hit = true
						}
					}
				}
				if hit {
					next.Pix[y*w+x] = 1
				}
			}
		}
		cur = next
	}
	return cur
}

func binaryErode(src *GrayF, iterations int) *GrayF {
	cur := src.clone()
	w, h := cur.W, cur.H
	for iter := 0; iter < iterations; iter++ {
		next := newGrayF(w, h)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				all := true
				for dy := -1; dy <= 1; dy++ {
					for dx := -1; dx <= 1; dx++ {
						yy, xx := y+dy, x+dx
						if yy < 0 || yy >= h || xx < 0 || xx >= w || cur.Pix[yy*w+xx] <= 0.5 {
							all = false
						}
					}
				}
				if all {
					next.Pix[y*w+x] = 1
				}
			}
		}
		cur = next
	}
	return cur
}

func binaryClose(src *GrayF, iterations int) *GrayF {
	return binaryErode(binaryDilate(src, iterations), iterations)
}

// Distance transform (Meijster et al. exact EDT)
func distanceTransformEDT(binary *GrayF) *GrayF {
	w, h := binary.W, binary.H
	inf := float64(w + h)

	g := make([]float64, w*h)
	for x := 0; x < w; x++ {
		if binary.Pix[x] > 0.5 {
			g[x] = 0
		} else {
			g[x] = inf
		}
		for y := 1; y < h; y++ {
			if binary.Pix[y*w+x] > 0.5 {
				g[y*w+x] = 0
			} else {
				g[y*w+x] = g[(y-1)*w+x] + 1
			}
		}
		for y := h - 2; y >= 0; y-- {
			if g[(y+1)*w+x]+1 < g[y*w+x] {
				g[y*w+x] = g[(y+1)*w+x] + 1
			}
		}
	}

	out := newGrayF(w, h)
	s := make([]int, w)
	t := make([]int, w)

	edtF := func(x, i int, gi float64) float64 {
		dx := float64(x - i)
		return dx*dx + gi*gi
	}

	for y := 0; y < h; y++ {
		q := 0
		s[0] = 0
		t[0] = 0

		for u := 1; u < w; u++ {
			for q >= 0 && edtF(t[q], s[q], g[y*w+s[q]]) > edtF(t[q], u, g[y*w+u]) {
				q--
			}
			if q < 0 {
				q = 0
				s[0] = u
			} else {
				sep := edtSep(s[q], u, g[y*w+s[q]], g[y*w+u], w)
				if sep < w {
					q++
					s[q] = u
					t[q] = sep
				}
			}
		}

		for u := w - 1; u >= 0; u-- {
			out.Pix[y*w+u] = math.Sqrt(edtF(u, s[q], g[y*w+s[q]]))
			if u == t[q] && q > 0 {
				q--
			}
		}
	}

	return out
}

func edtSep(i, u int, gi, gu float64, _ int) int {
	num := float64(u*u-i*i) + gu*gu - gi*gi
	den := 2.0 * float64(u-i)
	return int(math.Floor(num/den)) + 1
}

// Shift image by (dx, dy) with bilinear interpolation
func shiftImage(src *GrayF, dx, dy float64) *GrayF {
	out := newGrayF(src.W, src.H)
	for y := 0; y < src.H; y++ {
		for x := 0; x < src.W; x++ {
			sx := float64(x) - dx
			sy := float64(y) - dy
			x0 := int(math.Floor(sx))
			y0 := int(math.Floor(sy))
			fx := sx - float64(x0)
			fy := sy - float64(y0)
			v := src.at(x0, y0)*(1-fx)*(1-fy) +
				src.at(x0+1, y0)*fx*(1-fy) +
				src.at(x0, y0+1)*(1-fx)*fy +
				src.at(x0+1, y0+1)*fx*fy
			out.Pix[y*src.W+x] = v
		}
	}
	return out
}

// Noise generation
func normalNoise(w, h int) *GrayF {
	out := newGrayF(w, h)
	for i := range out.Pix {
		out.Pix[i] = rand.NormFloat64()
	}
	return out
}

func normalNoiseRGB(w, h int) *RGBF {
	out := newRGBF(w, h)
	for i := range out.Pix {
		out.Pix[i] = rand.NormFloat64()
	}
	return out
}

// Utility
func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func clampByte(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func percentile(data *GrayF, mask *GrayF, p float64) float64 {
	var vals []float64
	for i, v := range mask.Pix {
		if v > 0.5 {
			vals = append(vals, data.Pix[i])
		}
	}
	if len(vals) == 0 {
		return 1.0
	}
	sort.Float64s(vals)
	idx := p / 100.0 * float64(len(vals)-1)
	lo := int(math.Floor(idx))
	hi := int(math.Ceil(idx))
	if hi >= len(vals) {
		hi = len(vals) - 1
	}
	frac := idx - float64(lo)
	return vals[lo]*(1-frac) + vals[hi]*frac
}

func absMax(g *GrayF) float64 {
	m := 0.0
	for _, v := range g.Pix {
		if math.Abs(v) > m {
			m = math.Abs(v)
		}
	}
	return m
}

// Upscale a small image by repeating pixels then blurring
func upscaleBlur(src *GrayF, targetW, targetH int, scale int, sigma float64) *GrayF {
	// Nearest-neighbor upscale
	up := newGrayF(targetW, targetH)
	for y := 0; y < targetH; y++ {
		sy := y / scale
		if sy >= src.H {
			sy = src.H - 1
		}
		for x := 0; x < targetW; x++ {
			sx := x / scale
			if sx >= src.W {
				sx = src.W - 1
			}
			up.Pix[y*targetW+x] = src.Pix[sy*src.W+sx]
		}
	}
	if sigma > 0 {
		return gaussBlur(up, sigma)
	}
	return up
}

func upscaleBlurRGB(src *RGBF, targetW, targetH int, scale int, sigma float64) *RGBF {
	up := newRGBF(targetW, targetH)
	for y := 0; y < targetH; y++ {
		sy := y / scale
		if sy >= src.H {
			sy = src.H - 1
		}
		for x := 0; x < targetW; x++ {
			sx := x / scale
			if sx >= src.W {
				sx = src.W - 1
			}
			di := (y*targetW + x) * 3
			si := (sy*src.W + sx) * 3
			up.Pix[di] = src.Pix[si]
			up.Pix[di+1] = src.Pix[si+1]
			up.Pix[di+2] = src.Pix[si+2]
		}
	}
	if sigma > 0 {
		return gaussBlurRGB(up, sigma)
	}
	return up
}
