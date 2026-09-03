package home

import (
	"net/http"

	"codebase.bid/projects/yago/internal/db"
	"github.com/go-chi/chi/v5"
)

type Handler struct {
	Q *db.Queries
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	userCount, err := h.Q.GetUserCount(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	err = HomeView(HomeViewModel{UserCount: userCount}).Render(r.Context(), w)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type Universe interface {
	Queries() *db.Queries
}

func RegisterRoutes(r *chi.Mux, u Universe) {
	r.Get("/", (&Handler{Q: u.Queries()}).ServeHTTP)
}
