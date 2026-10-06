package domain

import (
	"sort"
	"time"
)

type ProcessConfigState string

const (
	ProcessConfigActive  ProcessConfigState = "active"
	ProcessConfigRetired ProcessConfigState = "retired"
)

// ProcessProfile carries the temperature, duration, and observation thresholds
// that differ between seasons and styles. It is embedded verbatim into a
// published revision so later publishes can never silently rewrite the
// standards a run was started against.
type ProcessProfile struct {
	MinFermentationTempC float64 `json:"min_fermentation_temp_c"`
	MaxFermentationTempC float64 `json:"max_fermentation_temp_c"`
	MinObservationTempC  float64 `json:"min_observation_temp_c"`
	MaxObservationTempC  float64 `json:"max_observation_temp_c"`
	MinimumDays          int     `json:"minimum_days"`
	MaximumDays          int     `json:"maximum_days"`
	// MinConditioningObservations / MinCompletionObservations replace the
	// hard-coded two/three observation gates for runs bound to a profile.
	MinConditioningObservations int `json:"min_conditioning_observations"`
	MinCompletionObservations   int `json:"min_completion_observations"`
	// MaxGravityRise is the largest permitted gravity increase against the
	// immediately preceding observation.
	MaxGravityRise float64 `json:"max_gravity_rise"`
	// FinalGravityTolerance is the advisory tolerance around the formula
	// target final gravity evaluated at completion.
	FinalGravityTolerance float64 `json:"final_gravity_tolerance"`
}

func (p ProcessProfile) Validate() error {
	if p.MinFermentationTempC < -5 || p.MinFermentationTempC > 60 {
		return FieldError(CodeInvalidInput, "min_fermentation_temp_c",
			"must be between -5 and 60 Celsius")
	}
	if p.MaxFermentationTempC < -5 || p.MaxFermentationTempC > 60 {
		return FieldError(CodeInvalidInput, "max_fermentation_temp_c",
			"must be between -5 and 60 Celsius")
	}
	if p.MinFermentationTempC > p.MaxFermentationTempC {
		return FieldError(CodeInvalidInput, "max_fermentation_temp_c",
			"must not be below the minimum fermentation temperature")
	}
	if p.MinObservationTempC < -5 || p.MinObservationTempC > 60 {
		return FieldError(CodeInvalidInput, "min_observation_temp_c",
			"must be between -5 and 60 Celsius")
	}
	if p.MaxObservationTempC < -5 || p.MaxObservationTempC > 60 {
		return FieldError(CodeInvalidInput, "max_observation_temp_c",
			"must be between -5 and 60 Celsius")
	}
	if p.MinObservationTempC > p.MinFermentationTempC ||
		p.MaxObservationTempC < p.MaxFermentationTempC {
		return FieldError(CodeInvalidInput, "observation temperature band",
			"must contain the fermentation temperature band")
	}
	if p.MinimumDays < 1 || p.MinimumDays > 120 {
		return FieldError(CodeInvalidInput, "minimum_days",
			"must be between 1 and 120")
	}
	if p.MaximumDays < p.MinimumDays || p.MaximumDays > 120 {
		return FieldError(CodeInvalidInput, "maximum_days",
			"must be between minimum days and 120")
	}
	if p.MinConditioningObservations < 1 || p.MinConditioningObservations > 500 {
		return FieldError(CodeInvalidInput, "min_conditioning_observations",
			"must be between 1 and 500")
	}
	if p.MinCompletionObservations < p.MinConditioningObservations ||
		p.MinCompletionObservations > 500 {
		return FieldError(CodeInvalidInput, "min_completion_observations",
			"must be between the conditioning minimum and 500")
	}
	if p.MaxGravityRise < 0 || p.MaxGravityRise > 0.1 {
		return FieldError(CodeInvalidInput, "max_gravity_rise",
			"must be between 0 and 0.100")
	}
	if p.FinalGravityTolerance < 0 || p.FinalGravityTolerance > 0.05 {
		return FieldError(CodeInvalidInput, "final_gravity_tolerance",
			"must be between 0 and 0.050")
	}
	return nil
}

// ProcessConfigRevision is immutable once published apart from withdrawal,
// which only marks the revision as ineligible for future starts.
type ProcessConfigRevision struct {
	Revision         int64          `json:"revision"`
	Profile          ProcessProfile `json:"profile"`
	EffectiveAt      time.Time      `json:"effective_at"`
	PublishedBy      string         `json:"published_by"`
	PublishedAt      time.Time      `json:"published_at"`
	WithdrawnAt      *time.Time     `json:"withdrawn_at,omitempty"`
	WithdrawalReason string         `json:"withdrawal_reason,omitempty"`
}

