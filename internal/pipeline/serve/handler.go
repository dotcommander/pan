package serve

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/dotcommander/pan/internal/pipeline/render"
)

func makeHandler(set *collectionSet) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.Trim(strings.TrimSuffix(r.URL.Path, ".html"), "/")
		if path == "" {
			serveIndex(w, set)
			return
		}
		parts := strings.Split(path, "/")
		switch len(parts) {
		case 1:
			serveCollection(w, r, set, parts[0])
		case 2:
			serveSpec(w, r, set, parts[0], parts[1])
		default:
			http.NotFound(w, r)
		}
	}
}

func serveIndex(w http.ResponseWriter, set *collectionSet) {
	var out bytes.Buffer
	if err := render.Index(set.summaries(), &out); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTML(w, out.Bytes())
}

func serveCollection(w http.ResponseWriter, r *http.Request, set *collectionSet, name string) {
	collection, ok := set.collection(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if collection.Single() {
		serveSpec(w, r, set, collection.Name, collection.Specs[0].Basename)
		return
	}
	var out bytes.Buffer
	if err := render.Collection(collection, &out); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTML(w, out.Bytes())
}

func serveSpec(w http.ResponseWriter, r *http.Request, set *collectionSet, collection, name string) {
	page, ok := set.page(collection, name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeHTML(w, page)
}

func writeHTML(w http.ResponseWriter, page []byte) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(page)
}
