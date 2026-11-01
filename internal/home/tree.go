package home

import (
	"cmp"
	"slices"
	"strings"
)

// Node is a folder of the home's tree (spec/home.md, "GET /api/home"): a folder that organizes, or a project with its
// videos. The top node is the home's folder; its videos are the ones it holds itself.
type Node struct {
	Title    string  `json:"title"`
	Path     string  `json:"path"`
	Project  bool    `json:"project"`
	Videos   []Card  `json:"videos"`
	Children []*Node `json:"children"`
}

// BuildTree puts cards in their folders by their Path and Project, so the local home and the team server's build the
// same tree. projects are more project folders to show, ones with no videos yet. title names the top. A card whose
// Project isn't its Path or a folder above it (or isn't a valid path) goes in the folder of its Path.
func BuildTree(title string, cards []Card, projects []string) *Node {
	top := &Node{Title: title, Videos: []Card{}, Children: []*Node{}}
	nodes := map[string]*Node{"": top}
	var at func(path string) *Node
	at = func(path string) *Node {
		if n, ok := nodes[path]; ok {
			return n
		}
		parent, name := "", path
		if i := strings.LastIndexByte(path, '/'); i >= 0 {
			parent, name = path[:i], path[i+1:]
		}
		n := &Node{Title: name, Path: path, Videos: []Card{}, Children: []*Node{}}
		p := at(parent)
		p.Children = append(p.Children, n)
		nodes[path] = n
		return n
	}
	for _, p := range projects {
		if p != "" && ValidPath(p) {
			at(p).Project = true
		}
	}
	for _, c := range cards {
		path, proj := c.Path, c.Project
		if !ValidPath(path) {
			path = ""
		}
		if !ValidPath(proj) || !Within(proj, path) {
			proj = path
		}
		n := at(proj)
		if proj != "" {
			n.Project = true
		}
		n.Videos = append(n.Videos, c)
	}
	var sortNode func(n *Node)
	sortNode = func(n *Node) {
		slices.SortStableFunc(n.Children, func(a, b *Node) int {
			return cmp.Or(cmp.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)), cmp.Compare(a.Title, b.Title))
		})
		slices.SortStableFunc(n.Videos, func(a, b Card) int {
			own := func(c Card) int {
				if c.Path == n.Path {
					return 0
				}
				return 1
			}
			return cmp.Or(cmp.Compare(own(a), own(b)), cmp.Compare(a.Path, b.Path), cmp.Compare(a.Name, b.Name))
		})
		for _, c := range n.Children {
			sortNode(c)
		}
	}
	sortNode(top)
	return top
}
