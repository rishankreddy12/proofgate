package version

import "testing"

func TestVersionDefault(t *testing.T) {
	if Version == "" {
		t.Fatal("Version must default to a non-empty value")
	}
}
