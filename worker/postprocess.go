package main

import (
	"fmt"
	"image"
	"image/png"
	"math"
	"math/rand/v2"
	"os"
	"time"
)

const postprocessPadding = 55

func postprocessPhotorealistic(inputPath, outputPath string) error {
	f, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	src, _, err := image.Decode(f)
	f.Close()
	if err != nil {
		return err
	}

	img := rgbfFromImage(src)
	h, w := img.H, img.W

	stage := func(name string) func() {
		t0 := time.Now()
		fmt.Printf("  %s...", name)
		return func() { fmt.Printf(" %.2fs\n", time.Since(t0).Seconds()) }
	}

	done := stage("[1/13] Extracting patch mask")
	mask := extractPatchMask(img)
	done()

	done = stage("[2/13] Computing normal map")
	normals := computeNormalMap(img, mask, 2.0)
	done()

	done = stage("[3/13] Estimating thread directions")
	threadAngle, anisotropy := estimateThreadDirection(img, mask, 12)
	done()

	done = stage("[4/13] Applying Blinn-Phong + anisotropic shading")
	lit := applyLighting(img, normals, mask, threadAngle, anisotropy)
	done()

	done = stage("[5/13] Computing ambient occlusion")
	ao := computeAmbientOcclusion(img, mask, 2.0, 0.12)
	for i := 0; i < w*h; i++ {
		scale := 1.0 - ao.Pix[i]
		lit.Pix[i*3] = clampF(lit.Pix[i*3]*scale, 0, 255)
		lit.Pix[i*3+1] = clampF(lit.Pix[i*3+1]*scale, 0, 255)
		lit.Pix[i*3+2] = clampF(lit.Pix[i*3+2]*scale, 0, 255)
	}
	done()

	done = stage("[6/13] Adding per-thread micro-highlights")
	lit = addThreadMicrohighlights(lit, mask, threadAngle, 0.05)
	done()

	canvasW := w + postprocessPadding*2
	canvasH := h + postprocessPadding*2

	done = stage(fmt.Sprintf("[7/13] Generating fabric texture (%dx%d)", canvasW, canvasH))
	canvas := generateFabricTexture(canvasW, canvasH)
	done()

	maskPadded := newGrayF(canvasW, canvasH)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			maskPadded.Pix[(y+postprocessPadding)*canvasW+(x+postprocessPadding)] = mask.Pix[y*w+x]
		}
	}

	done = stage("[8/13] Creating drop shadow")
	shadow := createPatchShadow(maskPadded, 5, 6, 10, 0.50)
	for i := 0; i < canvasW*canvasH; i++ {
		scale := 1.0 - shadow.Pix[i]*0.8
		canvas.Pix[i*3] *= scale
		canvas.Pix[i*3+1] *= scale
		canvas.Pix[i*3+2] *= scale
	}
	done()

	done = stage("[9/13] Compositing patch onto fabric")
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m := mask.Pix[y*w+x]
			ci := ((y+postprocessPadding)*canvasW + (x + postprocessPadding)) * 3
			li := (y*w + x) * 3
			canvas.Pix[ci] = canvas.Pix[ci]*(1-m) + lit.Pix[li]*m
			canvas.Pix[ci+1] = canvas.Pix[ci+1]*(1-m) + lit.Pix[li+1]*m
			canvas.Pix[ci+2] = canvas.Pix[ci+2]*(1-m) + lit.Pix[li+2]*m
		}
	}
	done()

	done = stage("[10/13] Adding edge bevel for 3D thickness")
	bevel := createEdgeBevel(maskPadded, 5, 0.25, -0.35)
	for i := 0; i < canvasW*canvasH; i++ {
		b := bevel.Pix[i] * 45.0
		canvas.Pix[i*3] = clampF(canvas.Pix[i*3]+b, 0, 255)
		canvas.Pix[i*3+1] = clampF(canvas.Pix[i*3+1]+b, 0, 255)
		canvas.Pix[i*3+2] = clampF(canvas.Pix[i*3+2]+b, 0, 255)
	}
	done()

	done = stage("[11/13] Adding inner relief at section boundaries")
	innerRelief := createInnerRelief(img, mask, 0.25, -0.35, 0.07)
	reliefPadded := newGrayF(canvasW, canvasH)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			reliefPadded.Pix[(y+postprocessPadding)*canvasW+(x+postprocessPadding)] = innerRelief.Pix[y*w+x]
		}
	}
	for i := 0; i < canvasW*canvasH; i++ {
		scale := 1.0 + reliefPadded.Pix[i]
		canvas.Pix[i*3] = clampF(canvas.Pix[i*3]*scale, 0, 255)
		canvas.Pix[i*3+1] = clampF(canvas.Pix[i*3+1]*scale, 0, 255)
		canvas.Pix[i*3+2] = clampF(canvas.Pix[i*3+2]*scale, 0, 255)
	}
	done()

	done = stage("[12/13] Adding merrow edge border")
	applyMerrowEdge(canvas, maskPadded, lit, mask, 3, [3]float64{0.25, -0.35, 0.90})
	done()

	done = stage("[13/13] Photographic finishing")
	canvas = addDepthOfField(canvas, maskPadded, 1.2)
	colorGrade(canvas, 0.02, 1.06, 1.12)
	addVignette(canvas, 0.20, 0.60)
	addFilmGrain(canvas, 3.0)
	done()

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, canvas.toImage())
}

