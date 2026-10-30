package assets

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/samahlstrom/retake/internal/transcribe"
)

// transcribe transcribes an asset's audio (the track-th audio stream) and stores the words.
func (c *Catalog) transcribe(ctx context.Context, d *db, id, file string, track int, m Media) error {
	if c.o.Transcriber == nil {
		return fmt.Errorf("transcription isn't configured (see [transcribe] in spec/notes.md)")
	}
	t, err := c.o.Transcriber()
	if err != nil {
		return err
	}
	if track < 0 || track >= len(m.Audio) {
		return fmt.Errorf("no audio track %d (the file has %d)", track, len(m.Audio))
	}
	audio := file
	if len(m.Audio) > 1 || m.hasVideo() {
		dir, done, err := d.scratch("tx-*")
		if err != nil {
			return err
		}
		defer done()
		audio = filepath.Join(dir, "track.wav")
		if _, err := c.tools.ffmpegRun(ctx, "-i", file, "-map", "0:a:"+strconv.Itoa(track), "-ac", "1", "-ar",
			strconv.Itoa(transcribe.SampleRate), "-c:a", "pcm_s16le", audio); err != nil {
			return err
		}
	}
	words, err := t.Transcribe(ctx, audio, transcribe.Options{})
	if err != nil {
		return err
	}
	return d.putTranscript(ctx, id, t.Name(), track, words)
}

// putTranscript stores an asset's words and reindexes it.
func (d *db) putTranscript(ctx context.Context, id, model string, track int, words []transcribe.Word) error {
	rows := make([][3]any, len(words))
	var text strings.Builder
	for i, w := range words {
		rows[i] = [3]any{round3(w.Start), round3(w.End), w.Text}
		text.WriteString(w.Text)
	}
	return d.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO transcripts (asset, model, track, words, text, created)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (asset) DO UPDATE SET model = excluded.model, track = excluded.track,
			words = excluded.words, text = excluded.text, created = excluded.created`,
			id, model, track, mustJSON(rows), strings.TrimSpace(text.String()), d.stamp())
		if err != nil {
			return err
		}
		return indexAsset(ctx, tx, id)
	})
}

// TranscriptHit is where query words were said in an asset's transcript.
type TranscriptHit struct {
	T0   float64 `json:"t0"`
	T1   float64 `json:"t1"`
	Text string  `json:"text"` // the words around the hit
}

// transcriptHits finds the query's words in an asset's stored transcript: up to limit runs of words, with their
// stored times.
func (d *db) transcriptHits(ctx context.Context, id string, terms []string, limit int) ([]TranscriptHit, error) {
	var words string
	err := d.rw.QueryRowContext(ctx, `SELECT words FROM transcripts WHERE asset = ?`, id).Scan(&words)
	if err != nil {
		return nil, nil //nolint:nilerr // no transcript: no hits
	}
	var ws [][3]any
	if err := jsonUnmarshal(words, &ws); err != nil {
		return nil, err
	}
	var out []TranscriptHit
	for i := 0; i < len(ws) && len(out) < limit; i++ {
		w := normWord(fmt.Sprint(ws[i][2]))
		match := false
		for _, t := range terms {
			if strings.HasPrefix(w, t) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		lo, hi := max(0, i-4), min(len(ws), i+5)
		var b strings.Builder
		for _, x := range ws[lo:hi] {
			fmt.Fprint(&b, x[2])
		}
		t0, _ := ws[i][0].(float64)
		t1, _ := ws[i][1].(float64)
		out = append(out, TranscriptHit{T0: t0, T1: t1, Text: strings.TrimSpace(b.String())})
		i += 4 // don't report the same phrase twice
	}
	return out, nil
}

// normWord lowercases a word and drops its punctuation.
func normWord(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		case r == ' ' || strings.ContainsRune(".,;:!?\"'()[]{}…-–—", r):
			return -1
		}
		return r
	}, s)
}
