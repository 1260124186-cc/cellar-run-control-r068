package workflowcheck

import (
	"fmt"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

type formulaView struct {
	ID      string               `json:"id"`
	State   domain.FormulaState  `json:"state"`
	Version int64                `json:"version"`
	Steps   []domain.FormulaStep `json:"steps"`
}

type vesselView struct {
	ID          string             `json:"id"`
	State       domain.VesselState `json:"state"`
	Version     int64              `json:"version"`
	ActiveRunID *string            `json:"active_run_id"`
}

type runView struct {
	ID              string                      `json:"id"`
	State           domain.RunState             `json:"state"`
	Version         int64                       `json:"version"`
	VesselID        *string                     `json:"vessel_id"`
	Observations    []domain.GravityObservation `json:"observations"`
	Metrics         *domain.RunMetrics          `json:"metrics"`
	ProcessConfigID string                      `json:"process_config_id"`
	ProcessBinding  *processBindingView         `json:"process_binding"`
	ProcessResult   *processResultView          `json:"process_result"`
	ProcessStatus   processStatusView           `json:"process_status"`
}

type processBindingView struct {
	ProcessConfigID   string                     `json:"process_config_id"`
	ProcessConfigName string                     `json:"process_config_name"`
	Revision          int64                      `json:"revision"`
	ResolutionReason  string                     `json:"resolution_reason"`
	Profile           domain.ProcessProfile      `json:"profile"`
	Candidates        []domain.RevisionCandidate `json:"candidates"`
}

type processResultView struct {
	FinalGravityWithinTolerance bool   `json:"final_gravity_within_tolerance"`
	DurationStatus              string `json:"duration_status"`
	BoundRevisionWithdrawn      bool   `json:"bound_revision_since_withdrawn"`
}

type processStatusView struct {
	Bound                  bool   `json:"bound"`
	Revision               int64  `json:"revision"`
	BoundRevisionWithdrawn bool   `json:"bound_revision_withdrawn"`
	ConfigSeriesRetired    bool   `json:"config_series_retired"`
	PendingConfigID        string `json:"pending_config_id"`
	PendingConfigName      string `json:"pending_config_name"`
	PendingEffective       bool   `json:"pending_effective_revision"`
	Summary                string `json:"summary"`
}

type processConfigView struct {
	ID        string                         `json:"id"`
	Name      string                         `json:"name"`
	State     domain.ProcessConfigState      `json:"state"`
	Version   int64                          `json:"version"`
	Revisions []domain.ProcessConfigRevision `json:"revisions"`
}

type processConfigEnvelope struct {
	ProcessConfig processConfigView            `json:"process_config"`
	Revision      domain.ProcessConfigRevision `json:"revision"`
}

type pairView struct {
	Run    runView    `json:"run"`
	Vessel vesselView `json:"vessel"`
}

func createFormulaPayload(name string) map[string]any {
	return map[string]any{
		"name":                    name,
		"style":                   "Cellar pilot",
		"target_original_gravity": 1.052,
		"target_final_gravity":    1.012,
		"max_fermentation_temp_c": 22.0,
		"minimum_days":            7,
		"maximum_days":            21,
	}
}

func formulaStepPayload() map[string]any {
	return map[string]any{
		"steps": []map[string]any{
			{
				"sequence":      1,
				"label":         "Heat and combine",
				"temperature_c": 66.0,
				"hold_minutes":  60,
			},
			{
				"sequence":      2,
				"label":         "Transfer and cool",
				"temperature_c": 20.0,
				"hold_minutes":  30,
			},
			{
				"sequence":      3,
				"label":         "Hold for primary activity",
				"temperature_c": 19.0,
				"hold_minutes":  240,
			},
		},
	}
}

func createRunPayload(code, formulaID string) map[string]any {
	return map[string]any{
		"code":                  code,
		"formula_id":            formulaID,
		"planned_volume_liters": 180.0,
		"notes":                 "pilot run",
	}
}

// standardProfile matches the historical fixed gates (2/3 observations,
// 0.010 gravity rise, 19-23 C observations, ~1.012 target) so the existing
// approval and observation flows stay valid under versioned standards.
func standardProfile() map[string]any {
	return map[string]any{
		"min_fermentation_temp_c":       18.0,
		"max_fermentation_temp_c":       24.0,
		"min_observation_temp_c":        15.0,
		"max_observation_temp_c":        26.0,
		"minimum_days":                  7,
		"maximum_days":                  21,
		"min_conditioning_observations": 2,
		"min_completion_observations":   3,
		"max_gravity_rise":              0.010,
		"final_gravity_tolerance":       0.005,
	}
}

func createProcessConfigPayload(name string) map[string]any {
	return map[string]any{
		"name":   name,
		"season": "All season",
		"style":  "Pilot",
		"notes":  "standard cellar profile",
	}
}

// ensureProcessConfig creates an active series with one revision effective
// immediately. Version assignments are deterministic for fresh check stores.
func ensureProcessConfig(api *client, name string) (processConfigView, error) {
	return ensureProcessConfigEffective(api, name, time.Now().UTC().Add(-time.Minute))
}

func ensureProcessConfigEffective(api *client, name string, effectiveAt time.Time) (processConfigView, error) {
	createdResponse, err := api.call("POST", "/v1/process-configs",
		createProcessConfigPayload(name), nil)
	if err != nil {
		return processConfigView{}, err
	}
	if err := requireStatus(createdResponse, 201, "create process config"); err != nil {
		return processConfigView{}, err
	}
	var config processConfigView
	if err := createdResponse.decode(&config); err != nil {
		return processConfigView{}, err
	}
	publishResponse, err := api.call("POST",
		"/v1/process-configs/"+config.ID+"/revisions",
		map[string]any{
			"profile":      standardProfile(),
			"effective_at": effectiveAt.Format(time.RFC3339),
			"published_by": "check-supervisor",
		}, ptrInt64(1))
	if err != nil {
		return processConfigView{}, err
	}
	if err := requireStatus(publishResponse, 201, "publish process config revision"); err != nil {
		return processConfigView{}, err
	}
	var envelope processConfigEnvelope
	if err := publishResponse.decode(&envelope); err != nil {
		return processConfigView{}, err
	}
	return envelope.ProcessConfig, nil
}

// approveFormulaWithConfig performs the steps/process-config/approve dance
// with the exact optimistic versions produced by a fresh store.
func approveFormulaWithConfig(api *client, formulaID, configID string) error {
	stepsResponse, err := api.call("PUT", "/v1/formulas/"+formulaID+"/steps",
		formulaStepPayload(), ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireStatus(stepsResponse, 200, "set setup steps"); err != nil {
		return err
	}
	bindResponse, err := api.call("PUT", "/v1/formulas/"+formulaID+"/process-config",
		map[string]any{"process_config_id": configID}, ptrInt64(2))
	if err != nil {
		return err
	}
	if err := requireStatus(bindResponse, 200, "bind process config"); err != nil {
		return err
	}
	approveResponse, err := api.call("POST", "/v1/formulas/"+formulaID+"/approve",
		nil, ptrInt64(3))
	if err != nil {
		return err
	}
	return requireStatus(approveResponse, 200, "approve setup formula")
}

func verifiedAt(base time.Time, sequence int) time.Time {
	return base.Add(time.Duration(sequence) * 10 * time.Second)
}

func requireRunState(run runView, state domain.RunState) error {
	if run.State != state {
		return fmt.Errorf("expected run state %q, got %q", state, run.State)
	}
	return nil
}

func requireVesselState(vessel vesselView, state domain.VesselState) error {
	if vessel.State != state {
		return fmt.Errorf("expected vessel state %q, got %q", state, vessel.State)
	}
	return nil
}
