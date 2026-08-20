package controllers

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"codeberg.org/pmc/Codebase/projects/datasite/internal/db"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/moviedb"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/views"
	"github.com/go-chi/chi/v5"
)

type MovieLogController struct {
	ServerSrc string
	DB        *db.Queries
	Movies    moviedb.MovieDB
}

func (c *MovieLogController) Get() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		log, err := c.DB.GetMovieLog(ctx, id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		movieName := "Unknown movie"
		posterURL := ""
		movie, err := c.Movies.GetMovieByID(ctx, int(log.MovieID))
		if err != nil {
			slog.Error("failed to get movie", slog.Int64("movieID", log.MovieID), slog.Any("err", err))
		} else {
			movieName = movie.Title
			if movie.PosterPath != "" {
				posterURL = fmt.Sprintf("https://image.tmdb.org/t/p/w500%s", movie.PosterPath)
			}
		}
		cinemaName := ""
		if log.CinemaID.Valid {
			cinema, err := c.DB.GetCinema(ctx, log.CinemaID.Int64)
			if err != nil {
				slog.Error("failed to get cinema", slog.Int64("cinemaID", log.CinemaID.Int64), slog.Any("err", err))
			} else {
				cinemaName = cinema.Name
			}
		}
		vm := views.PublicMovieLogViewModel{
			ServerSrc:              c.ServerSrc,
			ID:                     log.ID,
			MovieName:              movieName,
			PosterURL:              posterURL,
			Review:                 log.Review.String,
			ReviewContainsSpoilers: log.ReviewContainsSpoilers,
			CinemaName:             cinemaName,
		}
		if log.Date.Valid {
			vm.DateStr = fmt.Sprintf("%d-%02d-%02d", log.Year(), log.Month(), log.Day())
		}
		if log.Rating.Valid {
			vm.Rating = fmt.Sprintf("%.1f", log.Rating.Float64)
		}
		v := views.PublicMovieLog(vm)
		v.Render(ctx, w)
	})
}
