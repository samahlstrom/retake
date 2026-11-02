package hub

import "strings"

// shareHost retains the public address of a single-video share when its collection becomes the main app.
// Authentication still comes from that video's store; recognizing a host never grants access by itself.
func (f *Folder) shareHost(host string) bool {
	if f.allow[host] {
		return true
	}
	if st := f.homeStore(); st != nil {
		if allowed, err := st.HostAllowed(host); err == nil && allowed {
			return true
		}
	}
	for _, fd := range f.rescan(false) {
		if video := f.projectOf(fd); video != nil {
			if st := video.projectStore(); st != nil {
				if allowed, err := st.HostAllowed(host); err == nil && allowed {
					return true
				}
			}
		}
	}
	return false
}

// Share scopes (spec/home.md, "Share scopes"). A folder token opens the whole folder (no scope) or one video
// ("video:<name>": a video's own share link, opened on the folder's address).

// scopeAllows reports whether a token's scope opens a video.
func scopeAllows(scope, video string) bool {
	if scope == "" {
		return true
	}
	name, ok := strings.CutPrefix(scope, "video:")
	return ok && name == video
}

// retiredScope reports a link made for the project organization that only lived in Retake's database ("project:<id>"
// and "group:<project>/<group>"). It is gone, and so is what those links opened: they are refused with a page that
// says so (410).
func retiredScope(scope string) bool {
	return strings.HasPrefix(scope, "project:") || strings.HasPrefix(scope, "group:")
}
