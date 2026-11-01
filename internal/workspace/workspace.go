// Package workspace describes project organization independently of media paths and review status.
package workspace

import (
	"errors"
	"regexp"
	"strings"
)

// Group names a set of videos within a project.
type Group struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// Project organizes videos without moving their source files.
type Project struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Groups []Group `json:"groups"`
}

// Placement assigns a video to a project and optional group.
type Placement struct {
	Video   string `json:"video"`
	Project string `json:"project"`
	Group   string `json:"group"`
}

// Layout is a revision of the shared project view.
type Layout struct {
	Revision   int64       `json:"revision"`
	Projects   []Project   `json:"projects"`
	Placements []Placement `json:"placements"`
}

// Default creates the initial project view.
func Default(title string) Layout {
	return Layout{Projects: []Project{{ID: "default", Title: title, Groups: []Group{}}}, Placements: []Placement{}}
}

// Place returns a video's placement, falling back to the default project.
func (l Layout) Place(video string) Placement {
	for _, p := range l.Placements {
		if p.Video == video {
			return p
		}
	}
	return Placement{Video: video, Project: "default"}
}

// HasProject reports whether the project exists.
func (l Layout) HasProject(id string) bool {
	for _, p := range l.Projects {
		if p.ID == id {
			return true
		}
	}
	return false
}

// HasGroup reports whether a group belongs to this project.
func (l Layout) HasGroup(project, group string) bool {
	for _, p := range l.Projects {
		if p.ID == project {
			for _, g := range p.Groups {
				if g.ID == group {
					return true
				}
			}
		}
	}
	return false
}

var idRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

func validTitle(s string) bool { return strings.TrimSpace(s) != "" && len(s) <= 160 }

// Validate checks identifiers, references and size bounds against the known videos.
func (l Layout) Validate(videos map[string]bool) error {
	if !l.HasProject("default") || len(l.Projects) > 200 || len(l.Placements) > 10000 {
		return errors.New("invalid project list")
	}
	projects, placements := map[string]bool{}, map[string]bool{}
	for _, p := range l.Projects {
		if !idRE.MatchString(p.ID) || !validTitle(p.Title) || projects[p.ID] || len(p.Groups) > 200 {
			return errors.New("invalid or duplicate project")
		}
		projects[p.ID] = true
		groups := map[string]bool{}
		for _, g := range p.Groups {
			if !idRE.MatchString(g.ID) || !validTitle(g.Title) || groups[g.ID] {
				return errors.New("invalid or duplicate group")
			}
			groups[g.ID] = true
		}
	}
	for _, p := range l.Placements {
		if !videos[p.Video] || placements[p.Video] || !projects[p.Project] || p.Group != "" && !l.HasGroup(p.Project, p.Group) {
			return errors.New("invalid video placement")
		}
		placements[p.Video] = true
	}
	return nil
}

// ValidScope checks whether a share scope names an existing entity.
func (l Layout) ValidScope(scope string, videos map[string]bool) bool {
	kind, id, ok := strings.Cut(scope, ":")
	if !ok {
		return false
	}
	switch kind {
	case "video":
		return videos[id]
	case "project":
		return l.HasProject(id)
	case "group":
		p, g, ok := strings.Cut(id, "/")
		return ok && l.HasGroup(p, g)
	}
	return false
}

// Allows reports whether the share scope includes a video.
func (l Layout) Allows(scope, video string) bool {
	if scope == "" {
		return true
	}
	kind, id, _ := strings.Cut(scope, ":")
	p := l.Place(video)
	switch kind {
	case "video":
		return id == video
	case "project":
		return l.HasProject(id) && p.Project == id
	case "group":
		pr, g, ok := strings.Cut(id, "/")
		return ok && l.HasGroup(pr, g) && p.Project == pr && p.Group == g
	}
	return false
}

// Visible hides other projects and even empty group names from a scoped guest.
func (l Layout) Visible(scope string, videos []string) Layout {
	if scope == "" {
		return l
	}
	out := Layout{Revision: l.Revision, Projects: []Project{}, Placements: []Placement{}}
	for _, v := range videos {
		out.Placements = append(out.Placements, l.Place(v))
	}
	for _, p := range l.Projects {
		include := scope == "project:"+p.ID || strings.HasPrefix(scope, "group:"+p.ID+"/")
		for _, v := range out.Placements {
			include = include || v.Project == p.ID
		}
		if !include {
			continue
		}
		groups := []Group{}
		for _, g := range p.Groups {
			keep := scope == "project:"+p.ID || scope == "group:"+p.ID+"/"+g.ID
			for _, v := range out.Placements {
				keep = keep || v.Project == p.ID && v.Group == g.ID
			}
			if keep {
				groups = append(groups, g)
			}
		}
		p.Groups = groups
		out.Projects = append(out.Projects, p)
	}
	return out
}
