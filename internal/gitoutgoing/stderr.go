package gitoutgoing

type retainedStderr struct {
	limit     int
	data      []byte
	truncated bool
}

func (w *retainedStderr) Write(data []byte) (int, error) {
	keep := min(len(data), w.limit-len(w.data))
	if keep < 0 {
		keep = 0
	}
	w.data = append(w.data, data[:keep]...)
	w.truncated = w.truncated || keep < len(data)
	return len(data), nil
}
