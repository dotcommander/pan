package analyze

// CallsSymbol reports whether a call's type-resolved, captured declaration is
// this symbol. A bare name, even on a confirmed legacy edge, is not identity.
func (e Edge) CallsSymbol(symbol Symbol) bool {
	return e.Kind == "calls" && e.Target != nil && e.To == symbol.Name &&
		e.Target.Path == symbol.Location.Path && e.Target.Line == symbol.Location.Line
}
