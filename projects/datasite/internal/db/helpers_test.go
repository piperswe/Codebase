package db

import (
	"database/sql"
	"testing"
)

func TestMovieLogDateParts(t *testing.T) {
	tests := []struct {
		name             string
		date             int64
		year, month, day int
	}{
		{"two-digit month and day", 20261123, 2026, 11, 23},
		{"single-digit month and day", 20260702, 2026, 7, 2},
		{"first day of year", 20250101, 2025, 1, 1},
		{"last day of year", 20241231, 2024, 12, 31},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := MovieLog{Date: sql.NullInt64{Int64: tt.date, Valid: true}}
			if got := l.Year(); got != tt.year {
				t.Errorf("Year() = %d, want %d", got, tt.year)
			}
			if got := l.Month(); got != tt.month {
				t.Errorf("Month() = %d, want %d", got, tt.month)
			}
			if got := l.Day(); got != tt.day {
				t.Errorf("Day() = %d, want %d", got, tt.day)
			}
		})
	}
}

func TestMovieLogDatePartsNullDate(t *testing.T) {
	l := MovieLog{}
	if got := l.Year(); got != 0 {
		t.Errorf("Year() = %d, want 0", got)
	}
	if got := l.Month(); got != 0 {
		t.Errorf("Month() = %d, want 0", got)
	}
	if got := l.Day(); got != 0 {
		t.Errorf("Day() = %d, want 0", got)
	}
}