func extractPatchMask(img *RGBF) *GrayF {
	w, h := img.W, img.H
	mask := newGrayF(w, h)
	for i := 0; i < w*h; i++ {
		r, g, b := img.Pix[i*3], img.Pix[i*3+1], img.Pix[i*3+2]
		if r <= 235 || g <= 235 || b <= 235 {
			mask.Pix[i] = 1
		}
	}
	mask = binaryClose(mask, 3)
	mask = binaryDilate(mask, 1)
	mask = gaussBlur(mask, 1.2)
	for i := range mask.Pix {
		mask.Pix[i] = clampF(mask.Pix[i], 0, 1)
	}
	return mask
}

func computeNormalMap(img *RGBF, mask *GrayF, strength float64) *RGBF {
	lum := luminance(img)
	w, h := img.W, img.H

	gxFine := sobelX(lum)
	gyFine := sobelY(lum)

	lumMed := gaussBlur(lum, 1.2)
	gxMed := sobelX(lumMed)
	gyMed := sobelY(lumMed)

	lumCoarse := gaussBlur(lum, 3.0)
	gxCoarse := sobelX(lumCoarse)
	gyCoarse := sobelY(lumCoarse)

	normals := newRGBF(w, h)
	for i := 0; i < w*h; i++ {
		gx := 0.5*gxFine.Pix[i] + 0.35*gxMed.Pix[i] + 0.15*gxCoarse.Pix[i]
		gy := 0.5*gyFine.Pix[i] + 0.35*gyMed.Pix[i] + 0.15*gyCoarse.Pix[i]
		gx *= strength
		gy *= strength

		nx := -gx
		ny := -gy
		nz := 255.0
		length := math.Sqrt(nx*nx + ny*ny + nz*nz)
		if length < 1e-8 {
			length = 1e-8
		}
		m := mask.Pix[i]
		normals.Pix[i*3] = nx / length * m
		normals.Pix[i*3+1] = ny / length * m
		normals.Pix[i*3+2] = nz / length * m
	}
	return normals
}

func estimateThreadDirection(img *RGBF, mask *GrayF, blockSize int) (*GrayF, *GrayF) {
	lum := luminance(img)
	w, h := img.W, img.H

	gx := sobelX(lum)
	gy := sobelY(lum)

	sigma := float64(blockSize) / 2.0

	jxx := newGrayF(w, h)
	jxy := newGrayF(w, h)
	jyy := newGrayF(w, h)
	for i := 0; i < w*h; i++ {
		jxx.Pix[i] = gx.Pix[i] * gx.Pix[i]
		jxy.Pix[i] = gx.Pix[i] * gy.Pix[i]
		jyy.Pix[i] = gy.Pix[i] * gy.Pix[i]
	}
	jxx = gaussBlur(jxx, sigma)
	jxy = gaussBlur(jxy, sigma)
	jyy = gaussBlur(jyy, sigma)

	threadAngle := newGrayF(w, h)
	anisotropy := newGrayF(w, h)
	for i := 0; i < w*h; i++ {
		angle := 0.5 * math.Atan2(2.0*jxy.Pix[i], jxx.Pix[i]-jyy.Pix[i]+1e-10)
		threadAngle.Pix[i] = angle + math.Pi/2.0

		trace := jxx.Pix[i] + jyy.Pix[i] + 1e-10
		diff := math.Sqrt(math.Pow(jxx.Pix[i]-jyy.Pix[i], 2) + 4*jxy.Pix[i]*jxy.Pix[i])
		anisotropy.Pix[i] = diff / trace
	}
	return threadAngle, anisotropy
}

