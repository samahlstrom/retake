package project

import (
	"fmt"
	"slices"
	"strings"
)

// Status is a note's state (spec/notes.md, "Note states").
type Status string

// The note states.
const (
	StatusOpen      Status = "open"
	StatusQuestion  Status = "question"
	StatusAnswered  Status = "answered"
	StatusAccepted  Status = "accepted"
	StatusRetracted Status = "retracted"
)

// Statuses lists every state, in lifecycle order.
var Statuses = []Status{StatusOpen, StatusQuestion, StatusAnswered, StatusAccepted, StatusRetracted}

// ParseStatus checks a state name.
func ParseStatus(s string) (Status, error) {
	st := Status(strings.ToLower(strings.TrimSpace(s)))
	if slices.Contains(Statuses, st) {
		return st, nil
	}
	return "", fmt.Errorf("unknown status %q (one of open, question, answered, accepted, retracted)", s)
}

// Actor is who changes a note.
type Actor string

// The actors.
const (
	ByEngine   Actor = "engine"   // the notes engine, through a sync
	ByAgent    Actor = "agent"    // the agent, through note_ask and note_set
	ByPush     Actor = "push"     // a pushed version's changelog
	ByReviewer Actor = "reviewer" // the person reviewing: answers, reopen, approve
)

// transitions are the legal state changes and who may make them. Everything else is refused.
var transitions = []struct {
	from, to Status
	by       []Actor
}{
	{StatusOpen, StatusAnswered, []Actor{ByPush, ByAgent}}, // a pushed version lists it in its changelog (or note_set --in)
	{StatusOpen, StatusQuestion, []Actor{ByAgent}},         // the agent asks about it
	{StatusQuestion, StatusOpen, []Actor{ByReviewer}},      // the reviewer answers
	{StatusAnswered, StatusAccepted, []Actor{ByReviewer}},  // the reviewer approves that version
	{StatusAnswered, StatusOpen, []Actor{ByReviewer}},      // the reviewer reopens it
	{StatusOpen, StatusRetracted, []Actor{ByEngine}},       // the engine flags a retraction ("scrap that") covering it
	{StatusRetracted, StatusOpen, []Actor{ByEngine}},       // a reinstatement
}

// TransitionError is a refused note state change.
type TransitionError struct {
	Note     int64
	From, To Status
	By       Actor
}

func (e *TransitionError) Error() string {
	msg := fmt.Sprintf("note #%d is %s; it can't become %s", e.Note, e.From, e.To)
	if h := transitionHint(e.From, e.To, e.By); h != "" {
		msg += ": " + h
	}
	return msg
}

// transitionHint says what to do instead of a refused change.
func transitionHint(from, to Status, by Actor) string {
	if Legal(from, to, ByEngine) || Legal(from, to, ByAgent) || Legal(from, to, ByPush) ||
		Legal(from, to, ByReviewer) {
		switch {
		case to == StatusQuestion:
			return "ask about it with note_ask (retake ask)"
		case to == StatusAccepted:
			return "approving is the reviewer's action, in the review page"
		case from == StatusAnswered && to == StatusOpen:
			return "reopening is the reviewer's action, in the review page"
		case from == StatusQuestion && to == StatusOpen:
			return "it opens again when the reviewer answers the question"
		case to == StatusRetracted || from == StatusRetracted:
			return "retractions come from the reviewer's own words, through the notes engine"
		}
		return fmt.Sprintf("only the %s does that", legalActors(from, to))
	}
	switch {
	case from == StatusQuestion && to == StatusAnswered:
		return "it waits for the reviewer's answer; once answered it is open again and a push can answer it"
	case from == to:
		return "it already is"
	}
	return "that isn't a legal change (open→answered, open→question, question→open, answered→accepted, " +
		"answered→open, open→retracted, retracted→open)"
}

func legalActors(from, to Status) string {
	for _, tr := range transitions {
		if tr.from == from && tr.to == to {
			names := make([]string, len(tr.by))
			for i, a := range tr.by {
				names[i] = string(a)
			}
			return strings.Join(names, " or ")
		}
	}
	return "nobody"
}

// Legal reports whether by may move a note from one state to another.
func Legal(from, to Status, by Actor) bool {
	for _, tr := range transitions {
		if tr.from == from && tr.to == to {
			return slices.Contains(tr.by, by)
		}
	}
	return false
}

// checkTransition returns a *TransitionError unless the change is legal.
func checkTransition(id int64, from, to Status, by Actor) error {
	if Legal(from, to, by) {
		return nil
	}
	return &TransitionError{Note: id, From: from, To: to, By: by}
}
