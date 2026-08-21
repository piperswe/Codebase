package controllers

import (
	"log/slog"
	"net/http"

	"github.com/piperswe/Codebase/projects/datasite/internal/views"
)

type LoginController struct {
	ServerSrc string
}

func (c *LoginController) Get() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		v := views.AdminLogin(views.AdminLoginViewModel{
			ServerSrc: c.ServerSrc,
		})
		v.Render(ctx, w)
	})
}

func (c *LoginController) Post() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		err := r.ParseForm()
		if err != nil {
			slog.ErrorContext(ctx, "failed to parse form body", slog.Any("err", err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		key := r.FormValue("key")
		http.SetCookie(w, &http.Cookie{
			Name:  "datasite-admin-api-key",
			Value: key,
			Path:  "/",
		})
		w.Header().Set("Location", "/admin")
		w.WriteHeader(http.StatusSeeOther)
	})
}