func (r ProcessConfigRevision) Withdrawn(now time.Time) bool {
	return r.WithdrawnAt != nil && !r.WithdrawnAt.After(now.UTC())
}

func (r ProcessConfigRevision) Clone() ProcessConfigRevision {
	cloned := r
	if r.WithdrawnAt != nil {
		value := *r.WithdrawnAt
		cloned.WithdrawnAt = &value
	}
	return cloned
}

type ProcessConfig struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Season    string                  `json:"season,omitempty"`
	Style     string                  `json:"style,omitempty"`
	Notes     string                  `json:"notes,omitempty"`
	State     ProcessConfigState      `json:"state"`
	Revisions []ProcessConfigRevision `json:"revisions"`
	Version   int64                   `json:"version"`
	CreatedAt time.Time               `json:"created_at"`
	UpdatedAt time.Time               `json:"updated_at"`
	RetiredAt *time.Time              `json:"retired_at,omitempty"`
}

func (c ProcessConfig) Clone() ProcessConfig {
	cloned := c
	cloned.Revisions = append([]ProcessConfigRevision(nil), c.Revisions...)
	for index := range cloned.Revisions {
		cloned.Revisions[index] = c.Revisions[index].Clone()
	}
	if c.RetiredAt != nil {
		value := *c.RetiredAt
		cloned.RetiredAt = &value
	}
	return cloned
}

func (c *ProcessConfig) Normalize() {
	c.Name = NormalizeName(c.Name)
	c.Season = NormalizeName(c.Season)
	c.Style = NormalizeName(c.Style)
	c.Notes = NormalizeName(c.Notes)
}

func (c ProcessConfig) Validate() error {
	if c.Name == "" {
		return FieldError(CodeInvalidInput, "name", "is required")
	}
	if len([]rune(c.Name)) > 120 {
		return FieldError(CodeInvalidInput, "name", "must not exceed 120 characters")
	}
	if len([]rune(c.Season)) > 40 {
		return FieldError(CodeInvalidInput, "season", "must not exceed 40 characters")
	}
	if len([]rune(c.Style)) > 80 {
		return FieldError(CodeInvalidInput, "style", "must not exceed 80 characters")
	}
	if len([]rune(c.Notes)) > 1000 {
		return FieldError(CodeInvalidInput, "notes", "must not exceed 1000 characters")
	}
	return nil
}

// HasActiveRevision reports whether the series has any revision that can be
// selected by a run starting at now.
func (c ProcessConfig) HasActiveRevision(now time.Time) bool {
	for _, revision := range c.Revisions {
		if !revision.Withdrawn(now) {
			return true
		}
	}
	return false
}

// Publish appends an immutable revision. The revision number is assigned from
// the current revision count, so callers must hold the serialized snapshot
// lock (service use-cases do). Effective times must be unique within a series
// so the "latest effective at time T" rule never has an ambiguous winner.
func (c *ProcessConfig) Publish(
	profile ProcessProfile,
	effectiveAt time.Time,
	publishedBy string,
	now time.Time,
) (ProcessConfigRevision, error) {
	if c.State != ProcessConfigActive {
		return ProcessConfigRevision{}, NewError(CodeProcessConfigState,
			"only an active process configuration can publish revisions")
	}
	if err := profile.Validate(); err != nil {
		return ProcessConfigRevision{}, err
	}
	effective := effectiveAt.UTC()
	for _, existing := range c.Revisions {
		if existing.EffectiveAt.Equal(effective) {
			return ProcessConfigRevision{}, NewError(CodeProcessConfigRevision,
				"a revision with the same effective time already exists")
		}
	}
	stamp := now.UTC()
	revision := ProcessConfigRevision{
		Revision:    int64(len(c.Revisions) + 1),
		Profile:     profile,
		EffectiveAt: effective,
		PublishedBy: NormalizeName(publishedBy),
		PublishedAt: stamp,
	}
	c.Revisions = append(c.Revisions, revision)
	c.UpdatedAt = stamp
	c.Version++
	return revision.Clone(), nil
}

