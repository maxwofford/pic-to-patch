package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/beevik/etree"
)

const (
	inkstitchNS  = "http://inkstitch.org/namespace"
	svgNS        = "http://www.w3.org/2000/svg"
	inkscapeNS   = "http://www.inkscape.org/namespaces/inkscape"
	sodipodiNS   = "http://sodipodi.sourceforge.net/DTD/sodipodi-0.0.dtd"
	patchWidthMM = 80.0
)

var shapeTags = map[string]bool{
	"path": true, "circle": true, "ellipse": true,
	"rect": true, "polygon": true,
}

func addInkstitchParams(svgPath, outputPath, borderColor string) error {
	doc := etree.NewDocument()
	if err := doc.ReadFromFile(svgPath); err != nil {
		return err
	}

	root := doc.Root()
	if root == nil {
		return fmt.Errorf("no root element")
	}

	root.CreateAttr("xmlns:inkstitch", inkstitchNS)
	root.CreateAttr("xmlns:inkscape", inkscapeNS)
	root.CreateAttr("xmlns:sodipodi", sodipodiNS)

	vbW, vbH, originX, originY := getDimensions(root)
	if vbW == 0 {
		vbW = 100
	}
	if vbH == 0 {
		vbH = 100
	}

	scale := patchWidthMM / vbW
	root.CreateAttr("width", fmt.Sprintf("%gmm", patchWidthMM))
	root.CreateAttr("height", fmt.Sprintf("%gmm", vbH*scale))

	ensureNamedview(root)
	ensureInkstitchVersion(root)

	elementCount := 0
	for _, el := range collectElements(root) {
		tag := localName(el.Tag)
		if !shapeTags[tag] {
			continue
		}

		style := el.SelectAttrValue("style", "")
		fill := el.SelectAttrValue("fill", "")

		if strings.Contains(style, "fill:none") || fill == "none" {
			continue
		}
		if strings.Contains(style, "display:none") {
			continue
		}

		if fill != "" && !strings.Contains(style, "fill:") {
			if style != "" {
				el.CreateAttr("style", fmt.Sprintf("fill:%s;stroke:none;%s", fill, style))
			} else {
				el.CreateAttr("style", fmt.Sprintf("fill:%s;stroke:none", fill))
			}
			el.RemoveAttr("fill")
		} else if fill == "" && !strings.Contains(style, "fill:") {
			continue
		}

		currentStyle := el.SelectAttrValue("style", "")
		if !strings.Contains(currentStyle, "stroke:") {
			el.CreateAttr("style", strings.TrimRight(currentStyle, ";")+";stroke:none")
		}

		angle := (30 + elementCount*23) % 180
		setInkstitchAttr(el, "fill_method", "auto_fill")
		setInkstitchAttr(el, "fill_underlay", "true")
		setInkstitchAttr(el, "fill_underlay_angle", strconv.Itoa((angle+90)%360))
		setInkstitchAttr(el, "angle", strconv.Itoa(angle))
		setInkstitchAttr(el, "row_spacing_mm", "0.25")
		setInkstitchAttr(el, "max_stitch_length_mm", "3.0")
		setInkstitchAttr(el, "staggers", "4")

		elementCount++
	}

	if borderColor != "" {
		pad := vbW * 0.06

		border := etree.NewElement("rect")
		border.CreateAttr("x", fmt.Sprintf("%g", originX-pad))
		border.CreateAttr("y", fmt.Sprintf("%g", originY-pad))
		border.CreateAttr("width", fmt.Sprintf("%g", vbW+pad*2))
		border.CreateAttr("height", fmt.Sprintf("%g", vbH+pad*2))
		border.CreateAttr("rx", fmt.Sprintf("%g", pad*0.8))
		border.CreateAttr("ry", fmt.Sprintf("%g", pad*0.8))
		border.CreateAttr("style", fmt.Sprintf("fill:%s;stroke:none", borderColor))
		setInkstitchAttr(border, "fill_method", "auto_fill")
		setInkstitchAttr(border, "fill_underlay", "true")
		setInkstitchAttr(border, "angle", "90")
		setInkstitchAttr(border, "row_spacing_mm", "0.2")
		setInkstitchAttr(border, "max_stitch_length_mm", "2.5")
		setInkstitchAttr(border, "staggers", "4")
		root.InsertChildAt(0, border)

		newVBW := vbW + pad*2
		newVBH := vbH + pad*2
		root.CreateAttr("viewBox", fmt.Sprintf("%g %g %g %g",
			originX-pad, originY-pad, newVBW, newVBH))
		newScale := patchWidthMM / newVBW
		root.CreateAttr("width", fmt.Sprintf("%gmm", newVBW*newScale))
		root.CreateAttr("height", fmt.Sprintf("%gmm", newVBH*newScale))
		elementCount++
	}

	fmt.Printf("  %d elements parameterized\n", elementCount)

	doc.Indent(2)
	return doc.WriteToFile(outputPath)
}

func getDimensions(root *etree.Element) (w, h, ox, oy float64) {
	if vb := root.SelectAttrValue("viewBox", ""); vb != "" {
		parts := strings.Fields(vb)
		if len(parts) >= 4 {
			ox, _ = strconv.ParseFloat(parts[0], 64)
			oy, _ = strconv.ParseFloat(parts[1], 64)
			p2, _ := strconv.ParseFloat(parts[2], 64)
			p3, _ := strconv.ParseFloat(parts[3], 64)
			w = p2 - ox
			h = p3 - oy
			return
		}
	}

	wStr := strings.TrimSuffix(strings.TrimSuffix(
		root.SelectAttrValue("width", "100"), "px"), "mm")
	hStr := strings.TrimSuffix(strings.TrimSuffix(
		root.SelectAttrValue("height", "100"), "px"), "mm")
	w, _ = strconv.ParseFloat(wStr, 64)
	h, _ = strconv.ParseFloat(hStr, 64)
	return
}

func ensureNamedview(root *etree.Element) {
	for _, child := range root.ChildElements() {
		if localName(child.Tag) == "namedview" {
			return
		}
	}
	nv := root.CreateElement("sodipodi:namedview")
	nv.CreateAttr("inkscape:document-units", "mm")
}

func ensureInkstitchVersion(root *etree.Element) {
	var meta *etree.Element
	for _, child := range root.ChildElements() {
		if localName(child.Tag) == "metadata" {
			meta = child
			break
		}
	}
	if meta == nil {
		meta = root.CreateElement("metadata")
	}

	for _, child := range meta.ChildElements() {
		if localName(child.Tag) == "inkstitch_svg_version" {
			child.SetText("3")
			return
		}
	}
	ver := meta.CreateElement("inkstitch:inkstitch_svg_version")
	ver.SetText("3")
}

func setInkstitchAttr(el *etree.Element, name, value string) {
	el.CreateAttr("inkstitch:"+name, value)
}

func localName(tag string) string {
	if i := strings.LastIndex(tag, "}"); i >= 0 {
		return tag[i+1:]
	}
	if i := strings.LastIndex(tag, ":"); i >= 0 {
		return tag[i+1:]
	}
	return tag
}

func collectElements(root *etree.Element) []*etree.Element {
	var result []*etree.Element
	var walk func(*etree.Element)
	walk = func(el *etree.Element) {
		result = append(result, el)
		for _, child := range el.ChildElements() {
			walk(child)
		}
	}
	for _, child := range root.ChildElements() {
		walk(child)
	}
	return result
}
