package reportimport

// These encoders spell documented SCIP wire tags independently of the generated
// codec and importer. Fixtures do not call proto.Marshal or implementation code.
func uv(n uint64) []byte {
	var b []byte
	for n >= 128 {
		b = append(b, byte(n)|128)
		n >>= 7
	}
	return append(b, byte(n))
}
func wf(n int, b []byte) []byte {
	out := uv(uint64(n<<3 | 2))
	out = append(out, uv(uint64(len(b)))...)
	return append(out, b...)
}
func wi(n int, v uint64) []byte { return append(uv(uint64(n<<3)), uv(v)...) }
func ws(n int, s string) []byte { return wf(n, []byte(s)) }
func join(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
func packed(n int, values ...uint64) []byte {
	var b []byte
	for _, v := range values {
		b = append(b, uv(v)...)
	}
	return wf(n, b)
}
func metadata() []byte {
	return wf(1, join(wf(2, join(ws(1, "fixture-indexer"), ws(2, "1.2.3"))), ws(3, "file:///missing"), wi(4, 1)))
}
func document(path string, occ []byte, encoding uint64) []byte {
	return wf(2, join(ws(1, path), wf(2, occ), ws(4, "typescript"), ws(5, "é🚀"), wi(6, encoding)))
}
func occurrence() []byte {
	return join(packed(1, 0, 1, 3), ws(2, "local 0"), wi(3, 1|8|128), wf(8, join(wi(1, 0), wi(2, 1), wi(3, 3))), wf(6, join(wi(1, 1), ws(3, "diagnostic"))), wi(99, 7))
}
