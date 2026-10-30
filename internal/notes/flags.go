package notes

import (
	"regexp"
	"sort"
)

// The reviewer is allowed to change his mind. These only FLAG; the agent applying the notes decides (and asks when
// unclear). The patterns are the legacy engine's, in its order: reinstatements are tried first.
var (
	reinstatePats = compileAll(
		`(?i)\b(?:add|put|bring) (?:that|it|this|those|them|all (?:of )?(?:that|it|them))(?: \w+){0,2}? back\b`,
		`(?i)\b(?:add|put|bring) back (?:that|it|this|those|them|what)\b`,
		`(?i)\b(?:add|put) (?:that|it|this|those|them) (?:back )?in again\b`,
		`(?i)\bactually,? (?:keep|do|use|add|leave) (?:that|it|this|those|them)\b`,
		`(?i)\bkeep (?:that|it|this|those) after all\b`,
		`(?i)\bnever\s?mind,? (?:keep|do|leave|use)\b`,
		`(?i)\b(?:reinstate|restore|un-?rescind)\b`,
	)
	retractPats = compileAll(
		`(?i)\bthrow (?:that|this|it|those|them|all|everything)\b[\w\s',]{0,40}?\bout\b`,
		`(?i)\bthrow out\b`,
		`(?i)\bscrap (?:that|this|it|those|all|everything)\b`,
		`(?i)\b(?:scratch|strike) that\b`,
		`(?i)\bnever\s?mind\b`,
		`(?i)\bignore (?:that|this|it|what|all|everything|my|those)\b`,
		`(?i)\b(?:rescind|retract|disregard)\b`,
		`(?i)\bforget (?:that|this|it|what|about|everything|all|those|my)\b`,
		`(?i)\bundo that\b`,
		`(?i)\b(?:delete|remove|drop|cancel) (?:that|those|my) (?:comment|note|feedback)s?\b`,
	)
	widePat = regexp.MustCompile(`(?i)\b(?:everything|all (?:of )?(?:my|the|what|that|it)|whole (?:video|thing|edit)|` +
		`entire|so far|from the (?:start|beginning)|previous comments)\b`)
	localPat = regexp.MustCompile(`(?i)\b(?:this|that|the) (?:clip|part|shot|bit|section|moment|scene|fight|cut|zoom|` +
		`frame)\b|\bhere\b`)
	// audioPat marks a line that is about sound: check the audio, not just the picture.
	audioPat = regexp.MustCompile(`(?i)\b(?:pause[sd]?|stutter\w*|gaps?|voice|vo|audio|music|sound|loud\w*|quiet\w*|` +
		`volume|mic|breath\w*|silence|silent|cut off|clipped|echo|sfx|whoosh)\b`)
)

func compileAll(pats ...string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, len(pats))
	for i, p := range pats {
		out[i] = regexp.MustCompile(p)
	}
	return out
}

// Flag kinds and scopes.
const (
	FlagRetract   = "retract"
	FlagReinstate = "reinstate"

	ScopeReinstate  = "what an earlier retraction threw out"
	ScopeClipWide   = "everything earlier on this clip"
	ScopeWider      = "possibly wider than this clip: ask Sam"
	ScopeClip       = "this clip"
	ScopeJustBefore = "the comment just before"
)

// Flag is a likely retraction or reinstatement found in a comment, and what it probably refers to (by spoken order).
type Flag struct {
	Kind     string `json:"kind"`
	Phrase   string `json:"phrase"`
	Scope    string `json:"scope"`
	RefersTo []int  `json:"refers_to"`
	Restores *[]int `json:"restores,omitempty"` // reinstatements only: the comments the retraction had voided
}

// DetectFlag finds a retraction ("scrap that") or reinstatement ("actually, add that back") in text, or returns nil.
// RefersTo and Restores are left empty: they depend on the other comments.
func DetectFlag(text string) *Flag {
	if text == "" {
		return nil
	}
	for _, kp := range []struct {
		kind string
		pats []*regexp.Regexp
	}{{FlagReinstate, reinstatePats}, {FlagRetract, retractPats}} {
		for _, p := range kp.pats {
			m := p.FindString(text) // no pattern matches the empty string
			if m == "" {
				continue
			}
			wide, local := widePat.MatchString(text), localPat.MatchString(text)
			var scope string
			switch {
			case kp.kind == FlagReinstate:
				scope = ScopeReinstate
			case wide && local:
				scope = ScopeClipWide
			case wide:
				scope = ScopeWider
			case local:
				scope = ScopeClip
			default:
				scope = ScopeJustBefore
			}
			return &Flag{Kind: kp.kind, Phrase: m, Scope: scope}
		}
	}
	return nil
}

// FlagEntry is one line of the flags list in notes.json.
type FlagEntry struct {
	Order    int    `json:"order"`
	Seg      string `json:"seg"`
	Moment   string `json:"moment"`
	Kind     string `json:"kind"`
	Phrase   string `json:"phrase"`
	Scope    string `json:"scope"`
	RefersTo []int  `json:"refers_to"`
	Restores *[]int `json:"restores,omitempty"`
	Text     string `json:"text"`
}

// attachFlags flags likely retractions and reinstatements and points at what they probably refer to. It never
// removes anything. It also sets each comment's Retracted state: voided by a retraction and not reinstated since.
func attachFlags(comments []*Comment) []FlagEntry {
	byOrder := append([]*Comment(nil), comments...)
	sort.SliceStable(byOrder, func(i, j int) bool { return byOrder[i].Order < byOrder[j].Order })
	flags := []FlagEntry{}
	for ci, c := range byOrder {
		f := DetectFlag(c.Text)
		c.Flag = f
		if f == nil {
			continue
		}
		earlier := byOrder[:ci]
		if f.Kind == FlagRetract {
			f.RefersTo = []int{}
			if f.Scope == ScopeJustBefore {
				if len(earlier) > 0 {
					f.RefersTo = []int{earlier[len(earlier)-1].Order}
				}
			} else {
				for _, x := range earlier {
					if x.Moment == c.Moment && (x.Flag == nil || x.Flag.Kind == "") {
						f.RefersTo = append(f.RefersTo, x.Order)
					}
				}
			}
		} else {
			var retracts, here []*Comment
			for _, x := range earlier {
				if x.Flag != nil && x.Flag.Kind == FlagRetract {
					retracts = append(retracts, x)
					if x.Moment == c.Moment {
						here = append(here, x)
					}
				}
			}
			pool := here
			if len(pool) == 0 {
				pool = retracts
			}
			f.RefersTo = []int{}
			restores := []int{}
			if len(pool) > 0 {
				target := pool[len(pool)-1]
				f.RefersTo = []int{target.Order}
				restores = append(restores, target.Flag.RefersTo...)
			}
			f.Restores = &restores
		}
		flags = append(flags, FlagEntry{Order: c.Order, Seg: c.Seg, Moment: c.Moment, Kind: f.Kind, Phrase: f.Phrase,
			Scope: f.Scope, RefersTo: f.RefersTo, Restores: f.Restores, Text: c.Text})
	}
	retracted := map[int]bool{}
	for _, f := range flags {
		if f.Kind == FlagRetract {
			for _, o := range f.RefersTo {
				retracted[o] = true
			}
		} else if f.Restores != nil {
			for _, o := range *f.Restores {
				retracted[o] = false
			}
		}
	}
	for _, c := range comments {
		c.Retracted = retracted[c.Order]
	}
	return flags
}