func applyLighting(img *RGBF, normals *RGBF, mask, threadAngle, anisotropy *GrayF) *RGBF {
	w, h := img.W, img.H
	result := img.clone()

	lx, ly, lz := 0.25, -0.35, 0.90
	ll := math.Sqrt(lx*lx + ly*ly + lz*lz)
	lx, ly, lz = lx/ll, ly/ll, lz/ll

	hx := lx
	hy := ly
	hz := lz + 1.0
	hl := math.Sqrt(hx*hx + hy*hy + hz*hz)
	hx, hy, hz = hx/hl, hy/hl, hz/hl

	ambient := 0.55
	diffStr := 0.35
	specStr := 0.12
	shininess := 18.0
	anisoStr := 0.08
	anisoShin := 30.0

	lum := meanRGB(img)

	for i := 0; i < w*h; i++ {
		m := mask.Pix[i]
		if m < 0.001 {
			continue
		}

		nx := normals.Pix[i*3]
		ny := normals.Pix[i*3+1]
		nz := normals.Pix[i*3+2]

		ndl := clampF(nx*lx+ny*ly+nz*lz, 0, 1)
		diffuse := diffStr * (ndl*0.5 + 0.5)

		ndh := clampF(nx*hx+ny*hy+nz*hz, 0, 1)
		specIso := specStr * math.Pow(ndh, shininess)

		ta := threadAngle.Pix[i]
		tx, ty := math.Cos(ta), math.Sin(ta)
		tdh := tx*hx + ty*hy
		sinTH := math.Sqrt(clampF(1.0-tdh*tdh, 0, 1))
		specAniso := anisoStr * math.Pow(sinTH, anisoShin) * anisotropy.Pix[i]

		brightnessBoost := clampF((lum.Pix[i]/255.0-0.4)/0.6, 0, 1)
		specBroad := 0.06 * math.Pow(ndh, 8.0) * brightnessBoost

		colorLight := clampF(ambient+diffuse, 0.35, 1.5)
		specTotal := (specIso + specAniso + specBroad) * m

		for c := 0; c < 3; c++ {
			ch := result.Pix[i*3+c]
			litVal := ch*colorLight + specTotal*220.0
			result.Pix[i*3+c] = ch*(1-m) + litVal*m
		}
	}

	for i := range result.Pix {
		result.Pix[i] = clampF(result.Pix[i], 0, 255)
	}
	return result
}

func computeAmbientOcclusion(img *RGBF, mask *GrayF, radius, strength float64) *GrayF {
	w, h := img.W, img.H
	lum := meanRGB(img)
	localAvg := gaussBlur(lum, radius)

	ao := newGrayF(w, h)
	for i := 0; i < w*h; i++ {
		avg := localAvg.Pix[i]
		v := clampF((avg-lum.Pix[i])/(avg+1e-8), 0, 1) * strength
		ao.Pix[i] = v * mask.Pix[i]
	}
	return ao
}

func addThreadMicrohighlights(img *RGBF, mask, threadAngle *GrayF, intensity float64) *RGBF {
	w, h := img.W, img.H
	noise := normalNoise(w, h)

	noiseAlong := gaussBlurDirectional(noise, 3.0, 0.3)
	noiseAcross := gaussBlurDirectional(noise, 0.3, 3.0)

	result := img.clone()
	for i := 0; i < w*h; i++ {
		cosA := math.Abs(math.Cos(threadAngle.Pix[i]))
		sinA := math.Abs(math.Sin(threadAngle.Pix[i]))
		total := cosA + sinA + 1e-8
		directional := (noiseAlong.Pix[i]*cosA + noiseAcross.Pix[i]*sinA) / total
		highlight := directional * intensity * mask.Pix[i]

		shimmer := rand.NormFloat64() * 0.015
		m := mask.Pix[i]

		for c := 0; c < 3; c++ {
			result.Pix[i*3+c] *= (1.0 + highlight) * (1.0 + shimmer*m)
		}
	}
	for i := range result.Pix {
		result.Pix[i] = clampF(result.Pix[i], 0, 255)
	}
	return result
}

