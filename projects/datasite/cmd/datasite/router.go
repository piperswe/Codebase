package main

import (
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"

	"codebase.bid/projects/datasite/internal/controllers"
	"codebase.bid/projects/datasite/internal/oas"
	"codebase.bid/projects/datasite/internal/static"
	"github.com/go-chi/chi/v5"
)

//go:embed openapi.yaml
var openAPISpec []byte

// withChiRoutePattern exposes Chi's low-cardinality route template through the
// standard Request.Pattern field consumed by the outer HTTP instrumentation.
func withChiRoutePattern(router chi.Router) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		routeContext := chi.NewRouteContext()
		if router.Match(routeContext, r.Method, r.URL.Path) {
			r.Pattern = routeContext.RoutePattern()
		}
		router.ServeHTTP(w, r)
	})
}

func SetupMux(u *universe) chi.Router {
	r := chi.NewRouter()
	r.Use(func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Add("Server-Src", u.serverSrc)
			h.ServeHTTP(w, r)
		})
	})
	r.Method(http.MethodGet, "/", &controllers.HomeController{
		ServerSrc: u.serverSrc,
		DB:        u.db,
		Movies:    u.movies,
	})
	movieLog := &controllers.MovieLogController{
		ServerSrc: u.serverSrc,
		DB:        u.db,
		Movies:    u.movies,
	}
	r.Method(http.MethodGet, "/movielog/{id}", movieLog.Get())
	r.Route("/admin", func(r chi.Router) {
		login := controllers.LoginController{
			ServerSrc: u.serverSrc,
		}
		r.Use(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/admin/login" {
					c, err := r.Cookie("datasite-admin-api-key")
					if err != nil {
						login.Get().ServeHTTP(w, r)
						return
					}
					if c.Value != u.adminAPIKey {
						login.Get().ServeHTTP(w, r)
						return
					}
				}
				h.ServeHTTP(w, r)
			})
		})
		r.Method(http.MethodGet, "/", (&controllers.AdminDashController{ServerSrc: u.serverSrc, DBConn: u.dbConn}).Get())
		r.Method(http.MethodPost, "/login", login.Post())
		cinemas := controllers.AdminCinemasController{
			ServerSrc: u.serverSrc,
			DB:        u.db,
		}
		r.Method(http.MethodGet, "/cinemas", cinemas.List())
		r.Method(http.MethodGet, "/cinemas/new", cinemas.GetCreate())
		r.Method(http.MethodPost, "/cinemas", cinemas.Create())
		r.Method(http.MethodGet, "/cinemas/{id}", cinemas.Get())
		r.Method(http.MethodPost, "/cinemas/{id}", cinemas.Post())
		r.Method(http.MethodPost, "/cinemas/{id}/delete", cinemas.Delete())
		movieLogs := controllers.AdminMovieLogsController{
			ServerSrc: u.serverSrc,
			DB:        u.db,
			Movies:    u.movies,
		}
		r.Method(http.MethodGet, "/movie_logs", movieLogs.List())
		r.Method(http.MethodGet, "/movie_logs/new", movieLogs.GetCreate())
		r.Method(http.MethodPost, "/movie_logs", movieLogs.Create())
		r.Method(http.MethodGet, "/movie_logs/{id}", movieLogs.Get())
		r.Method(http.MethodPost, "/movie_logs/{id}", movieLogs.Post())
		r.Method(http.MethodPost, "/movie_logs/{id}/delete", movieLogs.Delete())
		imp := controllers.AdminImportController{
			ServerSrc: u.serverSrc,
			DB:        u.db,
			Movies:    u.movies,
		}
		r.Method(http.MethodGet, "/import", imp.Get())
		r.Method(http.MethodPost, "/import", imp.Post())
	})
	r.Route("/api/v1", func(api chi.Router) {
		// Middleware to inject API key cookie into Authorization header
		api.Use(func(h http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") == "" {
					c, err := r.Cookie("datasite-admin-api-key")
					if err == nil {
						r.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.Value))
					}
				}
				h.ServeHTTP(w, r)
			})
		})
		api.Get("/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
			_, _ = w.Write(openAPISpec)
		})
		apiHandler := &controllers.APIHandler{
			DB:          u.db,
			DBConn:      u.dbConn,
			AdminAPIKey: u.adminAPIKey,
		}
		server, err := oas.NewServer(apiHandler, apiHandler, oas.WithPathPrefix("/api/v1"))
		if err != nil {
			u.logger.Error("failed to create ogen server", slog.Any("err", err))
			panic(err)
		}
		api.Mount("/", server)
	})
	r.Mount("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Cache-Control", "max-age=3600")
		http.FileServerFS(static.Static).ServeHTTP(w, r)
	}))
	return r
}
