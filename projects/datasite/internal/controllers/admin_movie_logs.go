package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"codeberg.org/pmc/Codebase/projects/datasite/internal/db"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/moviedb"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/views"
	"github.com/go-chi/chi/v5"
)

var tmdbURLRe = regexp.MustCompile(`themoviedb\.org/movie/(\d+)`)

type AdminMovieLogsController struct {
	ServerSrc string
	DB        *db.Queries
	Movies    moviedb.MovieDB
}

func (c *AdminMovieLogsController) List() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		logs, err := c.DB.ListMovieLogs(ctx)
		if err != nil {
			slog.Error("failed to list movie logs", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		cinemas, err := c.DB.ListCinemas(ctx)
		if err != nil {
			slog.Error("failed to list cinemas", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		cinemaNames := make(map[int64]string, len(cinemas))
		for _, c := range cinemas {
			cinemaNames[c.ID] = c.Name
		}
		logsVM := make([]views.AdminMovieLogsLogViewModel, 0, len(logs))
		for _, l := range logs {
			movie, err := c.Movies.GetMovieByID(ctx, int(l.MovieID))
			if err != nil {
				slog.Error("failed to get movie", slog.Int64("movieID", l.MovieID), slog.Any("err", err))
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			logVM := views.AdminMovieLogsLogViewModel{
				URL:       fmt.Sprintf("/admin/movie_logs/%d", l.ID),
				MovieName: movie.Title,
				Spoilers:  l.ReviewContainsSpoilers,
			}
			if l.Date.Valid {
				logVM.Date = fmt.Sprintf("%d-%02d-%02d", l.Year(), l.Month(), l.Day())
			}
			if l.Rating.Valid {
				logVM.Rating = fmt.Sprintf("%.1f", l.Rating.Float64)
			}
			if l.CinemaID.Valid {
				logVM.CinemaName = cinemaNames[l.CinemaID.Int64]
			}
			logsVM = append(logsVM, logVM)
		}
		v := views.AdminMovieLogs(views.AdminMovieLogsListViewModel{
			ServerSrc: c.ServerSrc,
			Logs:      logsVM,
		})
		v.Render(ctx, w)
	})
}

func (c *AdminMovieLogsController) GetCreate() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		cinemasVM := c.cinemaViewModels(ctx, w)
		if cinemasVM == nil {
			return
		}
		now := time.Now()
		loc, err := time.LoadLocation("America/Chicago")
		if err == nil {
			now = now.In(loc)
		}
		defaultDate := now.Format("2006-01-02")
		v := views.AdminCreateMovieLog(views.AdminCreateMovieLogViewModel{
			ServerSrc:   c.ServerSrc,
			Cinemas:     cinemasVM,
			DefaultDate: defaultDate,
		})
		v.Render(ctx, w)
	})
}

func (c *AdminMovieLogsController) Create() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		err := r.ParseForm()
		if err != nil {
			slog.Error("failed to parse form", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		movieID, ok := parseMovieID(r.FormValue("movie_id"))
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		params := db.LogMovieParams{
			MovieID:                movieID,
			ReviewContainsSpoilers: r.FormValue("review_contains_spoilers") == "true",
		}
		params.Date = parseDate(r.FormValue("date"))
		params.CinemaID = parseNullInt64(r.FormValue("cinema_id"))
		params.Rating = parseNullRating(r.FormValue("rating"))
		params.Review = parseNullString(r.FormValue("review"))
		log, err := c.DB.LogMovie(ctx, params)
		if err != nil {
			slog.Error("failed to create movie log", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/movie_logs/%d", log.ID), http.StatusSeeOther)
	})
}

func (c *AdminMovieLogsController) Get() http.Handler {
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
		cinemasVM := c.cinemaViewModels(ctx, w)
		if cinemasVM == nil {
			return
		}
		movieName := ""
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
		vm := views.AdminMovieLogViewModel{
			ServerSrc:              c.ServerSrc,
			ID:                     log.ID,
			MovieID:                log.MovieID,
			MovieName:              movieName,
			PosterURL:              posterURL,
			Review:                 log.Review.String,
			ReviewContainsSpoilers: log.ReviewContainsSpoilers,
			Cinemas:                cinemasVM,
		}
		if log.Date.Valid {
			vm.DateStr = fmt.Sprintf("%d-%02d-%02d", log.Year(), log.Month(), log.Day())
		} else {
			now := time.Now()
			loc, err := time.LoadLocation("America/Chicago")
			if err == nil {
				now = now.In(loc)
			}
			vm.DateStr = now.Format("2006-01-02")
		}
		if log.CinemaID.Valid {
			vm.CinemaID = strconv.FormatInt(log.CinemaID.Int64, 10)
		}
		if log.Rating.Valid {
			vm.Rating = fmt.Sprintf("%.1f", log.Rating.Float64)
		}
		v := views.AdminMovieLog(vm)
		v.Render(ctx, w)
	})
}

