package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/service"
)

type createProcessConfigRequest struct {
	Name   string `json:"name"`
	Season string `json:"season"`
	Style  string `json:"style"`
	Notes  string `json:"notes"`
}

type publishRevisionRequest struct {
	Profile     domain.ProcessProfile `json:"profile"`
	EffectiveAt time.Time             `json:"effective_at"`
	PublishedBy string                `json:"published_by"`
}

type withdrawRevisionRequest struct {
	Reason string `json:"reason"`
}

func registerProcessConfigRoutes(mux *http.ServeMux, app *service.Service) {
	mux.HandleFunc("POST /v1/process-configs", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[createProcessConfigRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		config, err := app.CreateProcessConfig(service.CreateProcessConfigInput{
			Name:   body.Name,
			Season: body.Season,
			Style:  body.Style,
			Notes:  body.Notes,
		})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, config)
	})

	mux.HandleFunc("GET /v1/process-configs", func(response http.ResponseWriter, _ *http.Request) {
		configs, err := app.ListProcessConfigs()
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, map[string]any{"process_configs": configs})
	})

	mux.HandleFunc("GET /v1/process-configs/{id}", func(response http.ResponseWriter, request *http.Request) {
		config, err := app.GetProcessConfig(request.PathValue("id"))
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, config)
	})

	mux.HandleFunc("POST /v1/process-configs/{id}/revisions", func(response http.ResponseWriter, request *http.Request) {
		body, err := decodeJSON[publishRevisionRequest](response, request)
		if !handleDecode(response, err) {
			return
		}
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		if body.EffectiveAt.IsZero() {
			writeDomainError(response, domain.FieldError(domain.CodeInvalidInput,
				"effective_at", "is required and must be an RFC3339 timestamp"))
			return
		}
		config, revision, err := app.PublishRevision(request.PathValue("id"),
			service.PublishRevisionInput{
				ExpectedVersion: expected,
				Profile:         body.Profile,
				EffectiveAt:     body.EffectiveAt,
				PublishedBy:     body.PublishedBy,
			})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusCreated, map[string]any{
			"process_config": config,
			"revision":       revision,
		})
	})

	mux.HandleFunc("POST /v1/process-configs/{id}/revisions/{revision}/withdraw",
		func(response http.ResponseWriter, request *http.Request) {
			revisionNumber, err := strconv.ParseInt(request.PathValue("revision"), 10, 64)
			if err != nil || revisionNumber < 1 {
				writeDomainError(response, domain.FieldError(domain.CodeInvalidInput,
					"revision", "must be a positive integer"))
				return
			}
			body, decodeErr := decodeJSON[withdrawRevisionRequest](response, request)
			if !handleDecode(response, decodeErr) {
				return
			}
			expected, err := expectedVersion(request)
			if !handleDecode(response, err) {
				return
			}
			config, err := app.WithdrawRevision(request.PathValue("id"), revisionNumber,
				service.WithdrawRevisionInput{
					ExpectedVersion: expected,
					Reason:          body.Reason,
				})
			if err != nil {
				writeDomainError(response, err)
				return
			}
			writeJSON(response, http.StatusOK, config)
		})

	mux.HandleFunc("POST /v1/process-configs/{id}/retire", func(response http.ResponseWriter, request *http.Request) {
		expected, err := expectedVersion(request)
		if !handleDecode(response, err) {
			return
		}
		config, err := app.RetireProcessConfig(request.PathValue("id"),
			service.VersionInput{ExpectedVersion: expected})
		if err != nil {
			writeDomainError(response, err)
			return
		}
		writeJSON(response, http.StatusOK, config)
	})
}
