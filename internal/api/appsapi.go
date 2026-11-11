package api

import (
	"context"
	"errors"
	"net/http"

	"flock/internal/apps"
	"flock/internal/config"
)

// AppsDatabases maps daemon services into the database descriptors apps.Create expects.
func AppsDatabases(services []Service) []apps.Database {
	out := make([]apps.Database, len(services))
	for i, s := range services {
		out[i] = apps.Database{
			ID: s.ID, Name: s.Name, Version: s.Version, Port: s.Port,
			Installed: s.Installed, Running: s.Running,
		}
	}
	return out
}

func registerApps(mux *http.ServeMux, b Backend) {
	mux.HandleFunc("GET /v1/apps/templates", func(w http.ResponseWriter, _ *http.Request) {
		templates, err := apps.Load(b.Root())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, struct {
			Templates []apps.Template `json:"templates"`
		}{Templates: templates})
	})

	mux.HandleFunc("POST /v1/apps", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name      string `json:"name"`
			ParentDir string `json:"parent_dir"`
			Type      string `json:"type"`
			DB        bool   `json:"db"`
			DBService string `json:"db_service"`
			PHP       string `json:"php"`
			Secured   bool   `json:"secured"`
		}
		if err := decodeBody(r, &body); err != nil {
			writeError(w, statusFor(err), err)
			return
		}

		_, err := apps.Create(r.Context(), apps.CreateRequest{
			Root: b.Root(), Name: body.Name, ParentDir: body.ParentDir, Type: body.Type,
			DB: body.DB, DBService: body.DBService, PHP: body.PHP, Secured: body.Secured,
			Services: func(context.Context) ([]apps.Database, error) {
				return AppsDatabases(b.Services()), nil
			},
			SaveSite: func(site config.Site) error {
				return b.Mutate(func(live *config.Config) error {
					if _, ok := live.FindSite(site.Name); ok {
						return invalid("a site named %q already exists; choose another project name", site.Name)
					}
					if err := live.Upsert(site); err != nil {
						return invalid("%v", err)
					}
					return nil
				})
			},
		})
		if err != nil {
			switch {
			case errors.Is(err, apps.ErrInvalid):
				writeError(w, http.StatusBadRequest, err)
			case errors.Is(err, apps.ErrDownload):
				writeError(w, http.StatusBadGateway, err)
			default:
				writeError(w, statusFor(err), err)
			}
			return
		}

		writeJSON(w, http.StatusOK, b.Status())
	})
}
