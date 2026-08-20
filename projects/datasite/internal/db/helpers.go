package db

import "fmt"

func (l *MovieLog) URL() string {
	return fmt.Sprintf("/movielog/%d", l.ID)
}

func (l *MovieLog) Year() int {
	if !l.Date.Valid {
		return 0
	}
	return int(l.Date.Int64 / 100 / 100)
}

func (l *MovieLog) Month() int {
	if !l.Date.Valid {
		return 0
	}
	return int((l.Date.Int64 / 100) % 100)
}

func (l *MovieLog) Day() int {
	if !l.Date.Valid {
		return 0
	}
	return int(l.Date.Int64 % 100)
}
