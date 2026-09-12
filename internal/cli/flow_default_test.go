package cli

import "testing"

func TestFlowRenderDefaultOutputPath(t *testing.T) {
	t.Parallel()
	if got, want := flowRenderDefaultOutput("nested/minimal.yaml"), "out/minimal.html"; got != want {
		t.Fatalf("default output = %q, want %q", got, want)
	}
}
