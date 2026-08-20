CREATE TABLE cinemas (
    id INTEGER PRIMARY KEY NOT NULL,
    name TEXT NOT NULL
);

CREATE TABLE movie_logs (
    id INTEGER PRIMARY KEY NOT NULL,
    movie_id INTEGER NOT NULL,
    date INTEGER, -- format: YYYYMMDD; allows for sorting numerically
    cinema_id INTEGER,
    rating REAL,
    review TEXT,
    review_contains_spoilers BOOLEAN NOT NULL,

    FOREIGN KEY(cinema_id) REFERENCES cinemas(id)
);

CREATE INDEX movie_logs_by_movie ON movie_logs(movie_id);
CREATE INDEX movie_logs_by_date ON movie_logs(date);
CREATE INDEX movie_logs_by_cinema ON movie_logs(cinema_id);
