package config

import "testing"

func TestSTRMDirectoryLongestBoundary(t *testing.T) {
	m := []STRMDirectoryMapping{{"/nas/strm", "/local/copy"}, {"/nas/strm/movies", "/local/movies"}}
	for _, tc := range []struct{ source, want string }{{"/nas/strm/tv/a.strm", "/local/copy/tv/a.strm"}, {"/nas/strm/movies/a.strm", "/local/movies/a.strm"}} {
		got, err := MapSTRMDirectory(tc.source, m)
		if err != nil || got != tc.want {
			t.Fatal(got, err)
		}
	}
	for _, p := range []string{"/nas/strm-other/a.strm", "/nas/strm/../secret.strm", "/nas/strm/movies/../../secret.strm", "relative.strm", "/nas/strm//a.strm"} {
		if _, err := MapSTRMDirectory(p, m); err == nil {
			t.Fatalf("accepted %s", p)
		}
	}
	if ValidateSTRMDirectoryMap([]STRMDirectoryMapping{{"/a", "/b"}, {"/a", "/c"}}) == nil {
		t.Fatal("duplicate accepted")
	}
}
