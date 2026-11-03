package userdir

import "testing"

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestUnixIgnoresWindowsVariables(t *testing.T) {
	old := goos
	t.Cleanup(func() { goos = old })
	goos = "linux"
	e := env(map[string]string{"APPDATA": `C:\r`, "LOCALAPPDATA": `C:\l`, "USERPROFILE": `C:\u`})
	if Home(e) != "" || Roaming(e) != "" || Local(e) != "" {
		t.Fatalf("linux used a Windows variable: %q %q %q", Home(e), Roaming(e), Local(e))
	}
	if h := Home(env(map[string]string{"HOME": "/home/ana", "USERPROFILE": `C:\u`})); h != "/home/ana" {
		t.Fatalf("home %q", h)
	}
}

func TestWindowsFolders(t *testing.T) {
	old := goos
	t.Cleanup(func() { goos = old })
	goos = "windows"
	e := env(map[string]string{"APPDATA": `C:\Users\ana\AppData\Roaming`, "LOCALAPPDATA": `C:\Users\ana\AppData\Local`,
		"USERPROFILE": `C:\Users\ana`})
	if got := Roaming(e); got != `C:\Users\ana\AppData\Roaming` {
		t.Fatalf("roaming %q", got)
	}
	if got := Local(e); got != `C:\Users\ana\AppData\Local` {
		t.Fatalf("local %q", got)
	}
	if got := Home(e); got != `C:\Users\ana` {
		t.Fatalf("home %q", got)
	}
	// $HOME, when someone set it (Git Bash does), still wins; nothing set is nothing.
	if got := Home(env(map[string]string{"HOME": `D:\home`, "USERPROFILE": `C:\Users\ana`})); got != `D:\home` {
		t.Fatalf("home with HOME set: %q", got)
	}
	none := env(nil)
	if Home(none) != "" || Roaming(none) != "" || Local(none) != "" {
		t.Fatal("unset variables gave a folder")
	}
}