// WithdrawRevision marks a published revision as ineligible for future starts.
// Runs already bound to the revision keep their snapshotted profile.
func (c *ProcessConfig) WithdrawRevision(
	revisionNumber int64,
	reason string,
	now time.Time,
) (ProcessConfigRevision, error) {
	if revisionNumber < 1 || int(revisionNumber) > len(c.Revisions) {
		return ProcessConfigRevision{}, NewError(CodeProcessConfigRevNotFound,
			"process configuration revision was not found")
	}
	index := int(revisionNumber - 1)
	if c.Revisions[index].Withdrawn(now) {
		return ProcessConfigRevision{}, NewError(CodeProcessConfigRevision,
			"process configuration revision is already withdrawn")
	}
	stamp := now.UTC()
	revision := c.Revisions[index].Clone()
	revision.WithdrawnAt = &stamp
	revision.WithdrawalReason = NormalizeName(reason)
	c.Revisions[index] = revision
	c.UpdatedAt = stamp
	c.Version++
	return revision.Clone(), nil
}

func (c *ProcessConfig) Retire(now time.Time) error {
	if c.State != ProcessConfigActive {
		return NewError(CodeProcessConfigState,
			"only an active process configuration can be retired")
	}
	stamp := now.UTC()
	c.State = ProcessConfigRetired
	c.RetiredAt = &stamp
	c.UpdatedAt = stamp
	c.Version++
	return nil
}

// RevisionCandidate explains why a published revision was or was not selected
// when resolving the profile for a run start.
type RevisionCandidate struct {
	Revision    int64     `json:"revision"`
	EffectiveAt time.Time `json:"effective_at"`
	Withdrawn   bool      `json:"withdrawn"`
	Selected    bool      `json:"selected"`
	// Status is a stable machine token: selected, withdrawn, not_yet_effective,
	// or superseded.
	Status string `json:"status"`
	Reason string `json:"reason"`
}

const (
	CandidateSelected         = "selected"
	CandidateWithdrawn        = "withdrawn"
	CandidateNotYetEffective  = "not_yet_effective"
	CandidateSuperseded       = "superseded"
	ResolutionLatestEffective = "latest_effective_at_start"
)

// ResolveEffective selects the revision that governs a run starting at now:
// the published, non-withdrawn revision with the greatest effective_at not in
// the future of now, breaking ties by highest revision number. Every published
// revision is returned as a candidate with an explanation, including
// future-dated and withdrawn ones.
func ResolveEffective(
	config ProcessConfig,
	now time.Time,
) (ProcessConfigRevision, []RevisionCandidate, error) {
	stamp := now.UTC()
	eligible := make([]ProcessConfigRevision, 0, len(config.Revisions))
	for _, revision := range config.Revisions {
		if !revision.Withdrawn(stamp) && !revision.EffectiveAt.After(stamp) {
			eligible = append(eligible, revision)
		}
	}
	if len(eligible) == 0 {
		return ProcessConfigRevision{}, buildCandidates(config, -1, stamp),
			NewError(CodeProcessConfigNotEffective,
				"no published process configuration revision is effective at run start")
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].EffectiveAt.Equal(eligible[j].EffectiveAt) {
			return eligible[i].Revision > eligible[j].Revision
		}
		return eligible[i].EffectiveAt.After(eligible[j].EffectiveAt)
	})
	chosen := eligible[0]
	candidates := buildCandidates(config, chosen.Revision, stamp)
	return chosen.Clone(), candidates, nil
}

func buildCandidates(
	config ProcessConfig,
	selectedRevision int64,
	now time.Time,
) []RevisionCandidate {
	candidates := make([]RevisionCandidate, 0, len(config.Revisions))
	for _, revision := range config.Revisions {
		candidate := RevisionCandidate{
			Revision:    revision.Revision,
			EffectiveAt: revision.EffectiveAt,
			Withdrawn:   revision.Withdrawn(now),
		}
		switch {
		case revision.Revision == selectedRevision:
			candidate.Selected = true
			candidate.Status = CandidateSelected
			candidate.Reason = "greatest effective_at at or before run start"
		case revision.Withdrawn(now):
			candidate.Status = CandidateWithdrawn
			candidate.Reason = "revision was withdrawn and cannot govern new runs"
		case revision.EffectiveAt.After(now):
			candidate.Status = CandidateNotYetEffective
			candidate.Reason = "effective_at is in the future at run start"
		default:
			candidate.Status = CandidateSuperseded
			candidate.Reason = "an older effective revision was superseded by a newer one"
		}
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].Revision < candidates[j].Revision
	})
	return candidates
}