func generateFabricTexture(w, h int) *RGBF {
	canvas := newRGBF(w, h)
	baseR, baseG, baseB := 35.0, 35.0, 40.0
	weaveScale := 3.0

	for y := 0; y < h; y++ {
		fy := float64(y)
		for x := 0; x < w; x++ {
			fx := float64(x)
			twill1 := math.Sin(2*math.Pi*(fx+fy)/weaveScale) * 0.035
			twill2 := math.Sin(2*math.Pi*(fx-fy)/(weaveScale*1.3)) * 0.02
			twill3 := math.Sin(2*math.Pi*(fx*0.7+fy*1.3)/(weaveScale*2)) * 0.015
			scale := 1.0 + twill1 + twill2 + twill3

			idx := (y*w + x) * 3
			canvas.Pix[idx] = baseR * scale
			canvas.Pix[idx+1] = baseG * scale
			canvas.Pix[idx+2] = baseB * scale
		}
	}

	noiseFine := normalNoiseRGB(w, h)
	for i := range noiseFine.Pix {
		noiseFine.Pix[i] *= 2.0
	}

	noiseMedSrc := normalNoiseRGB(w/2+1, h/2+1)
	for i := range noiseMedSrc.Pix {
		noiseMedSrc.Pix[i] *= 1.2
	}
	noiseMed := upscaleBlurRGB(noiseMedSrc, w, h, 2, 1.0)

	noiseCoarseSrc := normalNoiseRGB(w/6+1, h/6+1)
	for i := range noiseCoarseSrc.Pix {
		noiseCoarseSrc.Pix[i] *= 0.8
	}
	noiseCoarse := upscaleBlurRGB(noiseCoarseSrc, w, h, 6, 3.0)

	for i := range canvas.Pix {
		canvas.Pix[i] += noiseFine.Pix[i] + noiseMed.Pix[i] + noiseCoarse.Pix[i]
	}

	varSrc := normalNoise(w/12+1, h/12+1)
	for i := range varSrc.Pix {
		varSrc.Pix[i] *= 0.015
	}
	variation := upscaleBlur(varSrc, w, h, 12, 6.0)

	for i := 0; i < w*h; i++ {
		scale := 1.0 + variation.Pix[i]
		canvas.Pix[i*3] = clampF(canvas.Pix[i*3]*scale, 0, 255)
		canvas.Pix[i*3+1] = clampF(canvas.Pix[i*3+1]*scale, 0, 255)
		canvas.Pix[i*3+2] = clampF(canvas.Pix[i*3+2]*scale, 0, 255)
	}
	return canvas
}

func createPatchShadow(mask *GrayF, ox, oy, blurRadius int, opacity float64) *GrayF {
	shifted := shiftImage(mask, float64(ox), float64(oy))
	cast := gaussBlur(shifted, float64(blurRadius))
	for i := range cast.Pix {
		cast.Pix[i] *= opacity
	}

	dilated := gaussBlur(mask, 2.0)
	contact := newGrayF(mask.W, mask.H)
	for i := range contact.Pix {
		contact.Pix[i] = clampF(dilated.Pix[i]-mask.Pix[i]*0.9, 0, 1)
	}
	contact = gaussBlur(contact, 2.5)
	for i := range contact.Pix {
		contact.Pix[i] *= 0.4
	}

	shadow := newGrayF(mask.W, mask.H)
	for i := range shadow.Pix {
		shadow.Pix[i] = math.Max(cast.Pix[i], contact.Pix[i])
		shadow.Pix[i] *= clampF(1.0-mask.Pix[i], 0, 1)
		shadow.Pix[i] = clampF(shadow.Pix[i], 0, 1)
	}
	return shadow
}

func createEdgeBevel(mask *GrayF, bevelWidth int, lightX, lightY float64) *GrayF {
	w, h := mask.W, mask.H
	bw := float64(bevelWidth)

	hardMask := newGrayF(w, h)
	invMask := newGrayF(w, h)
	for i := range mask.Pix {
		if mask.Pix[i] > 0.5 {
			hardMask.Pix[i] = 1
		} else {
			invMask.Pix[i] = 1
		}
	}

	dist := distanceTransformEDT(hardMask)
	distOutside := distanceTransformEDT(invMask)

	height := newGrayF(w, h)
	for i := 0; i < w*h; i++ {
		bh := clampF(dist.Pix[i]/bw, 0, 1)
		bhOut := clampF(1.0-distOutside.Pix[i]/(bw*0.5), 0, 1) * (1 - hardMask.Pix[i])
		height.Pix[i] = bh + bhOut
	}

	gx := sobelX(height)
	gy := sobelY(height)

	bevelLight := newGrayF(w, h)
	for i := 0; i < w*h; i++ {
		bevelLight.Pix[i] = -(gx.Pix[i]*lightX + gy.Pix[i]*lightY)
	}

	maxVal := absMax(bevelLight)
	if maxVal < 1e-8 {
		maxVal = 1e-8
	}

	for i := 0; i < w*h; i++ {
		bevelLight.Pix[i] /= maxVal
		edgeProx := clampF(1.0-dist.Pix[i]/(bw*1.5), 0, 1) * hardMask.Pix[i]
		edgeProx += clampF(1.0-distOutside.Pix[i]/(bw*0.8), 0, 1) * (1 - hardMask.Pix[i])
		bevelLight.Pix[i] *= edgeProx
	}
	return bevelLight
}

