package controllers

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"github.com/go-faster/jx"
	"github.com/ogen-go/ogen/ogenerrors"
	"github.com/piperswe/Codebase/projects/datasite/internal/db"
	"github.com/piperswe/Codebase/projects/datasite/internal/oas"
)

// APIHandler implements the generated oas.Handler and oas.SecurityHandler.
type APIHandler struct {
	DB          *db.Queries
	DBConn      *sql.DB
	AdminAPIKey string
}

// apiError is an error carrying an HTTP status code for ogen convenient errors.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

func newAPIError(status int, msg string) *apiError {
	return &apiError{status: status, msg: msg}
}

// NewError implements oas.Handler convenient error handling.
func (h *APIHandler) NewError(ctx context.Context, err error) *oas.InternalErrorStatusCode {
	if ae, ok := errors.AsType[*apiError](err); ok {
		return &oas.InternalErrorStatusCode{
			StatusCode: ae.status,
			Response:   oas.Error{Error: ae.msg},
		}
	}
	if _, ok := errors.AsType[*ogenerrors.SecurityError](err); ok {
		return &oas.InternalErrorStatusCode{
			StatusCode: 401,
			Response:   oas.Error{Error: "invalid api key"},
		}
	}
	slog.ErrorContext(ctx, "api error", slog.Any("err", err))
	return &oas.InternalErrorStatusCode{
		StatusCode: 500,
		Response:   oas.Error{Error: "internal error"},
	}
}

// HandleAdminAPIKey implements oas.SecurityHandler.
// It checks both Bearer token and the datasite-admin-api-key cookie.
func (h *APIHandler) HandleAdminAPIKey(ctx context.Context, operationName oas.OperationName, t oas.AdminAPIKey) (context.Context, error) {
	// Check Bearer token first
	if h.AdminAPIKey != "" && t.Token == h.AdminAPIKey {
		return ctx, nil
	}

	return ctx, newAPIError(401, "invalid api key")
}

func isConstraintViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "constraint failed")
}

func toNullInt64(v oas.OptNilInt64) sql.NullInt64 {
	if !v.IsSet() || v.IsNull() {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v.Value, Valid: true}
}

func toNullFloat64(v oas.OptNilFloat64) sql.NullFloat64 {
	if !v.IsSet() || v.IsNull() {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: v.Value, Valid: true}
}

func toNullString(v oas.OptNilString) sql.NullString {
	if !v.IsSet() || v.IsNull() {
		return sql.NullString{}
	}
	return sql.NullString{String: v.Value, Valid: true}
}

func cinemaToOAS(c db.Cinema) oas.Cinema {
	return oas.Cinema{ID: c.ID, Name: c.Name}
}

func movieLogToOAS(l db.MovieLog) oas.MovieLog {
	out := oas.MovieLog{
		ID:                     l.ID,
		MovieID:                l.MovieID,
		ReviewContainsSpoilers: l.ReviewContainsSpoilers,
	}
	if l.Date.Valid {
		out.Date.SetTo(l.Date.Int64)
	}
	if l.CinemaID.Valid {
		out.CinemaID.SetTo(l.CinemaID.Int64)
	}
	if l.Rating.Valid {
		out.Rating.SetTo(l.Rating.Float64)
	}
	if l.Review.Valid {
		out.Review.SetTo(l.Review.String)
	}
	return out
}

// ListCinemas implements listCinemas operation.
func (h *APIHandler) ListCinemas(ctx context.Context) (oas.ListCinemasRes, error) {
	cinemas, err := h.DB.ListCinemas(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list cinemas: %w", err)
	}
	out := make(oas.ListCinemasOKApplicationJSON, 0, len(cinemas))
	for _, c := range cinemas {
		out = append(out, cinemaToOAS(c))
	}
	return &out, nil
}

// CreateCinema implements createCinema operation.
func (h *APIHandler) CreateCinema(ctx context.Context, req *oas.CinemaInput) (oas.CreateCinemaRes, error) {
	if req.Name == "" {
		return nil, newAPIError(400, "name is required")
	}
	c, err := h.DB.CreateCinema(ctx, req.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to create cinema: %w", err)
	}
	res := cinemaToOAS(c)
	return &res, nil
}

// GetCinema implements getCinema operation.
func (h *APIHandler) GetCinema(ctx context.Context, params oas.GetCinemaParams) (oas.GetCinemaRes, error) {
	c, err := h.DB.GetCinema(ctx, params.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newAPIError(404, "cinema not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get cinema id=%d: %w", params.ID, err)
	}
	res := cinemaToOAS(c)
	return &res, nil
}

// UpdateCinema implements updateCinema operation.
func (h *APIHandler) UpdateCinema(ctx context.Context, req *oas.CinemaInput, params oas.UpdateCinemaParams) (oas.UpdateCinemaRes, error) {
	if req.Name == "" {
		return nil, newAPIError(400, "name is required")
	}
	c, err := h.DB.UpdateCinema(ctx, db.UpdateCinemaParams{Name: req.Name, ID: params.ID})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newAPIError(404, "cinema not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update cinema id=%d: %w", params.ID, err)
	}
	res := cinemaToOAS(c)
	return &res, nil
}

