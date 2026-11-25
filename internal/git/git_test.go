package git

import "testing"

func TestRepoFromURL(t *testing.T) {
	cases := map[string]string{
		"git@github.com:uptempohq/uptempo.git":       "uptempohq/uptempo",
		"git@github.com:uptempohq/uptempo":           "uptempohq/uptempo",
		"ssh://git@github.com/uptempohq/uptempo.git": "uptempohq/uptempo",
		"https://github.com/uptempohq/uptempo":       "uptempohq/uptempo",
		"https://github.com/uptempohq/uptempo.git/":  "uptempohq/uptempo",
		"https://gitlab.com/uptempohq/uptempo":       "",
		"/srv/git/uptempo.git":                       "",
		"https://github.com/uptempohq":               "",
	}
	for in, want := range cases {
		got, ok := RepoFromURL(in)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q %v, want %q", in, got, ok, want)
		}
	}
}