func createInnerRelief(img *RGBF, mask *GrayF, lightX, lightY, strength float64) *GrayF {
	w, h := img.W, img.H

	edges := newGrayF(w, h)
	for c := 0; c < 3; c++ {
		ch := newGrayF(w, h)
		for i := 0; i < w*h; i++ {
			ch.Pix[i] = img.Pix[i*3+c]
		}
		gx := sobelX(ch)
		gy := sobelY(ch)
		for i := 0; i < w*h; i++ {
			edges.Pix[i] += math.Sqrt(gx.Pix[i]*gx.Pix[i] + gy.Pix[i]*gy.Pix[i])
		}
	}
	for i := range edges.Pix {
		edges.Pix[i] /= 3.0
	}

	edgesSmooth := gaussBlur(edges, 1.5)
	edgeMax := percentile(edgesSmooth, mask, 95)
	if edgeMax < 1e-8 {
		edgeMax = 1
	}

	for i := range edgesSmooth.Pix {
		edgesSmooth.Pix[i] = clampF(edgesSmooth.Pix[i]/edgeMax, 0, 1)
	}

	heightMap := gaussBlur(edgesSmooth, 2.0)
	for i := range heightMap.Pix {
		heightMap.Pix[i] *= mask.Pix[i]
	}

	gx := sobelX(heightMap)
	gy := sobelY(heightMap)

	relief := newGrayF(w, h)
	for i := 0; i < w*h; i++ {
		relief.Pix[i] = -(gx.Pix[i]*lightX + gy.Pix[i]*lightY) * strength * mask.Pix[i]
	}
	return relief
}

func applyMerrowEdge(canvas *RGBF, maskPadded *GrayF, litPatch *RGBF, patchMask *GrayF, thickness int, lightDir [3]float64) {
	w, h := maskPadded.W, maskPadded.H

	hardMask := newGrayF(w, h)
	for i := range maskPadded.Pix {
		if maskPadded.Pix[i] > 0.5 {
			hardMask.Pix[i] = 1
		}
	}

	dilated := binaryDilate(hardMask, thickness)
	eroded := binaryErode(hardMask, max(1, thickness/2))

	edgeBand := newGrayF(w, h)
	for i := range edgeBand.Pix {
		edgeBand.Pix[i] = clampF(dilated.Pix[i]-eroded.Pix[i], 0, 1)
	}
	edgeBand = gaussBlur(edgeBand, 0.6)

	cy, cx := float64(h)/2.0, float64(w)/2.0

	merrow := newGrayF(w, h)
	for y := 0; y < h; y++ {
		dy := float64(y) - cy
		for x := 0; x < w; x++ {
			dx := float64(x) - cx
			angle := math.Atan2(dy, dx)
			dist := math.Sqrt(dx*dx + dy*dy)
			stitchFreq := dist * 0.15
			sp := math.Sin(stitchFreq+angle*25)*0.12 + 0.88
			sp2 := math.Cos(stitchFreq*1.7+angle*18)*0.06 + 0.94
			merrow.Pix[y*w+x] = edgeBand.Pix[y*w+x] * sp * sp2
		}
	}

	// Sample edge color from patch border
	edgeColor := [3]float64{160, 160, 165}
	var edgePixels int
	var edgeSum [3]float64
	pw, ph := patchMask.W, patchMask.H
	for i := 0; i < pw*ph; i++ {
		if patchMask.Pix[i] > 0.3 && patchMask.Pix[i] < 0.95 {
			edgeSum[0] += litPatch.Pix[i*3]
			edgeSum[1] += litPatch.Pix[i*3+1]
			edgeSum[2] += litPatch.Pix[i*3+2]
			edgePixels++
		}
	}
	if edgePixels > 0 {
		for c := 0; c < 3; c++ {
			edgeColor[c] = clampF(edgeSum[c]/float64(edgePixels)*1.3+30, 0, 255)
		}
	}

	merrowGx := sobelX(merrow)
	merrowGy := sobelY(merrow)
	merrowLight := newGrayF(w, h)
	for i := range merrowLight.Pix {
		merrowLight.Pix[i] = -(merrowGx.Pix[i]*lightDir[0] + merrowGy.Pix[i]*lightDir[1])
	}
	mlMax := absMax(merrowLight)
	if mlMax < 1e-8 {
		mlMax = 1e-8
	}
	for i := range merrowLight.Pix {
		merrowLight.Pix[i] = merrowLight.Pix[i] / mlMax * 0.3
	}

	for i := 0; i < w*h; i++ {
		eb := edgeBand.Pix[i]
		m := merrow.Pix[i]
		for c := 0; c < 3; c++ {
			edgeVal := edgeColor[c] * (1.0 + merrowLight.Pix[i]) * m
			canvas.Pix[i*3+c] = canvas.Pix[i*3+c]*(1-eb*0.65) + edgeVal*0.65
		}
	}

	for i := range canvas.Pix {
		canvas.Pix[i] = clampF(canvas.Pix[i], 0, 255)
	}
}

