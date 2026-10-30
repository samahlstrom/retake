package notes

import (
	"regexp"
	"strings"
)

const (
	gap      = 1.5 // s of silence that ends a speech unit even without punctuation
	attachTo = 3.0 // s: a stroke drawn while playing joins the nearest sentence within this gap
	unclear  = 0.5 // mean word probability below this -> "(unclear)"
)

// sentEnd matches a word that ends a sentence.
var sentEnd = regexp.MustCompile(`[.!?…]["')\]]*$`)

// word is one transcribed word on the take clock (seconds since the take started).
type word struct {
	w    string
	s, e float64
	p    float64
	seg  string
}

// unit is one whole sentence.
type unit struct {
	seg     string
	s, e    float64
	words   []word
	stretch int
	path    *Path
	text    string
	conf    float64
}

// buildUnits turns words into whole sentences. A sentence ends at punctuation, a >1.5 s gap or a segment boundary,
// never at play/pause/seek: it is filed under the stretch where it STARTS, and path says where the playhead went
// while he was still talking.
func buildUnits(words []word, tl *timeline) []*unit {
	var units []*unit
	var cur *unit
	for _, w := range words {
		if cur != nil && (w.seg != cur.seg || w.s-cur.e > gap ||
			sentEnd.MatchString(strings.TrimSpace(cur.words[len(cur.words)-1].w))) {
			units = append(units, cur)
			cur = nil
		}
		if cur == nil {
			cur = &unit{seg: w.seg, s: w.s, e: w.e}
		}
		cur.words = append(cur.words, w)
		cur.e = w.e
	}
	if cur != nil {
		units = append(units, cur)
	}
	for _, u := range units {
		w0 := u.words[0]
		u.stretch = tl.stretchOf((w0.s + w0.e) / 2)
		u.path = tl.path(max(u.s, tl.bounds[u.stretch]), u.e)
		var b strings.Builder
		ps := make([]float64, 0, len(u.words))
		for _, w := range u.words {
			b.WriteString(w.w)
			ps = append(ps, w.p)
		}
		u.text = collapseSpace(b.String())
		u.conf = r3(pySum(ps) / float64(len(ps)))
	}
	return units
}
