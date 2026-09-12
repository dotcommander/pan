package repo

import (
	"io/fs"
	"path/filepath"
	"testing"
)

func TestInstructionWalkSkipsUnreadableDescendant(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	walk := instructionWalk{root: root, max: 10}
	blocked := filepath.Join(root, "blocked")
	if err := walk.visit(blocked, nil, fs.ErrPermission); err != nil {
		t.Fatalf("unreadable descendant must be skipped: %v", err)
	}
}

func TestInstructionWalkRejectsUnreadableRoot(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	walk := instructionWalk{root: root, max: 10}
	if err := walk.visit(root, nil, fs.ErrPermission); err == nil {
		t.Fatal("unreadable repository root must fail")
	}
}
