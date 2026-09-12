package none

import "testing"

func TestValue(t *testing.T) {
	if Value() != "value" {
		t.Fatal("unexpected result")
	}
}
