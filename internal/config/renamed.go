package config

// RenamedExtensions maps an extension id that was renamed to its id now (spec/extensions.md, "Renamed ids"). The old
// id keeps working wherever an id is read: the config file, the commands, a server's allow list, a person's choices, a
// worker's report, kind ids and routes. What retake writes uses the new id.
var RenamedExtensions = map[string]string{
	"ignition": "scripts", // spec/scripts.md, "It was called Ignition"
}

// ExtensionID is id under its name now: the new id of a renamed extension, else id itself.
func ExtensionID(id string) string {
	if to, ok := RenamedExtensions[id]; ok {
		return to
	}
	return id
}

// ExtensionIDs is ids under their names now, in order, without repeats (["ignition", "scripts"] is ["scripts"]).
// nil stays nil.
func ExtensionIDs(ids []string) []string {
	if ids == nil {
		return nil
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = ExtensionID(id)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

// renameKeys moves the values of renamed ids in m to their new ids. A value already under the new id wins.
func renameKeys[V any](m map[string]V) {
	for old, to := range RenamedExtensions {
		v, ok := m[old]
		if !ok {
			continue
		}
		delete(m, old)
		if _, taken := m[to]; !taken {
			m[to] = v
		}
	}
}
