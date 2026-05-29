package lore

import (
	"reflect"
	"testing"
)

func TestBuildAgentArgsDefaultsToInteractivePermissions(t *testing.T) {
	prompt := "update this page"

	got := buildAgentArgs(prompt, false)
	want := []string{"-p", prompt}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildAgentArgs() = %#v, want %#v", got, want)
	}
}

func TestBuildAgentArgsCanOptIntoDangerouslySkippingPermissions(t *testing.T) {
	prompt := "update this page"

	got := buildAgentArgs(prompt, true)
	want := []string{"--dangerously-skip-permissions", "-p", prompt}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("buildAgentArgs() = %#v, want %#v", got, want)
	}
}
