package config

import "testing"

func TestPresentationValidation(t *testing.T) {
	for _, name := range []string{"auto", "dark", "light", "catppuccin", "tokyo-night"} {
		if err := (Config{Theme: name}).validatePresentation(); err != nil {
			t.Fatal(err)
		}
	}
	if err := (Config{Theme: "missing"}).validatePresentation(); err == nil {
		t.Fatal("unknown theme accepted")
	}
	p := Palette{Background: "#111122", Foreground: "#ddddff", Muted: "#aaaabb", Accent: "#77aaff", Success: "#77ee88", Warning: "#ffee99", Error: "#ff8899", Merged: "#bb99ff"}
	c := Config{Theme: "custom", Themes: map[string]Palette{"custom": p}}
	if err := c.validatePresentation(); err != nil {
		t.Fatal(err)
	}
	p.Accent = "red"
	c.Themes["custom"] = p
	if err := c.validatePresentation(); err == nil {
		t.Fatal("invalid color accepted")
	}
	c = Config{Theme: "dark"}
	c.Activity.NotifyOn = []string{"unknown"}
	if err := c.validatePresentation(); err == nil {
		t.Fatal("unknown event accepted")
	}
	c.Activity.NotifyOn = []string{}
	if err := c.validatePresentation(); err != nil {
		t.Fatal("empty notifications rejected", err)
	}
}
