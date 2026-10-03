package clean

import (
	"fmt"
)

// validateActions rejects every operand before backup directories or commands
// have effects, including operands absent from hand-crafted metadata.
func validateActions(root string, actions []Action) error {
	for _, a := range actions {
		if a.Comment {
			continue
		}
		switch a.Kind {
		case KindGit:
			if err := validateGitAction(root, a); err != nil {
				return err
			}
		case KindMove:
			if _, err := safeJoin(root, a.Source); err != nil {
				return err
			}
			if _, err := safeJoin(root, a.Target); err != nil {
				return err
			}
		case KindRemove, KindMkdir:
			if _, err := safeJoin(root, a.Target); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported action kind %q", a.Kind)
		}
	}
	return nil
}

func validateGitAction(root string, a Action) error {
	argv := a.Argv
	var operands []string
	switch {
	case len(argv) == 5 && argv[0] == cmdGit && argv[1] == cmdMove && argv[2] == argSeparator:
		operands = argv[3:]
		if a.Source != argv[3] || a.Target != argv[4] {
			return fmt.Errorf("git move operands disagree with action paths")
		}
	case len(argv) == 5 && argv[0] == cmdGit && argv[1] == "rm" && argv[2] == "--cached" && argv[3] == argSeparator:
		operands = argv[4:]
		if a.Source != argv[4] || a.Target != "" {
			return fmt.Errorf("git remove operand disagrees with action path")
		}
	default:
		return fmt.Errorf("unsupported cleanup git command")
	}
	for _, operand := range operands {
		if _, err := safeJoin(root, operand); err != nil {
			return err
		}
	}
	return nil
}
