package serve

import (
	"net/http"

	"github.com/stainless-code/verdict/internal/webui"
)

const contentTypeHTML = "text/html; charset=utf-8"

func (s *Server) serveIndex(w http.ResponseWriter) {
	body, err := webui.Asset("index.html")
	if err != nil {
		s.problem(w, http.StatusInternalServerError, "asset-error", "the embedded UI is unavailable")
		return
	}
	w.Header().Set("Content-Type", contentTypeHTML)
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		return
	}
}

func (s *Server) handleAsset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	body, err := webui.Asset(name)
	if err != nil {
		s.problem(w, http.StatusNotFound, "not-found", "no such asset")
		return
	}
	if name == "app.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		return
	}
}
