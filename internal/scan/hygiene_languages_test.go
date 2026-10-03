package scan

import "testing"

func TestHygieneRecognizesCanonicalSourceLanguages(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"main.go", "app.ts", "app.tsx", "app.js", "app.jsx", "app.py", "app.rs", "app.c", "app.cpp", "app.java", "app.rb", "app.php"} {
		if !isSourcePath(name) {
			t.Errorf("source omitted: %s", name)
		}
	}
	if isSourcePath("README.md") {
		t.Fatal("non-source admitted")
	}
}
