package controllers

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/piperswe/Codebase/projects/datasite/internal/views"
)

type AdminDashController struct {
	ServerSrc string
	DBConn    *sql.DB
}

func (c *AdminDashController) Get() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		vm := views.AdminDashViewModel{
			ServerSrc: c.ServerSrc,
		}

		// If query params are present, execute query
		sql := r.URL.Query().Get("sql")
		if sql != "" {
			page, _ := strconv.ParseInt(r.URL.Query().Get("page"), 10, 64)
			if page < 1 {
				page = 1
			}
			pageSize, _ := strconv.ParseInt(r.URL.Query().Get("page_size"), 10, 64)
			if pageSize < 1 {
				pageSize = 50
			}
			if pageSize > 1000 {
				pageSize = 1000
			}

			result := c.executeQuery(ctx, sql, page, pageSize)
			vm.SQL = sql
			vm.PageSize = pageSize
			if result.err != nil {
				vm.Error = result.err.Error()
			} else {
				vm.Columns = result.columns
				vm.Rows = result.rows
				vm.Page = result.page
				vm.TotalRows = result.totalRows
				vm.TotalPages = result.totalPages
			}
		}

		v := views.AdminDash(vm)
		v.Render(ctx, w)
	})
}

type queryResult struct {
	columns    []string
	rows       [][]string
	page       int64
	totalRows  int64
	totalPages int64
	err        error
}

func (c *AdminDashController) executeQuery(ctx context.Context, sqlStr string, page, pageSize int64) queryResult {
	queryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	startTime := time.Now()

	countQuery := fmt.Sprintf("SELECT COUNT(*) as count FROM (%s) as temp_count", sqlStr)
	var totalRows int64
	err := c.DBConn.QueryRowContext(queryCtx, countQuery).Scan(&totalRows)
	if err != nil {
		slog.WarnContext(ctx, "failed to count SQL query rows",
			slog.String("query", sqlStr),
			slog.String("error", err.Error()),
			slog.Duration("duration", time.Since(startTime)),
		)
		return queryResult{err: fmt.Errorf("query failed: %w", err)}
	}

	totalPages := int64(math.Ceil(float64(totalRows) / float64(pageSize)))
	offset := (page - 1) * pageSize

	limitQuery := fmt.Sprintf("SELECT * FROM (%s) as temp_query LIMIT %d OFFSET %d", sqlStr, pageSize, offset)
	rows, err := c.DBConn.QueryContext(queryCtx, limitQuery)
	if err != nil {
		slog.WarnContext(ctx, "failed to execute SQL query",
			slog.String("query", sqlStr),
			slog.String("error", err.Error()),
			slog.Duration("duration", time.Since(startTime)),
		)
		return queryResult{err: fmt.Errorf("query failed: %w", err)}
	}
	defer rows.Close()

	columns, err := rows.Columns()
	if err != nil {
		return queryResult{err: fmt.Errorf("failed to get columns: %w", err)}
	}

	var resultRows [][]string
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range columns {
			valuePtrs[i] = &values[i]
		}

		if err := rows.Scan(valuePtrs...); err != nil {
			return queryResult{err: fmt.Errorf("failed to scan row: %w", err)}
		}

		row := make([]string, len(columns))
		for i, val := range values {
			if val == nil {
				row[i] = "(null)"
			} else if b, ok := val.([]byte); ok {
				row[i] = string(b)
			} else {
				row[i] = fmt.Sprintf("%v", val)
			}
		}
		resultRows = append(resultRows, row)
	}

	if err = rows.Err(); err != nil {
		return queryResult{err: fmt.Errorf("row iteration error: %w", err)}
	}

	slog.InfoContext(ctx, "executed SQL query",
		slog.String("query", sqlStr),
		slog.Int64("total_rows", totalRows),
		slog.Int64("page", page),
		slog.Int64("page_size", pageSize),
		slog.Int64("returned_rows", int64(len(resultRows))),
		slog.Duration("duration", time.Since(startTime)),
	)

	return queryResult{
		columns:    columns,
		rows:       resultRows,
		page:       page,
		totalRows:  totalRows,
		totalPages: totalPages,
	}
}
