package hub

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/team"
	"github.com/samahlstrom/retake/internal/testmedia"
)

// postProject submits the streaming form the local Home uses. It deliberately uses the folder test server's fake
// prober, so the contract is exercised without a bundled ffprobe or ffmpeg process.
func (x *ft) postProject(t *testing.T, root, name, videoName string, video []byte, opts ...reqOpt) res {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	if err := w.WriteField("root", root); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		if err := w.WriteField("name", name); err != nil {
			t.Fatal(err)
		}
	}
	if videoName != "" {
		part, err := w.CreateFormFile("video", videoName)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(video); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return x.postProjectBody(t, w.FormDataContentType(), &body, opts...)
}

func (x *ft) postProjectBody(t *testing.T, contentType string, body io.Reader, opts ...reqOpt) res {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, x.url+"/api/projects", body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", contentType)
	for _, opt := range opts {
		opt(req)
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Keep this separate from ft.do: a multipart request must not turn a generated video into a string.
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return res{code: resp.StatusCode, hdr: resp.Header, body: string(b)}
}

func homeHasProject(h home.Response, name string) bool {
	for _, card := range h.Projects {
		if card.Name == name {
			return true
		}
	}
	return false
}

func rootNames(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return strings.Join(names, ",")
}

func linkedRootID(t *testing.T, x *ft, path string) string {
	t.Helper()
	return libraryString(t, folderAt(t, libraryFolders(t, libraryView(t, x.get("/api/library"))), path), "id")
}

func linkProjectRoot(t *testing.T, x *ft, path string) string {
	t.Helper()
	initial := libraryView(t, x.get("/api/library"))
	folders := append(libraryFolders(t, initial), map[string]any{"path": path, "subfolders": true})
	if r := replaceLibrary(t, x, libraryRevision(t, initial), folders); r.code != http.StatusOK {
		t.Fatalf("link project root: %d %s", r.code, r.body)
	}
	return linkedRootID(t, x, path)
}

func projectVideo(t *testing.T, x *ft, name string) string {
	t.Helper()
	return filepath.Join(x.dir, name, "v1", name+"_v1.mp4")
}

func probeProjectVideo(t *testing.T, path string) []struct {
	CodecType string `json:"codec_type"`
	CodecName string `json:"codec_name"`
	PixFmt    string `json:"pix_fmt"`
} {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-of", "json", "-show_streams", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			CodecName string `json:"codec_name"`
			PixFmt    string `json:"pix_fmt"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	return result.Streams
}

func TestLocalOwnerCreatesProjectFromUploadedVideo(t *testing.T) {
	x := newFT(t, nil)
	root := linkedRootID(t, x, x.dir)
	original := filepath.Join(t.TempDir(), "from-a-completely-different-cut.mp4")
	testmedia.Video(t, original, 1)
	before, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}

	r := x.postProject(t, root, "Fresh Project", filepath.Base(original), before, origin(x.url))
	if r.code != http.StatusCreated {
		t.Fatalf("POST /api/projects: %d %s, want 201", r.code, r.body)
	}
	var made struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal([]byte(r.body), &made); err != nil {
		t.Fatal(err)
	}
	if made.Name != "fresh-project" || made.URL != "p/fresh-project/" {
		t.Fatalf("created project: %+v", made)
	}
	found := false
	for _, card := range x.home(t).Projects {
		if card.Name == "fresh-project" {
			found = card.URL == "p/fresh-project/"
		}
	}
	if !found {
		t.Fatal("new project card or URL is missing from Home")
	}
	if _, err := os.Stat(projectVideo(t, x, "fresh-project")); err != nil {
		t.Fatalf("generated project video: %v", err)
	}
	after, err := os.ReadFile(original)
	if err != nil || !bytes.Equal(after, before) {
		t.Fatalf("source video changed: %v", err)
	}
}

func TestLocalOwnerCreatesBlankProjectAtSelectedRoot(t *testing.T) {
	x := newFT(t, nil)
	r := x.postProject(t, linkedRootID(t, x, x.dir), "Blank starter", "", nil, origin(x.url))
	if r.code != http.StatusCreated {
		t.Fatalf("blank POST /api/projects: %d %s, want 201", r.code, r.body)
	}
	timeline := filepath.Join(x.dir, "blank-starter", "edit", "timeline.json")
	b, err := os.ReadFile(timeline)
	if err != nil || len(bytes.TrimSpace(b)) == 0 || !json.Valid(b) {
		t.Fatalf("native timeline %q: %q %v", timeline, b, err)
	}
	if _, err := os.Stat(filepath.Join(x.dir, "blank-starter", "v1")); !os.IsNotExist(err) {
		t.Fatalf("blank project has a video version directory: %v", err)
	}
	if !homeHasProject(x.home(t), "blank-starter") {
		t.Fatal("blank project is not discoverable on Home")
	}
}

func TestLocalOwnerCreatesInExplicitLinkedRoot(t *testing.T) {
	x := newFT(t, nil)
	external := t.TempDir()
	root := linkProjectRoot(t, x, external)
	before := rootNames(t, x.dir)
	r := x.postProject(t, root, "External project", "", nil, origin(x.url))
	if r.code != http.StatusCreated {
		t.Fatalf("external-root POST /api/projects: %d %s, want 201", r.code, r.body)
	}
	if _, err := os.Stat(filepath.Join(external, "external-project", "edit", "timeline.json")); err != nil {
		t.Fatalf("external project: %v", err)
	}
	if !homeHasProject(x.home(t), "external-project") {
		t.Fatal("external project is not discoverable on Home")
	}
	if after := rootNames(t, x.dir); after != before {
		t.Fatalf("original root changed: %q, want %q", after, before)
	}
}

func TestProjectCreationRejectsUnavailableRoots(t *testing.T) {
	for _, tc := range []struct {
		name string
		root func(*testing.T, *ft) (string, string)
		want int
	}{
		{name: "unknown", root: func(_ *testing.T, _ *ft) (string, string) { return "missing-root", "" }, want: http.StatusBadRequest},
		{name: "removed", root: func(t *testing.T, x *ft) (string, string) {
			external := t.TempDir()
			id := linkProjectRoot(t, x, external)
			view := libraryView(t, x.get("/api/library"))
			if r := replaceLibrary(t, x, libraryRevision(t, view), libraryFolders(t, view)[:1]); r.code != http.StatusOK {
				t.Fatalf("remove root: %d %s", r.code, r.body)
			}
			return id, external
		}, want: http.StatusConflict},
		{name: "offline", root: func(t *testing.T, x *ft) (string, string) {
			external := t.TempDir()
			id := linkProjectRoot(t, x, external)
			offline := external + "-offline"
			if err := os.Rename(external, offline); err != nil {
				t.Fatal(err)
			}
			return id, offline
		}, want: http.StatusConflict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := newFT(t, nil)
			root, external := tc.root(t, x)
			originalBefore := rootNames(t, x.dir)
			before := originalBefore
			if external != "" {
				before = rootNames(t, external)
			}
			r := x.postProject(t, root, "Unavailable root", "", nil, origin(x.url))
			if r.code != tc.want {
				t.Fatalf("POST /api/projects: %d %s, want %d", r.code, r.body, tc.want)
			}
			if external != "" && rootNames(t, external) != before {
				t.Fatalf("unavailable root changed: %q", external)
			}
			if after := rootNames(t, x.dir); after != originalBefore {
				t.Fatalf("original root changed: %q, want %q", after, originalBefore)
			}
			if homeHasProject(x.home(t), "unavailable-root") {
				t.Fatal("unavailable root created a Home card")
			}
		})
	}
}

func TestProjectUploadConvertsAndCopiesGeneratedMedia(t *testing.T) {
	t.Run("converts non-baseline video", func(t *testing.T) {
		x := newFT(t, nil)
		source := filepath.Join(t.TempDir(), "source.webm")
		testmedia.Video(t, source, 1)
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		r := x.postProject(t, linkedRootID(t, x, x.dir), "Converted", filepath.Base(source), body, origin(x.url))
		if r.code != http.StatusCreated {
			t.Fatalf("converted upload: %d %s, want 201", r.code, r.body)
		}
		streams := probeProjectVideo(t, projectVideo(t, x, "converted"))
		if len(streams) != 2 || streams[0].CodecType != "video" || streams[0].CodecName != "h264" || streams[0].PixFmt != "yuv420p" || streams[1].CodecType != "audio" || streams[1].CodecName != "aac" {
			t.Fatalf("converted streams: %#v", streams)
		}
	})

	t.Run("copies browser-safe input", func(t *testing.T) {
		x := newFT(t, nil)
		source := filepath.Join(t.TempDir(), "copy-source.mp4")
		testmedia.Video(t, source, 1)
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		r := x.postProject(t, linkedRootID(t, x, x.dir), "Copied", filepath.Base(source), body, origin(x.url))
		if r.code != http.StatusCreated {
			t.Fatalf("copy upload: %d %s, want 201", r.code, r.body)
		}
		copied, err := os.ReadFile(projectVideo(t, x, "copied"))
		if err != nil || !bytes.Equal(copied, body) {
			t.Fatalf("browser-safe input was not copied: %v", err)
		}
	})

	t.Run("keeps no-audio video without an audio stream", func(t *testing.T) {
		x := newFT(t, nil)
		withAudio := filepath.Join(t.TempDir(), "with-audio.mp4")
		source := filepath.Join(t.TempDir(), "no-audio.mp4")
		testmedia.Video(t, withAudio, 1)
		if out, err := exec.Command("ffmpeg", "-nostdin", "-v", "error", "-y", "-i", withAudio, "-c:v", "copy", "-an", source).CombinedOutput(); err != nil {
			t.Fatalf("make no-audio fixture: %v %s", err, out)
		}
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		r := x.postProject(t, linkedRootID(t, x, x.dir), "No audio", filepath.Base(source), body, origin(x.url))
		if r.code != http.StatusCreated {
			t.Fatalf("no-audio upload: %d %s, want 201", r.code, r.body)
		}
		streams := probeProjectVideo(t, projectVideo(t, x, "no-audio"))
		if len(streams) != 1 || streams[0].CodecType != "video" || streams[0].CodecName != "h264" || streams[0].PixFmt != "yuv420p" {
			t.Fatalf("no-audio output streams: %#v", streams)
		}
	})
}

func TestSharedTokenCannotCreateLocalProject(t *testing.T) {
	x := newFT(t, nil)
	token := homeToken(t, x.dir, team.Reviewer, "review")
	r := x.postProject(t, linkedRootID(t, x, x.dir), "Shared attempt", "clip.mp4", []byte("not used"), shared(), bearerTok(token), origin("https://share.test"))
	if r.code != http.StatusForbidden {
		t.Fatalf("shared token POST /api/projects: %d %s, want 403", r.code, r.body)
	}
	if homeHasProject(x.home(t), "shared-attempt") {
		t.Fatal("shared request created a Home card")
	}
}

func TestProjectCreationRejectsConflictsAndBadUploadsWithoutResidue(t *testing.T) {
	x := newFT(t, nil)
	root := linkedRootID(t, x, x.dir)
	if err := os.Mkdir(filepath.Join(x.dir, "taken-name"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, display, filename string
		body                    []byte
		want                    int
	}{
		{name: "name conflict", display: "Taken name", filename: "clip.mp4", body: []byte("video"), want: http.StatusConflict},
		{name: "missing name", filename: "clip.mp4", body: []byte("video"), want: http.StatusBadRequest},
		{name: "non-video", display: "Not video", filename: "notes.txt", body: []byte("plain text"), want: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := rootNames(t, x.dir)
			r := x.postProject(t, root, tc.display, tc.filename, tc.body, origin(x.url))
			if r.code != tc.want {
				t.Fatalf("POST /api/projects: %d %s, want %d", r.code, r.body, tc.want)
			}
			slug := strings.ReplaceAll(strings.ToLower(tc.display), " ", "-")
			if slug != "" && slug != "taken-name" {
				if _, err := os.Stat(filepath.Join(x.dir, slug)); !os.IsNotExist(err) {
					t.Fatalf("failed upload left project folder %q: %v", slug, err)
				}
			}
			if slug != "" && homeHasProject(x.home(t), slug) {
				t.Fatalf("failed upload created %q card", slug)
			}
			if after := rootNames(t, x.dir); after != before {
				t.Fatalf("failed upload left root entries %q, want %q", after, before)
			}
		})
	}

	before := rootNames(t, x.dir)
	bad := x.postProjectBody(t, "multipart/form-data; boundary=missing", strings.NewReader("not a multipart body"), origin(x.url))
	if bad.code != http.StatusBadRequest {
		t.Fatalf("invalid multipart: %d %s, want 400", bad.code, bad.body)
	}
	if homeHasProject(x.home(t), "invalid-multipart") {
		t.Fatal("invalid multipart created a Home card")
	}
	if after := rootNames(t, x.dir); after != before {
		t.Fatalf("invalid multipart left root entries %q, want %q", after, before)
	}
}
