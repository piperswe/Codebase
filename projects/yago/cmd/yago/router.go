package main

import (
	"net/http"

	"codebase.bid/projects/yago/internal/app/home"
	"github.com/go-chi/chi/v5"
)

func notYetImplemented(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "Not yet implemented", http.StatusNotImplemented)
}

func NewRouter(u *universe) *chi.Mux {
	r := chi.NewRouter()
	r.Use(u.o11y.HTTPMiddleware)
	home.RegisterRoutes(r, u)
	r.Get("/profiles", notYetImplemented)
	r.Post("/profiles", notYetImplemented)
	r.Get("/profiles/{id}", notYetImplemented)
	r.Post("/profiles/{id}", notYetImplemented)
	r.Post("/profiles/{id}/delete", notYetImplemented)
	r.Post("/profiles/{id}/friend/add", notYetImplemented)
	r.Post("/profiles/{id}/friend/remove", notYetImplemented)
	r.Get("/friends", notYetImplemented)
	r.Post("/friends/circles", notYetImplemented)
	r.Post("/friends/circles/{id}", notYetImplemented)
	r.Post("/friends/circles/{id}/delete", notYetImplemented)
	r.Get("/timeline", notYetImplemented)
	r.Get("/posts", notYetImplemented)
	r.Post("/posts", notYetImplemented)
	r.Post("/posts/{id}", notYetImplemented)
	r.Post("/posts/{id}/delete", notYetImplemented)
	r.Get("/login", notYetImplemented)
	r.Post("/login", notYetImplemented)
	r.Get("/logout", notYetImplemented)
	r.Post("/logout", notYetImplemented)
	return r
}