func addDepthOfField(img *RGBF, mask *GrayF, maxBlur float64) *RGBF {
	w, h := img.W, img.H

	var sumX, sumY float64
	var count int
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if mask.Pix[y*w+x] > 0.5 {
				sumX += float64(x)
				sumY += float64(y)
				count++
			}
		}
	}
	if count == 0 {
		return img.clone()
	}
	cx := sumX / float64(count)
	cy := sumY / float64(count)

	blurred := gaussBlurRGB(img, maxBlur)

	result := newRGBF(w, h)
	fw, fh := float64(w), float64(h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			dx := (float64(x) - cx) / fw * 2
			dy := (float64(y) - cy) / fh * 2
			dist := math.Sqrt(dx*dx + dy*dy)
			t := clampF(math.Pow(math.Max((dist-0.35)/0.65, 0), 1.5), 0, 1)

			idx := (y*w + x) * 3
			for c := 0; c < 3; c++ {
				result.Pix[idx+c] = img.Pix[idx+c]*(1-t) + blurred.Pix[idx+c]*t
			}
		}
	}
	return result
}

func colorGrade(img *RGBF, warmth, contrast, saturation float64) {
	for i := 0; i < img.W*img.H; i++ {
		idx := i * 3
		img.Pix[idx] *= 1.0 + warmth
		img.Pix[idx+1] *= 1.0 + warmth*0.2
		img.Pix[idx+2] *= 1.0 - warmth*0.4

		for c := 0; c < 3; c++ {
			img.Pix[idx+c] = 128 + (img.Pix[idx+c]-128)*contrast
		}

		gray := (img.Pix[idx] + img.Pix[idx+1] + img.Pix[idx+2]) / 3.0
		for c := 0; c < 3; c++ {
			img.Pix[idx+c] = gray + (img.Pix[idx+c]-gray)*saturation
			img.Pix[idx+c] = clampF(img.Pix[idx+c], 0, 255)
		}
	}
}

func addVignette(img *RGBF, strength, radius float64) {
	w, h := img.W, img.H
	for y := 0; y < h; y++ {
		ny := float64(y)/float64(h-1)*2 - 1
		for x := 0; x < w; x++ {
			nx := float64(x)/float64(w-1)*2 - 1
			dist := math.Sqrt(nx*nx + ny*ny)
			v := 1.0 - strength*clampF(math.Pow(math.Max((dist-radius)/(1.4-radius), 0), 1.5), 0, 1)

			idx := (y*w + x) * 3
			img.Pix[idx] *= v
			img.Pix[idx+1] *= v
			img.Pix[idx+2] *= v
		}
	}
}

func addFilmGrain(img *RGBF, strength float64) {
	w, h := img.W, img.H
	lum := meanRGB(img)

	noise := normalNoise(w, h)
	noise = gaussBlur(noise, 0.4)

	for i := 0; i < w*h; i++ {
		grainStr := strength * (1.0 + 0.3*(1.0-lum.Pix[i]/255.0))
		g := noise.Pix[i] * grainStr
		img.Pix[i*3] = clampF(img.Pix[i*3]+g, 0, 255)
		img.Pix[i*3+1] = clampF(img.Pix[i*3+1]+g, 0, 255)
		img.Pix[i*3+2] = clampF(img.Pix[i*3+2]+g, 0, 255)
	}
}
