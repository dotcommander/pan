package obvious

import "testing"

func TestUsed(t *testing.T) {
	if Used() != "used" {
		t.Fatal("unexpected result")
	}
}
