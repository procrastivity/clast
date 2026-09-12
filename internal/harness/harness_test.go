package harness

import (
	"testing"

	"github.com/procrastivity/clast/internal/manifest"
	"github.com/procrastivity/clast/internal/surface"
)

// TestProjectable_StructuralNamespaceFilter pins SURFACE V32: the
// projectable set is exactly the `plumbing` namespace, keyed off verb name
// position, not off the C3.2 kind annotation. A top-level porcelain verb
// that is nonetheless kind=plumbing (doctor, install, …) must never appear
// in a harness projection, even though a kind-only filter would include it.
func TestProjectable_StructuralNamespaceFilter(t *testing.T) {
	verbs := []manifest.Verb{
		{Name: "init", Kind: surface.Plumbing},
		{Name: "breadcrumb", Kind: surface.Plumbing},
		{Name: "doctor", Kind: surface.Plumbing},
		{Name: "install", Kind: surface.Plumbing},
		{Name: "uninstall", Kind: surface.Plumbing},
		{Name: "version", Kind: surface.Plumbing},
		{Name: "manifest", Kind: surface.Plumbing},
		{Name: "wake", Kind: surface.LLM},
		{Name: "brief", Kind: surface.LLM},
		{Name: "retro", Kind: surface.LLM},
		{Name: "plumbing capture", Kind: surface.Plumbing},
		{Name: "plumbing curate", Kind: surface.Plumbing},
		{Name: "plumbing wake", Kind: surface.Plumbing},
		{Name: "plumbing brief", Kind: surface.Plumbing},
		{Name: "plumbing retro", Kind: surface.Plumbing},
		{Name: "plumbing asset", Kind: surface.Plumbing},
	}

	got := Projectable(verbs)

	wantNames := map[string]bool{
		"plumbing capture": true,
		"plumbing curate":  true,
		"plumbing wake":    true,
		"plumbing brief":   true,
		"plumbing retro":   true,
		"plumbing asset":   true,
	}
	if len(got) != len(wantNames) {
		t.Fatalf("Projectable() = %d verb(s) %+v, want %d", len(got), got, len(wantNames))
	}
	for _, v := range got {
		if !wantNames[v.Name] {
			t.Errorf("Projectable() included %q, which sits outside the plumbing namespace", v.Name)
		}
	}

	excluded := []string{"init", "breadcrumb", "doctor", "install", "uninstall", "version", "manifest", "wake", "brief", "retro"}
	for _, name := range excluded {
		for _, v := range got {
			if v.Name == name {
				t.Errorf("Projectable() included %q (kind=plumbing but outside the namespace); V32 requires it excluded", name)
			}
		}
	}
}

// TestProjectable_EmptyInput asserts a nil/empty verb list projects to an
// empty (non-nil) slice, never a nil one a caller might mishandle as "no
// filter applied".
func TestProjectable_EmptyInput(t *testing.T) {
	got := Projectable(nil)
	if len(got) != 0 {
		t.Fatalf("Projectable(nil) = %+v, want empty", got)
	}
}
