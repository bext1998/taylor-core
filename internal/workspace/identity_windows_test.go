//go:build windows

package workspace

import "testing"

func TestIdentityTellsDirectoriesApart(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	first, err := Bind(a)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Bind(a)
	other, _ := Bind(b)
	if first.Identity() == "" || first.Identity() != again.Identity() {
		t.Fatalf("the same directory gave %q and %q", first.Identity(), again.Identity())
	}
	if first.Identity() == other.Identity() {
		t.Fatalf("two directories share identity %q", first.Identity())
	}
}
