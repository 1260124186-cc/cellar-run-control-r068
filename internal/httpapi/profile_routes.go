package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
)

type createProfileRequest struct {
	Name   string `json:"name"`
	Season string `json:"season"`
	Style  string `json:"style"`
}

type publishProfileVersionRequest struct {
	Expectations  domain.ProfileExpectations `json:"expectations"`
	EffectiveFrom time.Time                  `json:"effective_from"`
	Note          string                     `json:"note"`
}

func registerProfileRoutes(mux *http.ServeMux, app *service.Service) {
	mux.HandleFunc("POST /v1/profiles", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[createProfileRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		profile, err := app.CreateProfile(service.CreateProfileInput{
			Name:   body.Name,
			Season: body.Season,
			Style:  body.Style,
		})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, profile)
	})

	mux.HandleFunc("GET /v1/profiles", func(response http.ResponseWriter, _ *http.Request) {
		profiles, err := app.ListProfiles()
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"profiles": profiles})
	})

	mux.HandleFunc("GET /v1/profiles/{id}", func(response http.ResponseWriter, request *http.Request) {
		profile, err := app.GetProfile(request.PathValue("id"))
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, profile)
	})

	mux.HandleFunc("POST /v1/profiles/{id}/versions", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[publishProfileVersionRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		profile, err := app.PublishProfileVersion(request.PathValue("id"),
			service.PublishProfileVersionInput{
				ExpectedVersion: expected,
				Expectations:    body.Expectations,
				EffectiveFrom:   body.EffectiveFrom,
				Note:            body.Note,
			})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, profile)
	})

	mux.HandleFunc("POST /v1/profiles/{id}/versions/{version}/withdraw",
		func(response http.ResponseWriter, request *http.Request) {
			version, err := strconv.ParseInt(request.PathValue("version"), 10, 64)
			if err != nil || version < 1 {
				writeDomainError(response, domain.FieldError(domain.CodeInvalidInput,
					"version", "must be a positive integer"))
				return
			}
			expected, err := expectedVersion(request)
			if !handleDecode(response, err) {
				return
			}
			profile, err := app.WithdrawProfileVersion(request.PathValue("id"), version,
				service.VersionInput{ExpectedVersion: expected})
			if err != nil {
				writeDomainError(response, err)
				return
			}
			writeJSON(response, http.StatusOK, profile)
		})
}
