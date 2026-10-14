package reportimport

import (
	"fmt"
	"slices"

	"github.com/scip-code/scip/bindings/go/scip"
)

func scipOccurrence(occ *scip.Occurrence, r *Report) (View, error) {
	rangeData, err := effectiveRange(occ.GetRange(), occ.GetSingleLineRange(), occ.GetMultiLineRange(), true)
	if err != nil {
		return View{}, err
	}
	enclosing, err := effectiveRange(occ.GetEnclosingRange(), occ.GetSingleLineEnclosingRange(), occ.GetMultiLineEnclosingRange(), false)
	if err != nil {
		return View{}, fmt.Errorf("enclosing range: %w", err)
	}
	if len(enclosing) > 0 && (slices.Compare(enclosing[:2], rangeData[:2]) > 0 || slices.Compare(enclosing[2:], rangeData[2:]) < 0) {
		return View{}, fmt.Errorf("enclosing range does not contain occurrence")
	}
	if occ.GetSymbolRoles() < 0 || occ.GetSymbolRoles() & ^int32(127) != 0 {
		r.Unsupported = append(r.Unsupported, fmt.Sprintf("SCIP role bits %d include uninterpreted bits; numeric roles retained", occ.GetSymbolRoles()))
	}
	return View{Symbol: occ.GetSymbol(), Roles: occ.GetSymbolRoles(), Range: rangeData, EnclosingRange: enclosing}, nil
}

func effectiveRange(legacy []int32, single *scip.SingleLineRange, multi *scip.MultiLineRange, required bool) ([]int64, error) {
	var old, current []int64
	if len(legacy) > 0 {
		switch len(legacy) {
		case 3:
			old = []int64{int64(legacy[0]), int64(legacy[1]), int64(legacy[0]), int64(legacy[2])}
		case 4:
			old = []int64{int64(legacy[0]), int64(legacy[1]), int64(legacy[2]), int64(legacy[3])}
		default:
			return nil, fmt.Errorf("SCIP legacy range requires 3 or 4 integers")
		}
		if err := validRange(old[:2], old[2:]); err != nil {
			return nil, err
		}
	}
	if single != nil {
		current = []int64{int64(single.GetLine()), int64(single.GetStartCharacter()), int64(single.GetLine()), int64(single.GetEndCharacter())}
	}
	if multi != nil {
		if single != nil {
			return nil, fmt.Errorf("conflicting typed SCIP ranges")
		}
		current = []int64{int64(multi.GetStartLine()), int64(multi.GetStartCharacter()), int64(multi.GetEndLine()), int64(multi.GetEndCharacter())}
	}
	if len(current) > 0 {
		if err := validRange(current[:2], current[2:]); err != nil {
			return nil, err
		}
		if len(old) > 0 && !slices.Equal(old, current) {
			return nil, fmt.Errorf("legacy and typed SCIP ranges disagree")
		}
		return current, nil
	}
	if len(old) == 0 && required {
		return nil, fmt.Errorf("SCIP occurrence missing range")
	}
	return old, nil
}
