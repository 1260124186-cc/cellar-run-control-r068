package domain

import (
	"fmt"
	"time"
)

type RunState string

const (
	RunPlanned      RunState = "planned"
	RunReserved     RunState = "reserved"
	RunFermenting   RunState = "fermenting"
	RunConditioning RunState = "conditioning"
	RunCompleted    RunState = "completed"
	RunAborted      RunState = "aborted"
)

// Default observation thresholds for runs without a bound process profile,
// such as runs created before profiles existed.
const (
	DefaultMinConditioningObservations = 2
	DefaultMinCompletionObservations   = 3
)

type FermentationRun struct {
	ID               string                `json:"id"`
	Code             string                `json:"code"`
	FormulaID        string                `json:"formula_id"`
	VesselID         *string               `json:"vessel_id,omitempty"`
	PlannedVolumeL   float64               `json:"planned_volume_liters"`
	Notes            string                `json:"notes,omitempty"`
	State            RunState              `json:"state"`
	StartedAt        *time.Time            `json:"started_at,omitempty"`
	EndedAt          *time.Time            `json:"ended_at,omitempty"`
	Observations     []GravityObservation  `json:"observations"`
	Metrics          *RunMetrics           `json:"metrics,omitempty"`
	ProfileBinding   *ProfileBinding       `json:"profile_binding,omitempty"`
	ProfileDecisions []ProfileDecision     `json:"profile_decisions,omitempty"`
	Assessment       *CompletionAssessment `json:"assessment,omitempty"`
	Version          int64                 `json:"version"`
	CreatedAt        time.Time             `json:"created_at"`
	UpdatedAt        time.Time             `json:"updated_at"`
}

func (r FermentationRun) Clone() FermentationRun {
	cloned := r
	cloned.Observations = append([]GravityObservation(nil), r.Observations...)
	cloned.ProfileDecisions = append([]ProfileDecision(nil), r.ProfileDecisions...)
	if r.VesselID != nil {
		value := *r.VesselID
		cloned.VesselID = &value
	}
	if r.StartedAt != nil {
		value := *r.StartedAt
		cloned.StartedAt = &value
	}
	if r.EndedAt != nil {
		value := *r.EndedAt
		cloned.EndedAt = &value
	}
	if r.Metrics != nil {
		value := *r.Metrics
		cloned.Metrics = &value
	}
	if r.ProfileBinding != nil {
		value := *r.ProfileBinding
		cloned.ProfileBinding = &value
	}
	if r.Assessment != nil {
		value := *r.Assessment
		cloned.Assessment = &value
	}
	return cloned
}

func (r FermentationRun) Validate() error {
	if err := ValidateRunCode(r.Code, "code"); err != nil {
		return err
	}
	if r.PlannedVolumeL <= 0 {
		return FieldError(CodeInvalidInput, "planned_volume_liters",
			"must be greater than zero")
	}
	if len([]rune(r.Notes)) > 1000 {
		return FieldError(CodeInvalidInput, "notes",
			"must not exceed 1000 characters")
	}
	return nil
}

func (r *FermentationRun) ReserveVessel(vesselID string, now time.Time) error {
	if r.State != RunPlanned {
		return NewError(CodeRunState, "only a planned run can reserve a vessel")
	}
	value := vesselID
	r.VesselID = &value
	r.State = RunReserved
	r.UpdatedAt = now.UTC()
	r.Version++
	return nil
}

func (r *FermentationRun) StartFermentation(now time.Time) error {
	if r.State != RunReserved || r.VesselID == nil {
		return NewError(CodeRunState, "only a reserved run can start fermentation")
	}
	stamp := now.UTC()
	r.State = RunFermenting
	r.StartedAt = &stamp
	r.UpdatedAt = stamp
	r.Version++
	return nil
}

// BindProfile pins a resolved profile version onto a fermenting run. It does
// not bump the aggregate version; it is part of the start transition.
func (r *FermentationRun) BindProfile(binding ProfileBinding, now time.Time) error {
	if r.State != RunFermenting {
		return NewError(CodeRunState, "profile binding requires a fermenting run")
	}
	if r.ProfileBinding != nil {
		return NewError(CodeRunState, "run already has a bound process profile")
	}
	bound := binding
	r.ProfileBinding = &bound
	r.RecordProfileDecision(ProfileDecisionBoundAtStart, binding.Reason, now)
	return nil
}

