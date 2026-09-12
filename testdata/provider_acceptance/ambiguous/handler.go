package ambiguous

type Handler interface {
	Handle() string
}

type defaultHandler struct{}

func (defaultHandler) Handle() string { return "handled" }

func NewHandler() Handler { return defaultHandler{} }