// PatchCinema implements patchCinema operation.
func (h *APIHandler) PatchCinema(ctx context.Context, req *oas.CinemaPatch, params oas.PatchCinemaParams) (oas.PatchCinemaRes, error) {
	c, err := h.DB.GetCinema(ctx, params.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newAPIError(404, "cinema not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get cinema id=%d: %w", params.ID, err)
	}

	name := c.Name
	if req.Name.IsSet() {
		if req.Name.Value == "" {
			return nil, newAPIError(400, "name must be a non-empty string")
		}
		name = req.Name.Value
	}

	updated, err := h.DB.UpdateCinema(ctx, db.UpdateCinemaParams{Name: name, ID: params.ID})
	if err != nil {
		return nil, fmt.Errorf("failed to update cinema id=%d: %w", params.ID, err)
	}
	res := cinemaToOAS(updated)
	return &res, nil
}

// DeleteCinema implements deleteCinema operation.
func (h *APIHandler) DeleteCinema(ctx context.Context, params oas.DeleteCinemaParams) (oas.DeleteCinemaRes, error) {
	err := h.DB.DeleteCinema(ctx, params.ID)
	if isConstraintViolation(err) {
		return nil, newAPIError(409, "cinema is referenced by existing movie logs")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to delete cinema id=%d: %w", params.ID, err)
	}
	return &oas.DeleteCinemaNoContent{}, nil
}

// ListMovieLogs implements listMovieLogs operation.
func (h *APIHandler) ListMovieLogs(ctx context.Context) (oas.ListMovieLogsRes, error) {
	logs, err := h.DB.ListMovieLogs(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list movie logs: %w", err)
	}
	out := make(oas.ListMovieLogsOKApplicationJSON, 0, len(logs))
	for _, l := range logs {
		out = append(out, movieLogToOAS(l))
	}
	return &out, nil
}

// CreateMovieLog implements createMovieLog operation.
func (h *APIHandler) CreateMovieLog(ctx context.Context, req *oas.MovieLogInput) (oas.CreateMovieLogRes, error) {
	if req.MovieID == 0 {
		return nil, newAPIError(400, "movie_id is required")
	}
	l, err := h.DB.LogMovie(ctx, db.LogMovieParams{
		MovieID:                req.MovieID,
		Date:                   toNullInt64(req.Date),
		CinemaID:               toNullInt64(req.CinemaID),
		Rating:                 toNullFloat64(req.Rating),
		Review:                 toNullString(req.Review),
		ReviewContainsSpoilers: req.ReviewContainsSpoilers.Or(false),
	})
	if isConstraintViolation(err) {
		return nil, newAPIError(409, "cinema_id does not reference an existing cinema")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create movie log: %w", err)
	}
	res := movieLogToOAS(l)
	return &res, nil
}

// GetMovieLog implements getMovieLog operation.
func (h *APIHandler) GetMovieLog(ctx context.Context, params oas.GetMovieLogParams) (oas.GetMovieLogRes, error) {
	l, err := h.DB.GetMovieLog(ctx, params.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newAPIError(404, "movie log not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get movie log id=%d: %w", params.ID, err)
	}
	res := movieLogToOAS(l)
	return &res, nil
}

