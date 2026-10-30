package conformance

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/samahlstrom/retake/internal/accounts"
	"github.com/samahlstrom/retake/internal/blob"
	"github.com/samahlstrom/retake/internal/project"
	"github.com/samahlstrom/retake/internal/sqldb"
	"github.com/samahlstrom/retake/internal/team"
)

func (d driver) accounts(t *testing.T) *accounts.Store {
	t.Helper()
	var s *accounts.Store
	var err error
	if d.postgres {
		s, err = accounts.OpenPostgres(context.Background(), database(t))
	} else {
		s, err = accounts.OpenSQLite(filepath.Join(t.TempDir(), "server.db"))
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func (d driver) project(t *testing.T) *project.Store {
	t.Helper()
	root := t.TempDir()
	var s *project.Store
	var err error
	if d.postgres {
		s, err = project.OpenPostgres(context.Background(), database(t), "retake_p1", root)
	} else {
		s, err = project.Open(root)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func (d driver) blobs(t *testing.T) blob.Store {
	t.Helper()
	if d.postgres {
		s, _ := bucket(t)
		return s
	}
	return blob.NewLocal(t.TempDir())
}

func TestDialects(t *testing.T) {
	each(t, func(t *testing.T, d driver) {
		s := d.project(t)
		want := sqldb.SQLite
		if d.postgres {
			want = sqldb.Postgres
		}
		if s.Dialect() != want || s.Ping(context.Background()) != nil {
			t.Fatalf("dialect %v", s.Dialect())
		}
	})
}

func TestAccountsStore(t *testing.T) {
	each(t, func(t *testing.T, d driver) {
		s := d.accounts(t)
		now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
		var mu sync.Mutex
		s.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
		advance := func(dd time.Duration) { mu.Lock(); now = now.Add(dd); mu.Unlock() }

		ana, err := s.CreateUser("Ana@Example.com", "Ana", false)
		if err != nil || ana.Email != "ana@example.com" || ana.Admin {
			t.Fatal(ana, err)
		}
		if _, err := s.CreateUser("ana@example.com", "", false); !errors.Is(err, accounts.ErrExists) {
			t.Fatalf("duplicate: %v", err)
		}
		boss, _ := s.CreateUser("boss@example.com", "", true)
		if got, _ := s.User("ANA@example.com"); got.ID != ana.ID || got.Admin {
			t.Fatalf("lookup: %+v", got)
		}
		demo, err := s.CreateProject("demo", "Demo")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateProject("demo", ""); !errors.Is(err, accounts.ErrExists) {
			t.Fatalf("duplicate project: %v", err)
		}
		_, _ = s.CreateProject("zeta", "")
		if err := s.SetMember(demo.ID, ana.ID, team.Reviewer); err != nil {
			t.Fatal(err)
		}
		if err := s.SetMember(demo.ID, ana.ID, team.Owner); err != nil {
			t.Fatal(err)
		}
		if r, ok, _ := s.Role(demo.ID, ana); !ok || r != team.Owner {
			t.Fatal(r, ok)
		}
		if ps, _ := s.ProjectsOf(boss); len(ps) != 2 || ps[0].Role != team.Owner {
			t.Fatalf("admin's projects: %+v", ps)
		}
		if ps, _ := s.ProjectsOf(ana); len(ps) != 1 || ps[0].Name != "demo" {
			t.Fatalf("member's projects: %+v", ps)
		}
		if ms, _ := s.Members(demo.ID); len(ms) != 1 || ms[0].Role != team.Owner || ms[0].User.Name != "Ana" {
			t.Fatalf("members: %+v", ms)
		}
		if u, err := s.UpdateUser("ana@example.com", "Ana B", true); err != nil || !u.Admin {
			t.Fatal(u, err)
		}

		code, _, err := s.CreateLink(ana.ID, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.PeekLink(code); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UseLink(code); err != nil {
			t.Fatal(err)
		}
		if _, err := s.UseLink(code); !errors.Is(err, accounts.ErrBadLink) {
			t.Fatalf("reuse: %v", err)
		}

		v, sess, err := s.StartSession(ana.ID, "agent", "203.0.113.1")
		if err != nil {
			t.Fatal(err)
		}
		if _, u, rot, err := s.CheckSession(v); err != nil || u.ID != ana.ID || rot != "" {
			t.Fatal(u, rot, err)
		}
		advance(accounts.RotateEvery)
		_, _, rot, err := s.CheckSession(v)
		if err != nil || rot == "" {
			t.Fatalf("rotation: %q %v", rot, err)
		}
		if _, _, _, err := s.CheckSession(v); err != nil {
			t.Fatalf("grace: %v", err)
		}
		advance(accounts.RotateGrace)
		if _, _, _, err := s.CheckSession(v); !errors.Is(err, accounts.ErrNoSession) {
			t.Fatalf("after grace: %v", err)
		}
		if list, _ := s.Sessions(ana.ID); len(list) != 1 || list[0].ID != sess.ID {
			t.Fatalf("sessions: %+v", list)
		}
		if err := s.EndSession(sess.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := s.CheckSession(rot); !errors.Is(err, accounts.ErrNoSession) {
			t.Fatalf("ended: %v", err)
		}
		if _, err := s.Prune(); err != nil {
			t.Fatal(err)
		}
		_ = s.SetSetting(accounts.SettingPublicURL, "https://a")
		_ = s.SetSetting(accounts.SettingPublicURL, "https://b")
		if v, _ := s.Setting(accounts.SettingPublicURL); v != "https://b" {
			t.Fatal(v)
		}
		if err := s.RemoveMember(demo.ID, ana.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.RemoveUser("ana@example.com"); err != nil {
			t.Fatal(err)
		}
		if n, _ := s.CountUsers(); n != 1 {
			t.Fatalf("users left: %d", n)
		}
	})
}

func comment(take, seg string, n int, t0 float64, text string) project.Comment {
	return project.Comment{Take: take, Seg: seg, N: n, T0: t0, T1: t0 + 1, Text: text, Author: "Ana"}
}

func TestProjectStore(t *testing.T) {
	each(t, func(t *testing.T, d driver) {
		s := d.project(t)
		take := "2026-10-30_10-00-00"
		res, err := s.Sync(project.Run{Version: "v1", Comments: []project.Comment{
			comment(take, "S1", 1, 1, "tighten the intro"), comment(take, "S1", 2, 4.5, "brighter here"),
			comment(take, "S2", 1, 9, "scrap that"),
		}})
		if err != nil || len(res.Added) != 3 {
			t.Fatal(res, err)
		}
		// A second run keeps ids, updates text, and retracts what's gone.
		res, err = s.Sync(project.Run{Version: "v1", Comments: []project.Comment{
			comment(take, "S1", 1, 1, "tighten the intro a lot"), comment(take, "S1", 2, 4.5, "brighter here"),
		}})
		if err != nil || len(res.Updated) != 1 || len(res.Retracted) != 1 || res.Retracted[0] != 3 {
			t.Fatalf("resync: %+v %v", res, err)
		}
		th, err := s.Ask(project.Ask{Note: 2, Text: "how much brighter?", Options: []string{"v1"}})
		if err != nil || th.ID == 0 || th.Note == nil || *th.Note != 2 {
			t.Fatal(th, err)
		}
		if n, _ := s.Note(2); n.Status != project.StatusQuestion {
			t.Fatalf("asked note: %s", n.Status)
		}
		if un, _ := s.Unanswered(); len(un) != 1 {
			t.Fatalf("unanswered: %v", un)
		}
		// An answer reopens the note.
		ans := comment("2026-10-30_10-10-00", "S1", 1, 4.5, "about a stop")
		ans.AnswerTo = th.ID
		if res, err := s.Sync(project.Run{Version: "v1", Comments: []project.Comment{
			comment(take, "S1", 1, 1, "tighten the intro a lot"), comment(take, "S1", 2, 4.5, "brighter here"), ans,
		}}); err != nil || len(res.Answers) != 1 || len(res.Reopened) != 1 {
			t.Fatalf("answer: %+v %v", res, err)
		}
		// The push rule, then a push that handles both open notes.
		push := project.Push{Label: "v2", Path: "/x/demo_v2.mp4", SHA256: "abc", Parent: "v1", By: "agent",
			Changelog: []project.ChangeEntry{{Note: 1, Summary: "cut 2 s"}}, Scope: project.Scope{Version: "v1"}}
		var pr *project.PushRuleError
		if err := s.CheckPush(push); !errors.As(err, &pr) || len(pr.Notes) != 1 || pr.Notes[0] != 2 {
			t.Fatalf("push rule: %v", err)
		}
		push.Changelog = append(push.Changelog, project.ChangeEntry{Note: 2, Summary: "+1 stop"})
		pres, err := s.RecordPush(push)
		if err != nil || len(pres.Answered) != 2 || pres.Version.Label != "v2" {
			t.Fatalf("push: %+v %v", pres, err)
		}
		if vs, _ := s.Versions(); len(vs) != 1 || vs[0].Changelog[1].Summary != "+1 stop" {
			t.Fatalf("versions: %+v", vs)
		}
		if _, err := s.Reopen(1); err != nil {
			t.Fatal(err)
		}
		if ids, err := s.Approve("v2"); err != nil || len(ids) != 1 || ids[0] != 2 {
			t.Fatalf("approve: %v %v", ids, err)
		}
		var te *project.TransitionError
		if _, err := s.SetNote(2, project.NoteUpdate{Status: project.StatusOpen}); !errors.As(err, &te) {
			t.Fatalf("illegal: %v", err)
		}
		items, err := s.Sidebar(project.SidebarQuery{Version: "v1"})
		if err != nil || len(items) == 0 {
			t.Fatalf("sidebar: %v %v", items, err)
		}
		notes, err := s.Notes(project.NoteFilter{Statuses: []project.Status{project.StatusOpen},
			Scope: &project.Scope{Version: "v1"}})
		if err != nil || len(notes) != 1 || notes[0].ID != 1 {
			t.Fatalf("open notes: %+v %v", notes, err)
		}

		// Jobs: queue, claim, fail with a hint, retry, finish; the event log in order.
		jobs, err := s.QueueTake("v1", take, "/x/take")
		if err != nil || len(jobs) != 2 {
			t.Fatal(jobs, err)
		}
		if again, _ := s.QueueTake("v1", take, "/x/take"); again[0].ID != jobs[0].ID {
			t.Fatal("QueueTake isn't idempotent")
		}
		j, ok, err := s.Claim(project.JobTranscribe, nil)
		if err != nil || !ok || j.ID != jobs[0].ID || j.State != project.JobRunning {
			t.Fatal(j, ok, err)
		}
		if _, ok, _ := s.Claim(project.JobNotes, nil); ok {
			t.Fatal("notes ran before its transcription")
		}
		if j, err = s.Finish(j.ID, project.Fatal, "no whisper", "install it", 3); err != nil || j.State !=
			project.JobFailed || j.Hint != "install it" {
			t.Fatal(j, err)
		}
		if _, ok, _ := s.Claim(project.JobNotes, nil); ok {
			t.Fatal("notes ran after a failed transcription")
		}
		if n, _ := s.Job(jobs[1].ID); n.State != project.JobFailed || !strings.Contains(n.Error, "no whisper") {
			t.Fatalf("dependent job: %+v", n)
		}
		evs, err := s.Events(0, 0)
		if err != nil || len(evs) < 8 {
			t.Fatalf("events: %d %v", len(evs), err)
		}
		for i := 1; i < len(evs); i++ {
			if evs[i].Seq <= evs[i-1].Seq {
				t.Fatal("events out of order")
			}
		}
		last, _ := s.LastSeq()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go func() {
			time.Sleep(50 * time.Millisecond)
			_, _ = s.QueueNotes("v2")
		}()
		if got, err := s.WaitEvents(ctx, last, 0); err != nil || len(got) == 0 || got[0].Seq <= last {
			t.Fatalf("wait: %v %v", got, err)
		}
		if n, err := s.RequeueRunning(3); err != nil || len(n) != 0 {
			t.Fatal(n, err)
		}

		// Tokens and hosts.
		tok, hash, _ := team.NewToken()
		row, err := s.CreateToken(project.NewToken{Hash: hash, Role: "agent", Label: "bot", TTL: time.Hour})
		if err != nil || row.ID == 0 || row.Expires == "" {
			t.Fatal(row, err)
		}
		if got, err := s.TokenByHash(team.Hash(tok)); err != nil || got.ID != row.ID || !got.Valid(time.Now()) {
			t.Fatal(got, err)
		}
		if _, err := s.TokenByHash("nope"); !errors.Is(err, project.ErrNotFound) {
			t.Fatal(err)
		}
		if r, _ := s.RevokeToken(row.ID); r.Revoked == "" {
			t.Fatal("not revoked")
		}
		if err := s.AddHost("Share.Example", "none", 42); err != nil {
			t.Fatal(err)
		}
		if err := s.AddHost("share.example", "cloudflare", 43); err != nil {
			t.Fatal(err)
		}
		if ok, _ := s.HostAllowed("SHARE.example"); !ok {
			t.Fatal("host")
		}
		if hs, _ := s.Hosts(); len(hs) != 1 || hs[0].PID != 43 {
			t.Fatalf("hosts: %+v", hs)
		}
		_ = s.RemoveHost("share.example", 42) // another process's row stays
		if ok, _ := s.HostAllowed("share.example"); !ok {
			t.Fatal("removed another share's host")
		}
	})
}

func TestBlobStore(t *testing.T) {
	each(t, func(t *testing.T, d driver) {
		s := d.blobs(t)
		ctx := context.Background()
		data := []byte("0123456789abcdefghij")
		if err := s.Put(ctx, "projects/demo/media/a.mp4", bytes.NewReader(data), int64(len(data)), "video/mp4"); err != nil {
			t.Fatal(err)
		}
		f := filepath.Join(t.TempDir(), "b.bin")
		_ = os.WriteFile(f, []byte("bee"), 0o600)
		if err := s.PutFile(ctx, "projects/demo/media/x.review/t/seg_01.webm", f, "audio/webm"); err != nil {
			t.Fatal(err)
		}
		if err := s.Put(ctx, "projects/other/z", strings.NewReader("z"), 1, ""); err != nil {
			t.Fatal(err)
		}
		read := func(key string, off, n int64) string {
			rc, err := s.Get(ctx, key, off, n)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rc.Close() }()
			b, _ := io.ReadAll(rc)
			return string(b)
		}
		if got := read("projects/demo/media/a.mp4", 0, -1); got != string(data) {
			t.Fatal(got)
		}
		if got := read("projects/demo/media/a.mp4", 5, 3); got != "567" {
			t.Fatal(got)
		}
		if got := read("projects/demo/media/a.mp4", 15, -1); got != "fghij" {
			t.Fatal(got)
		}
		if info, err := s.Stat(ctx, "projects/demo/media/a.mp4"); err != nil || info.Size != 20 {
			t.Fatal(info, err)
		}
		if _, err := s.Stat(ctx, "projects/demo/nope"); !errors.Is(err, blob.ErrNotFound) {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "projects/demo/nope", 0, -1); !errors.Is(err, blob.ErrNotFound) {
			t.Fatal(err)
		}
		list, err := s.List(ctx, "projects/demo/")
		if err != nil || len(list) != 2 || list[0].Key != "projects/demo/media/a.mp4" ||
			list[1].Key != "projects/demo/media/x.review/t/seg_01.webm" || list[1].Size != 3 {
			t.Fatalf("list: %+v %v", list, err)
		}
		// Serving with ranges, as the hub proxies versions.
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/video", nil)
		r.Header.Set("Range", "bytes=-4")
		if err := blob.Serve(w, r, s, "projects/demo/media/a.mp4", "video/mp4"); err != nil || w.Code != 206 ||
			w.Body.String() != "ghij" || w.Header().Get("Content-Range") != "bytes 16-19/20" {
			t.Fatalf("serve: %v %d %q %v", err, w.Code, w.Body.String(), w.Header())
		}
		w = httptest.NewRecorder()
		r.Header.Set("Range", "bytes=30-")
		if err := blob.Serve(w, r, s, "projects/demo/media/a.mp4", "video/mp4"); err != nil || w.Code != 416 {
			t.Fatalf("416: %v %d", err, w.Code)
		}
		if err := blob.Serve(httptest.NewRecorder(), r, s, "projects/demo/x", "video/mp4"); !errors.Is(err,
			blob.ErrNotFound) {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, "projects/demo/media/a.mp4"); err != nil {
			t.Fatal(err)
		}
		if err := s.Delete(ctx, "projects/demo/media/a.mp4"); err != nil {
			t.Fatalf("deleting twice: %v", err)
		}
		if list, _ := s.List(ctx, "projects/demo/"); len(list) != 1 {
			t.Fatalf("after delete: %+v", list)
		}
	})
}