// RecordProfileDecision appends an audit entry describing which bound
// version the run used and why. It is a no-op for runs without a binding.
func (r *FermentationRun) RecordProfileDecision(
	event ProfileDecisionEvent,
	reason string,
	now time.Time,
) {
	if r.ProfileBinding == nil {
		return
	}
	r.ProfileDecisions = append(r.ProfileDecisions, ProfileDecision{
		At:        now.UTC(),
		Event:     event,
		ProfileID: r.ProfileBinding.ProfileID,
		Version:   r.ProfileBinding.Version,
		Reason:    reason,
	})
}

func (r FermentationRun) MinConditioningObservations() int {
	if r.ProfileBinding != nil {
		return r.ProfileBinding.Expectations.MinConditioningObservations
	}
	return DefaultMinConditioningObservations
}

func (r FermentationRun) MinCompletionObservations() int {
	if r.ProfileBinding != nil {
		return r.ProfileBinding.Expectations.MinCompletionObservations
	}
	return DefaultMinCompletionObservations
}

func (r *FermentationRun) AddObservation(observation GravityObservation) error {
	if r.State != RunFermenting || r.StartedAt == nil {
		return NewError(CodeRunState, "observations require a fermenting run")
	}
	if len(r.Observations) >= 500 {
		return NewError(CodeObservationConflict,
			"run cannot contain more than 500 observations")
	}
	var previous *GravityObservation
	if len(r.Observations) > 0 {
		value := r.Observations[len(r.Observations)-1]
		previous = &value
	}
	if err := ValidateObservation(*r.StartedAt, previous, observation, observation.ReceivedAt); err != nil {
		return err
	}
	expected := len(r.Observations) + 1
	if observation.Sequence != expected {
		return NewError(CodeObservationConflict,
			"observation sequence must be contiguous and begin at 1")
	}
	observation.RunID = r.ID
	r.Observations = append(r.Observations, observation)
	r.UpdatedAt = observation.ReceivedAt.UTC()
	r.Version++
	return nil
}

func (r *FermentationRun) BeginConditioning(now time.Time) error {
	if r.State != RunFermenting {
		return NewError(CodeRunState, "only a fermenting run can enter conditioning")
	}
	required := r.MinConditioningObservations()
	if len(r.Observations) < required {
		return NewError(CodeRunState,
			fmt.Sprintf("conditioning requires at least %d observations", required))
	}
	r.State = RunConditioning
	r.UpdatedAt = now.UTC()
	r.Version++
	return nil
}

func (r *FermentationRun) Complete(originalGravity float64, now time.Time) error {
	if r.State != RunConditioning {
		return NewError(CodeRunState, "only a conditioning run can be completed")
	}
	required := r.MinCompletionObservations()
	if len(r.Observations) < required {
		return NewError(CodeRunState,
			fmt.Sprintf("completion requires at least %d observations", required))
	}
	stamp := now.UTC()
	final := r.Observations[len(r.Observations)-1]
	metrics := CalculateMetrics(originalGravity, final.Gravity, len(r.Observations))
	r.Metrics = &metrics
	if r.ProfileBinding != nil && r.StartedAt != nil {
		assessment := AssessCompletion(*r.ProfileBinding, *r.StartedAt, stamp, r.Observations)
		r.Assessment = &assessment
	}
	r.State = RunCompleted
	r.EndedAt = &stamp
	r.UpdatedAt = stamp
	r.Version++
	return nil
}

func (r *FermentationRun) Abort(now time.Time) error {
	if r.State == RunCompleted || r.State == RunAborted {
		return NewError(CodeRunState, "completed or aborted runs cannot be aborted")
	}
	stamp := now.UTC()
	r.State = RunAborted
	r.EndedAt = &stamp
	r.UpdatedAt = stamp
	r.Version++
	return nil
}

func (r FermentationRun) HadStarted() bool {
	return r.StartedAt != nil
}