// UpdateMovieLog implements updateMovieLog operation.
func (h *APIHandler) UpdateMovieLog(ctx context.Context, req *oas.MovieLogInput, params oas.UpdateMovieLogParams) (oas.UpdateMovieLogRes, error) {
	if req.MovieID == 0 {
		return nil, newAPIError(400, "movie_id is required")
	}
	l, err := h.DB.EditMovieLog(ctx, db.EditMovieLogParams{
		MovieID:                req.MovieID,
		Date:                   toNullInt64(req.Date),
		CinemaID:               toNullInt64(req.CinemaID),
		Rating:                 toNullFloat64(req.Rating),
		Review:                 toNullString(req.Review),
		ReviewContainsSpoilers: req.ReviewContainsSpoilers.Or(false),
		ID:                     params.ID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newAPIError(404, "movie log not found")
	}
	if isConstraintViolation(err) {
		return nil, newAPIError(409, "cinema_id does not reference an existing cinema")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update movie log id=%d: %w", params.ID, err)
	}
	res := movieLogToOAS(l)
	return &res, nil
}

// PatchMovieLog implements patchMovieLog operation.
func (h *APIHandler) PatchMovieLog(ctx context.Context, req *oas.MovieLogPatch, params oas.PatchMovieLogParams) (oas.PatchMovieLogRes, error) {
	l, err := h.DB.GetMovieLog(ctx, params.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, newAPIError(404, "movie log not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get movie log id=%d: %w", params.ID, err)
	}

	p := db.EditMovieLogParams{
		MovieID:                l.MovieID,
		Date:                   l.Date,
		CinemaID:               l.CinemaID,
		Rating:                 l.Rating,
		Review:                 l.Review,
		ReviewContainsSpoilers: l.ReviewContainsSpoilers,
		ID:                     l.ID,
	}

	if req.MovieID.IsSet() {
		if req.MovieID.Value == 0 {
			return nil, newAPIError(400, "movie_id must be a non-null integer")
		}
		p.MovieID = req.MovieID.Value
	}
	if req.Date.IsSet() {
		p.Date = toNullInt64(req.Date)
	}
	if req.CinemaID.IsSet() {
		p.CinemaID = toNullInt64(req.CinemaID)
	}
	if req.Rating.IsSet() {
		p.Rating = toNullFloat64(req.Rating)
	}
	if req.Review.IsSet() {
		p.Review = toNullString(req.Review)
	}
	if req.ReviewContainsSpoilers.IsSet() {
		p.ReviewContainsSpoilers = req.ReviewContainsSpoilers.Value
	}

	updated, err := h.DB.EditMovieLog(ctx, p)
	if isConstraintViolation(err) {
		return nil, newAPIError(409, "cinema_id does not reference an existing cinema")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update movie log id=%d: %w", params.ID, err)
	}
	res := movieLogToOAS(updated)
	return &res, nil
}

// DeleteMovieLog implements deleteMovieLog operation.
func (h *APIHandler) DeleteMovieLog(ctx context.Context, params oas.DeleteMovieLogParams) (oas.DeleteMovieLogRes, error) {
	if err := h.DB.DeleteMovieLog(ctx, params.ID); err != nil {
		return nil, fmt.Errorf("failed to delete movie log id=%d: %w", params.ID, err)
	}
	return &oas.DeleteMovieLogNoContent{}, nil
}

// QuerySql implements querySql operation.
func (h *APIHandler) QuerySql(ctx context.Context, req *oas.QueryRequest) (oas.QuerySqlRes, error) {
	// Validate input
	if req.SQL == "" {
		return nil, newAPIError(400, "sql query is required")
	}

	// Set pagination defaults
	page := req.Page.Value
	if !req.Page.IsSet() || page < 1 {
		page = 1
	}

	pageSize := req.PageSize.Value
	if !req.PageSize.IsSet() || pageSize < 1 {
		pageSize = 50
	}
	if pageSize > 1000 {
		pageSize = 1000
	}

	// Create context with timeout
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	startTime := time.Now()

	// Execute count query first to get total
	// Wrap the query in a subquery to safely count results
	countQuery := fmt.Sprintf("SELECT COUNT(*) as count FROM (%s) as temp_count", req.SQL)
	var totalRows int64
	err := h.DBConn.QueryRowContext(queryCtx, countQuery).Scan(&totalRows)
	if err != nil {
		slog.WarnContext(ctx, "failed to count SQL query rows",
			slog.String("query", req.SQL),
			slog.String("error", err.Error()),
			slog.Duration("duration", time.Since(startTime)),
		)
		return nil, newAPIError(500, fmt.Sprintf("query failed: %v", err))
	}

	// Calculate pagination
	totalPages := int64(math.Ceil(float64(totalRows) / float64(pageSize)))
	offset := (page - 1) * pageSize

	// Execute main query with LIMIT and OFFSET - wrap in subquery to ensure clean pagination
	limitQuery := fmt.Sprintf("SELECT * FROM (%s) as temp_query LIMIT %d OFFSET %d", req.SQL, pageSize, offset)
	rows, err := h.DBConn.QueryContext(queryCtx, limitQuery)
	if err != nil {
		slog.WarnContext(ctx, "failed to execute SQL query",
			slog.String("query", req.SQL),
			slog.String("error", err.Error()),
			slog.Duration("duration", time.Since(startTime)),
		)
		return nil, newAPIError(500, fmt.Sprintf("query failed: %v", err))
	}
	defer rows.Close()

	// Get column names
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	// Convert rows to JSON-serializable format
	var resultRows []oas.QueryResponseRowsItem
	for rows.Next() {
		// Create a slice to hold the values
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		// Build map for this row with jx.Raw values
		rowMap := oas.QueryResponseRowsItem{}
		for i, col := range columns {
			val := values[i]

			// Convert value to JSON and then to jx.Raw
			var jsonBytes []byte
			if val == nil {
				jsonBytes = []byte("null")
			} else if b, ok := val.([]byte); ok {
				// String value
				jsonBytes, _ = json.Marshal(string(b))
			} else {
				// Other types (int, float, etc.)
				jsonBytes, _ = json.Marshal(val)
			}

			rowMap[col] = jx.Raw(jsonBytes)
		}
		resultRows = append(resultRows, rowMap)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("row iteration error: %w", err)
	}

	// Log the query execution
	slog.InfoContext(ctx, "executed SQL query",
		slog.String("query", req.SQL),
		slog.Int64("total_rows", totalRows),
		slog.Int64("page", page),
		slog.Int64("page_size", pageSize),
		slog.Int64("returned_rows", int64(len(resultRows))),
		slog.Duration("duration", time.Since(startTime)),
	)

	res := oas.QueryResponse{
		Rows:       resultRows,
		Total:      totalRows,
		Page:       page,
		PageSize:   pageSize,
		TotalPages: totalPages,
	}

	return &res, nil
}
