package lore

import (
	"reflect"
	"testing"
)

func TestRawSearchArgsSeparateFlagLikeQuery(t *testing.T) {
	paths := []string{"/vault", "/lib"}

	rgWant := []string{"--type", "md", "--color", "always", "--heading", "--line-number", "--smart-case", "--", "--flag-like", "/vault", "/lib"}
	if got := rgSearchArgs("--flag-like", paths); !reflect.DeepEqual(got, rgWant) {
		t.Fatalf("rgSearchArgs = %#v, want %#v", got, rgWant)
	}

	grepWant := []string{"-r", "-n", "--include=*.md", "-i", "--", "--flag-like", "/vault", "/lib"}
	if got := grepSearchArgs("--flag-like", paths); !reflect.DeepEqual(got, grepWant) {
		t.Fatalf("grepSearchArgs = %#v, want %#v", got, grepWant)
	}
}
