package ambiguous

import "testing"

func TestHandler(t *testing.T) {
	if NewHandler().Handle() != "handled" {
		t.Fatal("unexpected result")
	}
}
