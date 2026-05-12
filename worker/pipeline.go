package main

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

const maxImageDim = 500

func runPipeline(input []byte, ext string, borderColor string, colorPrecision int, postprocess bool) ([]byte, error) {
	tmpDir, err := os.MkdirTemp("", "p2p_")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	inputPath := filepath.Join(tmpDir, "input."+ext)
	if err := os.WriteFile(inputPath, input, 0644); err != nil {
		return nil, err
	}

	isSVG := strings.EqualFold(ext, "svg")

	var embroideryPath string
	if isSVG {
		embroideryPath = filepath.Join(tmpDir, "embroidery.svg")
		if err := addInkstitchParams(inputPath, embroideryPath, borderColor); err != nil {
			return nil, fmt.Errorf("inkstitch params: %w", err)
		}
	} else {
		resizedPath := filepath.Join(tmpDir, "resized.png")
		if err := resizeImage(inputPath, resizedPath); err != nil {
			return nil, fmt.Errorf("resize: %w", err)
		}

		vectorizedPath := filepath.Join(tmpDir, "vectorized.svg")
		if err := vectorize(resizedPath, vectorizedPath, colorPrecision); err != nil {
			return nil, fmt.Errorf("vectorize: %w", err)
		}

		embroideryPath = filepath.Join(tmpDir, "embroidery.svg")
		if err := addInkstitchParams(vectorizedPath, embroideryPath, borderColor); err != nil {
			return nil, fmt.Errorf("inkstitch params: %w", err)
		}
	}

	renderPath := filepath.Join(tmpDir, "render.png")
	if err := renderInkstitch(embroideryPath, renderPath); err != nil {
		return nil, err
	}

	if !postprocess {
		return os.ReadFile(renderPath)
	}

	outputPath := filepath.Join(tmpDir, "patch.png")
	if err := postprocessPhotorealistic(renderPath, outputPath); err != nil {
		return nil, fmt.Errorf("postprocess: %w", err)
	}

	return os.ReadFile(outputPath)
}

func resizeImage(inputPath, outputPath string) error {
	f, err := os.Open(inputPath)
	if err != nil {
		return err
	}
	defer f.Close()

	src, _, err := image.Decode(f)
	if err != nil {
		return err
	}

	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()

	maxDim := w
	if h > maxDim {
		maxDim = h
	}

	if maxDim <= maxImageDim {
		out, err := os.Create(outputPath)
		if err != nil {
			return err
		}
		defer out.Close()
		return png.Encode(out, src)
	}

	ratio := float64(maxImageDim) / float64(maxDim)
	newW := int(float64(w) * ratio)
	newH := int(float64(h) * ratio)

	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	out, err := os.Create(outputPath)
	if err != nil {
		return err
	}
	defer out.Close()
	return png.Encode(out, dst)
}

func vectorize(inputPath, outputPath string, colorPrecision int) error {
	cmd := exec.Command("vtracer",
		"--input", inputPath,
		"--output", outputPath,
		"--colormode", "color",
		"--hierarchical", "stacked",
		"--filter_speckle", "4",
		"--color_precision", strconv.Itoa(colorPrecision),
		"--corner_threshold", "60",
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, output)
	}

	if _, err := os.Stat(outputPath); err != nil {
		return fmt.Errorf("vtracer produced no output")
	}
	return nil
}

func renderInkstitch(svgPath, outputPath string) error {
	inkstitchBin := getenv("INKSTITCH_BIN", "/opt/inkstitch/inkstitch/bin/inkstitch")

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	cmd := exec.Command(inkstitchBin, "--extension=png_realistic", svgPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		errStr := stderr.String()
		if len(errStr) > 300 {
			errStr = errStr[:300]
		}
		return fmt.Errorf("inkstitch render failed: %s", errStr)
	}

	if stdout.Len() == 0 {
		return fmt.Errorf("inkstitch produced empty output")
	}

	return os.WriteFile(outputPath, stdout.Bytes(), 0644)
}
