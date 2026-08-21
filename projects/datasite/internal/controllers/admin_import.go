package controllers

import (
	"context"
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/piperswe/Codebase/projects/datasite/internal/db"
	"github.com/piperswe/Codebase/projects/datasite/internal/moviedb"
	"github.com/piperswe/Codebase/projects/datasite/internal/views"
)

type AdminImportController struct {
	ServerSrc string
	DB        *db.Queries
	Movies    moviedb.MovieDB
}

func (c *AdminImportController) Get() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		v := views.AdminImportForm(views.AdminImportViewModel{
			ServerSrc: c.ServerSrc,
		})
		v.Render(ctx, w)
	})
}

func (c *AdminImportController) Post() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		err := r.ParseMultipartForm(32 << 20)
		if err != nil {
			slog.Error("failed to parse multipart form", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		results := make([]views.ImportResult, 0)
		imported := 0
		skipped := 0

		if f, _, err := r.FormFile("diary"); err == nil {
			defer f.Close()
			r, imp, skp := c.processDiary(ctx, f)
			results = append(results, r...)
			imported += imp
			skipped += skp
		}
		if f, _, err := r.FormFile("ratings"); err == nil {
			defer f.Close()
			r, imp, skp := c.processRatings(ctx, f)
			results = append(results, r...)
			imported += imp
			skipped += skp
		}
		if f, _, err := r.FormFile("reviews"); err == nil {
			defer f.Close()
			r, imp, skp := c.processReviews(ctx, f)
			results = append(results, r...)
			imported += imp
			skipped += skp
		}

		total := imported + skipped
		v := views.AdminImportReport(views.AdminImportReportViewModel{
			ServerSrc: c.ServerSrc,
			Total:     total,
			Imported:  imported,
			Skipped:   skipped,
			Results:   results,
		})
		v.Render(ctx, w)
	})
}

func (c *AdminImportController) processDiary(ctx context.Context, file io.Reader) ([]views.ImportResult, int, int) {
	results := make([]views.ImportResult, 0)
	reader := csv.NewReader(file)
	lineNum := 0
	imported := 0
	skipped := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("failed to read CSV row", slog.Any("err", err))
			continue
		}
		lineNum++
		if lineNum == 1 {
			continue
		}
		if len(record) < 8 {
			slog.Warn("short CSV row", slog.Int("line", lineNum), slog.Int("cols", len(record)))
			continue
		}
		name := strings.TrimSpace(record[1])
		yearStr := strings.TrimSpace(record[2])
		year, err := strconv.Atoi(yearStr)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "diary",
				MovieName: name,
				Success:   false,
				Error:     fmt.Sprintf("invalid year: %s", yearStr),
			})
			skipped++
			continue
		}
		rating := strings.TrimSpace(record[4])
		watchedDate := strings.TrimSpace(record[7])
		movieID, tmdbTitle, err := c.Movies.SearchMovie(ctx, name, year)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "diary",
				MovieName: name,
				Watched:   watchedDate,
				Rating:    rating,
				Success:   false,
				Error:     fmt.Sprintf("TMDB search: %v", err),
			})
			skipped++
			continue
		}
		params := db.LogMovieParams{
			MovieID:                movieID,
			ReviewContainsSpoilers: false,
		}
		params.Date = parseDate(watchedDate)
		if rating != "" {
			v, err := strconv.ParseFloat(rating, 64)
			if err == nil && v >= 0.5 && v <= 5.0 {
				params.Rating = sql.NullFloat64{Float64: v, Valid: true}
			}
		}
		_, err = c.DB.LogMovie(ctx, params)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "diary",
				MovieName: tmdbTitle,
				Watched:   watchedDate,
				Rating:    rating,
				Success:   false,
				Error:     fmt.Sprintf("DB: %v", err),
			})
			skipped++
			continue
		}
		results = append(results, views.ImportResult{
			Source:    "diary",
			MovieName: tmdbTitle,
			Watched:   watchedDate,
			Rating:    rating,
			Success:   true,
		})
		imported++
	}
	return results, imported, skipped
}

