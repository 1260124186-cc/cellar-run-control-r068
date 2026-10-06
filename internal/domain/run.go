package domain

import "time"

type RunState string

const (
	RunPlanned      RunState = "planned"
	RunReserved     RunState = "reserved"
	RunFermenting   RunState = "fermenting"
	RunConditioning RunState = "conditioning"
	RunCompleted    RunState = "completed"
	RunAborted      RunState = "aborted"
)

// RunProcessBinding is snapshotted onto the run at start. It captures the
// exact configuration revision that was resolved then, including the full
// threshold profile, so later publishes or withdrawals cannot change what a
// live or completed run is judged against.
type RunProcessBinding struct {
	ProcessConfigID   string              `json:"process_config_id"`
	ProcessConfigName string              `json:"process_config_name"`
	Revision          int64               `json:"revision"`
	EffectiveAt       time.Time           `json:"effective_at"`
	PublishedAt       time.Time           `json:"published_at"`
	ResolvedAt        time.Time           `json:"resolved_at"`
	ResolutionReason  string              `json:"resolution_reason"`
	Profile           ProcessProfile      `json:"profile"`
	Candidates        []RevisionCandidate `json:"candidates"`
	ExpectedEarliest  time.Time           `json:"expected_completion_earliest"`
	ExpectedLatest    time.Time           `json:"expected_completion_latest"`
}

// RunProcessResult records the completion-time evaluation performed against
// the bound revision, so every finished run shows why it passed.
type RunProcessResult struct {
	FinalGravity                float64 `json:"final_gravity"`
	TargetFinalGravity          float64 `json:"target_final_gravity"`
	FinalGravityTolerance       float64 `json:"final_gravity_tolerance"`
	FinalGravityWithinTolerance bool    `json:"final_gravity_within_tolerance"`
	ElapsedDays                 int     `json:"elapsed_days"`
	MinimumDays                 int     `json:"minimum_days"`
	MaximumDays                 int     `json:"maximum_days"`
	DurationStatus              string  `json:"duration_status"`
	ObservationCount            int     `json:"observation_count"`
	BoundRevisionWithdrawn      bool    `json:"bound_revision_since_withdrawn"`
}

const (
	DurationWithinWindow = "within_expected_window"
	DurationBeforeWindow = "before_expected_window"
	DurationAfterWindow  = "after_expected_window"
)

