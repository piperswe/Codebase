package home

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

type Handler struct{}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	err := HomeView().Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func RegisterRoutes(r *chi.Mux) {
	r.Get("/", (&Handler{}).ServeHTTP)
}
