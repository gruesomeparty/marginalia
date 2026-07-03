package server

import "testing"

func TestBrowserCommand(t *testing.T) {
	cases := []struct {
		goos, wantName string
		wantErr        bool
	}{
		{"darwin", "open", false},
		{"linux", "xdg-open", false},
		{"windows", "", true},
	}
	for _, c := range cases {
		name, args, err := browserCommand(c.goos, "http://x")
		if (err != nil) != c.wantErr {
			t.Errorf("%s: err=%v wantErr=%v", c.goos, err, c.wantErr)
		}
		if !c.wantErr && (name != c.wantName || len(args) != 1 || args[0] != "http://x") {
			t.Errorf("%s: got %q %v", c.goos, name, args)
		}
	}
}
