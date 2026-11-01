package notes

import (
	"fmt"
	"math"
)

// minStroke: strokes smaller than this fraction of the frame diagonal are noise (2-3 px ticks).
const minStroke = 0.01

// Geometry is what the shape classifier finds out about one stroke. Coordinates are normalized 0-1 to the frame.
type Geometry struct {
	BBox  [4]float64 // x0, y0, x1, y1
	From  [2]float64
	To    [2]float64 // the tip, for an arrow
	Shape string     // "line", "arrow", "circle" or "curve" ("x" is decided across two strokes, by markX)
}

// pt formats a point like the legacy "(0.40,0.40)".
func pt(x, y float64) string { return fmt.Sprintf("(%.2f,%.2f)", x, y) }

var directions = [8]string{"right", "down-right", "down", "down-left", "left", "up-left", "up", "up-right"}

// direction names the compass direction of a move by (dx, dy) on a w x h frame (screen y points down).
func direction(dx, dy, w, h float64) string {
	ang := radToDeg * math.Atan2(dy*h, dx*w)
	return directions[int(pyFloorDiv(pyMod(ang+22.5, 360), 45))%8]
}

// Describe classifies a stroke's points (normalized, on a w x h frame) and describes it in words, without the color.
func Describe(points [][2]float64, w, h float64) (string, Geometry) {
	pts := points
	if len(pts) == 0 {
		pts = [][2]float64{{0, 0}}
	}
	minX, minY, maxX, maxY := pts[0][0], pts[0][1], pts[0][0], pts[0][1]
	for _, p := range pts[1:] {
		minX, minY = math.Min(minX, p[0]), math.Min(minY, p[1])
		maxX, maxY = math.Max(maxX, p[0]), math.Max(maxY, p[1])
	}
	bb := [4]float64{r3(minX), r3(minY), r3(maxX), r3(maxY)}
	a, b := pts[0], pts[len(pts)-1]
	px := make([][2]float64, len(pts))
	for i, p := range pts {
		px[i] = [2]float64{p[0] * w, p[1] * h}
	}
	steps := make([]float64, 0, len(px))
	for i := 0; i+1 < len(px); i++ {
		steps = append(steps, dist(px[i], px[i+1]))
	}
	path := pySum(steps)
	chord := dist(px[0], px[len(px)-1])
	span := max((bb[2]-bb[0])*w, (bb[3]-bb[1])*h, 1e-6)
	g := Geometry{BBox: bb, From: [2]float64{r3(a[0]), r3(a[1])}, To: [2]float64{r3(b[0]), r3(b[1])}}
	if len(pts) > 6 && chord < 0.3*span && path > 1.5*span {
		g.Shape = "circle"
		return fmt.Sprintf("circle around %s–%s", pt(bb[0], bb[1]), pt(bb[2], bb[3])), g
	}
	if path <= 1.2*max(chord, 1e-6) {
		g.Shape = "line"
		return fmt.Sprintf("line %s→%s heading %s", pt(a[0], a[1]), pt(b[0], b[1]),
			direction(b[0]-a[0], b[1]-a[1], w, h)), g
	}
	// one-stroke arrow: a straight shaft out to the farthest point, then a short head drawn around the tip
	k, far := 0, math.Inf(-1)
	for i := range px {
		if d := dist(px[0], px[i]); d > far {
			k, far = i, d
		}
	}
	shaft := dist(px[0], px[k])
	shaftPath := pySum(steps[:k])
	head := path - shaftPath
	if 0 < k && k < len(px)-1 && shaft > 0 && shaftPath <= 1.25*shaft && 0.08*shaft <= head && head <= 0.9*shaft &&
		headNearTip(px, k, 0.45*shaft) {
		tip := pts[k]
		g.Shape, g.To = "arrow", [2]float64{r3(tip[0]), r3(tip[1])}
		return fmt.Sprintf("arrow %s→%s pointing %s", pt(a[0], a[1]), pt(tip[0], tip[1]),
			direction(tip[0]-a[0], tip[1]-a[1], w, h)), g
	}
	g.Shape = "curve"
	return fmt.Sprintf("curve %s→%s within %s–%s", pt(a[0], a[1]), pt(b[0], b[1]), pt(bb[0], bb[1]),
		pt(bb[2], bb[3])), g
}

func headNearTip(px [][2]float64, k int, limit float64) bool {
	for _, q := range px[k:] {
		if dist(px[k], q) > limit {
			return false
		}
	}
	return true
}

// cross returns where segments p-q and r-t (normalized coords) intersect, if it's near both middles.
func cross(p, q, r, t [2]float64) ([2]float64, bool) {
	d := (q[0]-p[0])*(t[1]-r[1]) - (q[1]-p[1])*(t[0]-r[0])
	if math.Abs(d) < 1e-9 {
		return [2]float64{}, false
	}
	a := ((r[0]-p[0])*(t[1]-r[1]) - (r[1]-p[1])*(t[0]-r[0])) / d
	b := ((r[0]-p[0])*(q[1]-p[1]) - (r[1]-p[1])*(q[0]-p[0])) / d
	if 0.15 <= a && a <= 0.85 && 0.15 <= b && b <= 0.85 {
		return [2]float64{p[0] + a*(q[0]-p[0]), p[1] + a*(q[1]-p[1])}, true
	}
	return [2]float64{}, false
}

// markX turns two consecutive straight strokes that cross near their middles at a wide angle into an X.
func markX(strokes []*Stroke, w, h float64) {
	for i := 0; i+1 < len(strokes); i++ {
		a, b := strokes[i], strokes[i+1]
		if a.Shape != "line" || b.Shape != "line" {
			continue
		}
		c, ok := cross(a.from(), a.to(), b.from(), b.to())
		if !ok {
			continue
		}
		va := [2]float64{(a.to()[0] - a.from()[0]) * w, (a.to()[1] - a.from()[1]) * h}
		vb := [2]float64{(b.to()[0] - b.from()[0]) * w, (b.to()[1] - b.from()[1]) * h}
		if math.Abs(va[0]*vb[0]+va[1]*vb[1])/(hypot(va[0], va[1])*hypot(vb[0], vb[1])+1e-9) > 0.7 {
			continue // nearly parallel: not an X
		}
		for _, st := range []*Stroke{a, b} {
			st.Shape = "x"
			st.At = &[2]jsonFloat{jsonFloat(r3(c[0])), jsonFloat(r3(c[1]))}
			st.Desc = fmt.Sprintf("%s X at %s", st.Color, pt(c[0], c[1]))
		}
	}
}