func (c *AdminImportController) processRatings(ctx context.Context, file io.Reader) ([]views.ImportResult, int, int) {
	results := make([]views.ImportResult, 0)
	reader := csv.NewReader(file)
	lineNum := 0
	imported := 0
	skipped := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("failed to read CSV row", slog.Any("err", err))
			continue
		}
		lineNum++
		if lineNum == 1 {
			continue
		}
		if len(record) < 5 {
			continue
		}
		name := strings.TrimSpace(record[1])
		yearStr := strings.TrimSpace(record[2])
		year, err := strconv.Atoi(yearStr)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "ratings",
				MovieName: name,
				Success:   false,
				Error:     fmt.Sprintf("invalid year: %s", yearStr),
			})
			skipped++
			continue
		}
		rating := strings.TrimSpace(record[4])
		watchedDate := strings.TrimSpace(record[0])
		movieID, tmdbTitle, err := c.Movies.SearchMovie(ctx, name, year)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "ratings",
				MovieName: name,
				Watched:   watchedDate,
				Rating:    rating,
				Success:   false,
				Error:     fmt.Sprintf("TMDB search: %v", err),
			})
			skipped++
			continue
		}
		dateInt, _ := parseDateToInt(watchedDate)
		log, err := c.findLogByMovieAndDate(ctx, movieID, dateInt)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "ratings",
				MovieName: tmdbTitle,
				Watched:   watchedDate,
				Rating:    rating,
				Success:   false,
				Error:     "log not found to update",
			})
			skipped++
			continue
		}
		v, err := strconv.ParseFloat(rating, 64)
		if err != nil || v < 0.5 || v > 5.0 {
			results = append(results, views.ImportResult{
				Source:    "ratings",
				MovieName: tmdbTitle,
				Watched:   watchedDate,
				Rating:    rating,
				Success:   false,
				Error:     "invalid rating",
			})
			skipped++
			continue
		}
		_, err = c.DB.EditMovieLog(ctx, db.EditMovieLogParams{
			ID:                     log.ID,
			MovieID:                log.MovieID,
			Date:                   log.Date,
			CinemaID:               log.CinemaID,
			Rating:                 sql.NullFloat64{Float64: v, Valid: true},
			Review:                 log.Review,
			ReviewContainsSpoilers: log.ReviewContainsSpoilers,
		})
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "ratings",
				MovieName: tmdbTitle,
				Watched:   watchedDate,
				Rating:    rating,
				Success:   false,
				Error:     fmt.Sprintf("DB: %v", err),
			})
			skipped++
			continue
		}
		results = append(results, views.ImportResult{
			Source:    "ratings",
			MovieName: tmdbTitle,
			Watched:   watchedDate,
			Rating:    rating,
			Success:   true,
		})
		imported++
	}
	return results, imported, skipped
}

func (c *AdminImportController) processReviews(ctx context.Context, file io.Reader) ([]views.ImportResult, int, int) {
	results := make([]views.ImportResult, 0)
	reader := csv.NewReader(file)
	lineNum := 0
	imported := 0
	skipped := 0
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			slog.Error("failed to read CSV row", slog.Any("err", err))
			continue
		}
		lineNum++
		if lineNum == 1 {
			continue
		}
		if len(record) < 9 {
			continue
		}
		name := strings.TrimSpace(record[1])
		yearStr := strings.TrimSpace(record[2])
		year, err := strconv.Atoi(yearStr)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "reviews",
				MovieName: name,
				Success:   false,
				Error:     fmt.Sprintf("invalid year: %s", yearStr),
			})
			skipped++
			continue
		}
		rating := strings.TrimSpace(record[4])
		review := strings.TrimSpace(record[6])
		tags := strings.TrimSpace(record[7])
		watchedDate := strings.TrimSpace(record[8])
		if tags != "" {
			review = review + "\n\nTags: " + tags
		}
		movieID, tmdbTitle, err := c.Movies.SearchMovie(ctx, name, year)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "reviews",
				MovieName: name,
				Watched:   watchedDate,
				Rating:    rating,
				Success:   false,
				Error:     fmt.Sprintf("TMDB search: %v", err),
			})
			skipped++
			continue
		}
		dateInt, _ := parseDateToInt(watchedDate)
		log, err := c.findLogByMovieAndDate(ctx, movieID, dateInt)
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "reviews",
				MovieName: tmdbTitle,
				Watched:   watchedDate,
				Success:   false,
				Error:     "log not found to update",
			})
			skipped++
			continue
		}
		var ratingVal sql.NullFloat64
		if rating != "" {
			v, err := strconv.ParseFloat(rating, 64)
			if err == nil && v >= 0.5 && v <= 5.0 {
				ratingVal = sql.NullFloat64{Float64: v, Valid: true}
			}
		}
		_, err = c.DB.EditMovieLog(ctx, db.EditMovieLogParams{
			ID:                     log.ID,
			MovieID:                log.MovieID,
			Date:                   log.Date,
			CinemaID:               log.CinemaID,
			Rating:                 ratingVal,
			Review:                 sql.NullString{String: review, Valid: review != ""},
			ReviewContainsSpoilers: false,
		})
		if err != nil {
			results = append(results, views.ImportResult{
				Source:    "reviews",
				MovieName: tmdbTitle,
				Watched:   watchedDate,
				Rating:    rating,
				Success:   false,
				Error:     fmt.Sprintf("DB: %v", err),
			})
			skipped++
			continue
		}
		results = append(results, views.ImportResult{
			Source:    "reviews",
			MovieName: tmdbTitle,
			Watched:   watchedDate,
			Rating:    rating,
			Success:   true,
		})
		imported++
	}
	return results, imported, skipped
}

func (c *AdminImportController) findLogByMovieAndDate(ctx context.Context, movieID int64, date int64) (db.MovieLog, error) {
	logs, err := c.DB.GetLogsForMovie(ctx, movieID)
	if err != nil {
		return db.MovieLog{}, err
	}
	for _, l := range logs {
		if l.Date.Valid && l.Date.Int64 == date {
			return l, nil
		}
	}
	return db.MovieLog{}, fmt.Errorf("no log found for movie %d on date %d", movieID, date)
}

func parseDateToInt(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty date")
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return 0, err
	}
	y, m, d := t.Date()
	return int64(y*10000 + int(m)*100 + d), nil
}
