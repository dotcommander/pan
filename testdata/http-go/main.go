package main

import "net/http"

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)
	_ = http.ListenAndServe(":8080", mux)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeHealth(w)
}

func writeHealth(w http.ResponseWriter) {
	_, _ = w.Write([]byte("ok"))
}
