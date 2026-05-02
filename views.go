package main

import (
	"embed"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed templates/*.html
var templateFS embed.FS

type viewRenderer struct {
	templates *template.Template
}

func newViewRenderer() (*viewRenderer, error) {
	funcs := template.FuncMap{
		"trimPrefix": strings.TrimPrefix,
		"since": func(t time.Time) string {
			if t.IsZero() {
				return "-"
			}
			d := time.Since(t)
			if d < time.Minute {
				return "just now"
			}
			if d < time.Hour {
				return plural(int(d.Minutes()), "minute")
			}
			if d < 24*time.Hour {
				return plural(int(d.Hours()), "hour")
			}
			return plural(int(d.Hours()/24), "day")
		},
	}
	tmpl, err := template.New("").Funcs(funcs).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	return &viewRenderer{templates: tmpl}, nil
}

func (v *viewRenderer) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := v.templates.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word + " ago"
	}
	return strconv.Itoa(n) + " " + word + "s ago"
}
