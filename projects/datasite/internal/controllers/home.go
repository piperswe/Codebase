package controllers

import (
	"fmt"
	"log/slog"
	"net/http"

	"codeberg.org/pmc/Codebase/projects/datasite/internal/db"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/moviedb"
	"codeberg.org/pmc/Codebase/projects/datasite/internal/views"
)

type HomeController struct {
	ServerSrc string
	DB        *db.Queries
	Movies    moviedb.MovieDB
}

func (c *HomeController) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var err error
	recentLogs, err := c.DB.GetRecentMovieLogs(ctx, 10)
	if err != nil {
		slog.Error("failed to get recent movie logs", slog.Any("err", err))
		w.WriteHeader(500)
		return
	}
	logViewModels := make([]views.MovieLogViewModel, 0, len(recentLogs))
	for _, l := range recentLogs {
		movie, err := c.Movies.GetMovieByID(ctx, int(l.MovieID))
		if err != nil {
			slog.Error("failed to get movie", slog.Int64("movieID", l.MovieID), slog.Any("err", err))
			w.WriteHeader(500)
			return
		}
		logVM := views.MovieLogViewModel{
			URL:       l.URL(),
			MovieName: movie.Title,
			Year:      l.Year(),
			Month:     l.Month(),
			Day:       l.Day(),
		}
		if movie.PosterPath != "" {
			logVM.PosterURL = fmt.Sprintf("https://image.tmdb.org/t/p/w500%s", movie.PosterPath)
		}
		if l.Rating.Valid {
			logVM.Rating = fmt.Sprintf("%.1f", l.Rating.Float64)
		}
		logViewModels = append(logViewModels, logVM)
	}
	home := views.Home(views.HomeViewModel{
		ServerSrc: c.ServerSrc,
		MovieLogs: logViewModels,
	})
	home.Render(ctx, w)
}
