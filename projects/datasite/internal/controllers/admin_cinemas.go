package controllers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/piperswe/Codebase/projects/datasite/internal/db"
	"github.com/piperswe/Codebase/projects/datasite/internal/views"
)

type AdminCinemasController struct {
	ServerSrc string
	DB        *db.Queries
}

func (c *AdminCinemasController) List() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cinemas, err := c.DB.ListCinemas(ctx)
		if err != nil {
			slog.Error("failed to list cinemas", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		cinemasVM := make([]views.AdminCinemasCinemaViewModel, 0, len(cinemas))
		for _, c := range cinemas {
			cinemasVM = append(cinemasVM, views.AdminCinemasCinemaViewModel{
				URL:  fmt.Sprintf("/admin/cinemas/%d", c.ID),
				Name: c.Name,
				ID:   c.ID,
			})
		}
		v := views.AdminCinemas(views.AdminCinemasViewModel{
			ServerSrc: c.ServerSrc,
			Cinemas:   cinemasVM,
		})
		v.Render(ctx, w)
	})
}

func (c *AdminCinemasController) GetCreate() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		v := views.AdminCreateCinema(views.AdminCreateCinemaViewModel{
			ServerSrc: c.ServerSrc,
		})
		v.Render(ctx, w)
	})
}

func (c *AdminCinemasController) Create() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		err := r.ParseForm()
		if err != nil {
			slog.Error("failed to parse form", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		cinema, err := c.DB.CreateCinema(ctx, name)
		if err != nil {
			slog.Error("failed to create cinema", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/cinemas/%d", cinema.ID), http.StatusSeeOther)
	})
}

func (c *AdminCinemasController) Get() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cinema, err := c.DB.GetCinema(ctx, id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		v := views.AdminCinema(views.AdminCinemaViewModel{
			ServerSrc: c.ServerSrc,
			Name:      cinema.Name,
			ID:        cinema.ID,
		})
		v.Render(ctx, w)
	})
}

func (c *AdminCinemasController) Delete() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		err = c.DB.DeleteCinema(ctx, id)
		if err != nil {
			slog.Error("failed to delete cinema", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/admin/cinemas", http.StatusSeeOther)
	})
}

func (c *AdminCinemasController) Post() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		cinema, err := c.DB.GetCinema(ctx, id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		err = r.ParseForm()
		if err != nil {
			slog.Error("failed to parse form", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		name := r.FormValue("name")
		c.DB.UpdateCinema(ctx, db.UpdateCinemaParams{
			ID:   cinema.ID,
			Name: name,
		})
		http.Redirect(w, r, fmt.Sprintf("/admin/cinemas/%d", id), http.StatusSeeOther)
	})
}