type FermentationRun struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	FormulaID string `json:"formula_id"`
	// ProcessConfigID is copied from the approved formula at creation so the
	// linkage survives even if the formula aggregate is later retired.
	ProcessConfigID string               `json:"process_config_id"`
	VesselID        *string              `json:"vessel_id,omitempty"`
	PlannedVolumeL  float64              `json:"planned_volume_liters"`
	Notes           string               `json:"notes,omitempty"`
	State           RunState             `json:"state"`
	StartedAt       *time.Time           `json:"started_at,omitempty"`
	EndedAt         *time.Time           `json:"ended_at,omitempty"`
	Observations    []GravityObservation `json:"observations"`
	Metrics         *RunMetrics          `json:"metrics,omitempty"`
	ProcessBinding  *RunProcessBinding   `json:"process_binding,omitempty"`
	ProcessResult   *RunProcessResult    `json:"process_result,omitempty"`
	Version         int64                `json:"version"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

func (r FermentationRun) Clone() FermentationRun {
	cloned := r
	cloned.Observations = append([]GravityObservation(nil), r.Observations...)
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
	if r.ProcessBinding != nil {
		binding := *r.ProcessBinding
		binding.Candidates = append([]RevisionCandidate(nil), r.ProcessBinding.Candidates...)
		cloned.ProcessBinding = &binding
	}
	if r.ProcessResult != nil {
		value := *r.ProcessResult
		cloned.ProcessResult = &value
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

// StartFermentation binds the resolved configuration revision before the run
// enters fermentation. Every later judgment reads the binding instead of the
// current configuration catalogue.
func (r *FermentationRun) StartFermentation(binding RunProcessBinding, now time.Time) error {
	if r.State != RunReserved || r.VesselID == nil {
		return NewError(CodeRunState, "only a reserved run can start fermentation")
	}
	if binding.ProcessConfigID == "" || binding.Revision < 1 {
		return NewError(CodeProcessConfigNotEffective,
			"a run requires an effective process configuration revision at start")
	}
	stamp := now.UTC()
	bound := binding
	bound.Candidates = append([]RevisionCandidate(nil), binding.Candidates...)
	bound.ResolvedAt = stamp
	bound.ExpectedEarliest = stamp.AddDate(0, 0, binding.Profile.MinimumDays)
	bound.ExpectedLatest = stamp.AddDate(0, 0, binding.Profile.MaximumDays)
	r.ProcessBinding = &bound
	r.State = RunFermenting
	r.StartedAt = &stamp
	r.UpdatedAt = stamp
	r.Version++
	return nil
}

// boundProfile returns the snapshotted profile for a run that has started.
// Started runs always carry a binding; the defensive default keeps older
// snapshots loadable without inventing configuration data.
func (r *FermentationRun) boundProfile() (ProcessProfile, error) {
	if r.ProcessBinding == nil {
		return ProcessProfile{}, NewError(CodeProcessConfigNotEffective,
			"started run has no bound process configuration")
	}
	return r.ProcessBinding.Profile, nil
}

func (r *FermentationRun) AddObservation(observation GravityObservation) error {
	if r.State != RunFermenting || r.StartedAt == nil {
		return NewError(CodeRunState, "observations require a fermenting run")
	}
	profile, err := r.boundProfile()
	if err != nil {
		return err
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
	if observation.TemperatureC < profile.MinObservationTempC ||
		observation.TemperatureC > profile.MaxObservationTempC {
		return FieldError(CodeInvalidInput, "temperature_c",
			"must stay within the bound process configuration observation temperature band")
	}
	if previous != nil && observation.Gravity-previous.Gravity > profile.MaxGravityRise {
		return NewError(CodeObservationConflict,
			"gravity rise exceeds the bound process configuration limit")
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
	profile, err := r.boundProfile()
	if err != nil {
		return err
	}
	if len(r.Observations) < profile.MinConditioningObservations {
		return NewError(CodeRunState,
			"conditioning requires the observation minimum defined by the bound process configuration")
	}
	r.State = RunConditioning
	r.UpdatedAt = now.UTC()
	r.Version++
	return nil
}

func (r *FermentationRun) Complete(
	originalGravity, targetFinalGravity float64,
	boundRevisionWithdrawn bool,
	now time.Time,
) error {
	if r.State != RunConditioning {
		return NewError(CodeRunState, "only a conditioning run can be completed")
	}
	profile, err := r.boundProfile()
	if err != nil {
		return err
	}
	if len(r.Observations) < profile.MinCompletionObservations {
		return NewError(CodeRunState,
			"completion requires the observation minimum defined by the bound process configuration")
	}
	stamp := now.UTC()
	final := r.Observations[len(r.Observations)-1]
	metrics := CalculateMetrics(originalGravity, final.Gravity, len(r.Observations))
	r.Metrics = &metrics

	difference := final.Gravity - targetFinalGravity
	if difference < 0 {
		difference = -difference
	}
	elapsedDays := int(stamp.Sub(r.StartedAt.UTC()).Hours() / 24)
	durationStatus := DurationWithinWindow
	if elapsedDays < profile.MinimumDays {
		durationStatus = DurationBeforeWindow
	} else if elapsedDays > profile.MaximumDays {
		durationStatus = DurationAfterWindow
	}
	r.ProcessResult = &RunProcessResult{
		FinalGravity:                round2(final.Gravity),
		TargetFinalGravity:          round2(targetFinalGravity),
		FinalGravityTolerance:       profile.FinalGravityTolerance,
		FinalGravityWithinTolerance: difference <= profile.FinalGravityTolerance+1e-9,
		ElapsedDays:                 elapsedDays,
		MinimumDays:                 profile.MinimumDays,
		MaximumDays:                 profile.MaximumDays,
		DurationStatus:              durationStatus,
		ObservationCount:            len(r.Observations),
		BoundRevisionWithdrawn:      boundRevisionWithdrawn,
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
