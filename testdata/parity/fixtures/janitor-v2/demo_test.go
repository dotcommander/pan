package janitorfixture

import "testing"

func TestCovered(t *testing.T) {
	if Covered() != 1 {
		t.Fatal("unexpected result")
	}
}
