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
	s.serveAsset(w, r.PathValue("name"))
}

func (s *Server) assetHandler(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		s.serveAsset(w, name)
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, name string) {
	body, err := webui.Asset(name)
	if err != nil {
		s.problem(w, http.StatusNotFound, "not-found", "no such asset")
		return
	}
	switch name {
	case "index.html":
		w.Header().Set("Content-Type", contentTypeHTML)
	case "app.js":
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case "app.css":
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body); err != nil {
		return
	}
}