func (c *AdminMovieLogsController) Post() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, err = c.DB.GetMovieLog(ctx, id)
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
		movieID, ok := parseMovieID(r.FormValue("movie_id"))
		if !ok {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		params := db.EditMovieLogParams{
			ID:                     id,
			MovieID:                movieID,
			ReviewContainsSpoilers: r.FormValue("review_contains_spoilers") == "true",
		}
		params.Date = parseDate(r.FormValue("date"))
		params.CinemaID = parseNullInt64(r.FormValue("cinema_id"))
		params.Rating = parseNullRating(r.FormValue("rating"))
		params.Review = parseNullString(r.FormValue("review"))
		_, err = c.DB.EditMovieLog(ctx, params)
		if err != nil {
			slog.Error("failed to update movie log", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/admin/movie_logs/%d", id), http.StatusSeeOther)
	})
}

func (c *AdminMovieLogsController) Delete() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		idStr := chi.URLParam(r, "id")
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		err = c.DB.DeleteMovieLog(ctx, id)
		if err != nil {
			slog.Error("failed to delete movie log", slog.Any("err", err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		http.Redirect(w, r, "/admin/movie_logs", http.StatusSeeOther)
	})
}

func (c *AdminMovieLogsController) cinemaViewModels(ctx context.Context, w http.ResponseWriter) []views.AdminCinemasCinemaViewModel {
	cinemas, err := c.DB.ListCinemas(ctx)
	if err != nil {
		slog.Error("failed to list cinemas", slog.Any("err", err))
		w.WriteHeader(http.StatusInternalServerError)
		return nil
	}
	cinemasVM := make([]views.AdminCinemasCinemaViewModel, 0, len(cinemas))
	for _, c := range cinemas {
		cinemasVM = append(cinemasVM, views.AdminCinemasCinemaViewModel{
			URL:  fmt.Sprintf("/admin/cinemas/%d", c.ID),
			Name: c.Name,
			ID:   c.ID,
		})
	}
	return cinemasVM
}

func parseMovieID(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	matches := tmdbURLRe.FindStringSubmatch(s)
	if len(matches) >= 2 {
		id, err := strconv.ParseInt(matches[1], 10, 64)
		return id, err == nil && id >= 1
	}
	id, err := strconv.ParseInt(s, 10, 64)
	return id, err == nil && id >= 1
}

func parseDate(s string) sql.NullInt64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullInt64{Valid: false}
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return sql.NullInt64{Valid: false}
	}
	y, m, d := t.Date()
	return sql.NullInt64{
		Int64: int64(y*10000 + int(m)*100 + d),
		Valid: true,
	}
}

func parseNullInt64(s string) sql.NullInt64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullInt64{Valid: false}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return sql.NullInt64{Valid: false}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

func parseNullRating(s string) sql.NullFloat64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullFloat64{Valid: false}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return sql.NullFloat64{Valid: false}
	}
	if v < 0.5 || v > 5.0 {
		return sql.NullFloat64{Valid: false}
	}
	return sql.NullFloat64{Float64: v, Valid: true}
}

func parseNullString(s string) sql.NullString {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullString{Valid: false}
	}
	return sql.NullString{String: s, Valid: true}
}
