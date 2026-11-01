package hub

import (
	"github.com/samahlstrom/retake/internal/home"
	"github.com/samahlstrom/retake/internal/project"
)

// machines are the computers whose workers poll this folder's projects (spec/home.md, "machines"): with retake open,
// the in-process worker.
func (f *Folder) machines() []home.Machine {
	var workers [][]project.EditingWorker
	for _, fd := range f.rescan(false) {
		fp := f.projectOf(fd)
		if fp == nil {
			continue
		}
		st := fp.projectStore()
		if st == nil {
			continue
		}
		if a, err := st.Automation(); err == nil {
			workers = append(workers, a.Workers)
		}
	}
	return home.Machines(f.opts.Now(), workers...)
}
