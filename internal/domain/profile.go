package domain

import (
	"fmt"
	"time"
)

type ProfileVersionState string

const (
	ProfileVersionPublished ProfileVersionState = "published"
	ProfileVersionWithdrawn ProfileVersionState = "withdrawn"
)

// ProfileExpectations describes the cellar standard a run is judged against:
// the expected fermentation temperature band, the expected fermentation
// duration, and the observation thresholds for conditioning and completion.
type ProfileExpectations struct {
	MinFermentationTempC        float64 `json:"min_fermentation_temp_c"`
	MaxFermentationTempC        float64 `json:"max_fermentation_temp_c"`
	MinFermentationDays         int     `json:"min_fermentation_days"`
	MaxFermentationDays         int     `json:"max_fermentation_days"`
	MinConditioningObservations int     `json:"min_conditioning_observations"`
	MinCompletionObservations   int     `json:"min_completion_observations"`
}

func (e ProfileExpectations) Validate() error {
	if e.MinFermentationTempC < -5 || e.MinFermentationTempC > 60 {
		return FieldError(CodeInvalidInput, "expectations.min_fermentation_temp_c",
			"must be between -5 and 60 Celsius")
	}
	if e.MaxFermentationTempC < e.MinFermentationTempC || e.MaxFermentationTempC > 60 {
		return FieldError(CodeInvalidInput, "expectations.max_fermentation_temp_c",
			"must be between the minimum and 60 Celsius")
	}
	if e.MinFermentationDays < 0 || e.MinFermentationDays > 120 {
		return FieldError(CodeInvalidInput, "expectations.min_fermentation_days",
			"must be between 0 and 120")
	}
	if e.MaxFermentationDays < 1 || e.MaxFermentationDays > 120 {
		return FieldError(CodeInvalidInput, "expectations.max_fermentation_days",
			"must be between 1 and 120")
	}
	if e.MaxFermentationDays < e.MinFermentationDays {
		return FieldError(CodeInvalidInput, "expectations.max_fermentation_days",
			"must not be less than minimum fermentation days")
	}
	if e.MinConditioningObservations < 1 || e.MinConditioningObservations > 500 {
		return FieldError(CodeInvalidInput, "expectations.min_conditioning_observations",
			"must be between 1 and 500")
	}
	if e.MinCompletionObservations < e.MinConditioningObservations ||
		e.MinCompletionObservations > 500 {
		return FieldError(CodeInvalidInput, "expectations.min_completion_observations",
			"must be between minimum conditioning observations and 500")
	}
	return nil
}

// ProfileVersion is one immutable published revision of a process profile.
// Its expectations never change after publication; withdrawal only blocks
// new bindings and never rewrites runs that already bound the version.
type ProfileVersion struct {
	Version       int64               `json:"version"`
	State         ProfileVersionState `json:"state"`
	Expectations  ProfileExpectations `json:"expectations"`
	EffectiveFrom time.Time           `json:"effective_from"`
	Note          string              `json:"note,omitempty"`
	PublishedAt   time.Time           `json:"published_at"`
	WithdrawnAt   *time.Time          `json:"withdrawn_at,omitempty"`
}

// CellarProfile is a versioned family of cellar process standards. The
// aggregate Version coordinates optimistic concurrency between supervisors
// publishing at the same time.
type CellarProfile struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Season    string           `json:"season"`
	Style     string           `json:"style"`
	Versions  []ProfileVersion `json:"versions"`
	Version   int64            `json:"version"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

func (p CellarProfile) Clone() CellarProfile {
	cloned := p
	cloned.Versions = make([]ProfileVersion, len(p.Versions))
	for index, version := range p.Versions {
		copied := version
		if version.WithdrawnAt != nil {
			stamp := *version.WithdrawnAt
			copied.WithdrawnAt = &stamp
		}
		cloned.Versions[index] = copied
	}
	return cloned
}

func (p *CellarProfile) Normalize() {
	p.Name = NormalizeName(p.Name)
	p.Season = NormalizeName(p.Season)
	p.Style = NormalizeName(p.Style)
}

func (p CellarProfile) Validate() error {
	if p.Name == "" {
		return FieldError(CodeInvalidInput, "name", "is required")
	}
	if len([]rune(p.Name)) > 120 {
		return FieldError(CodeInvalidInput, "name", "must not exceed 120 characters")
	}
	if p.Season == "" {
		return FieldError(CodeInvalidInput, "season", "is required")
	}
	if len([]rune(p.Season)) > 80 {
		return FieldError(CodeInvalidInput, "season", "must not exceed 80 characters")
	}
	if p.Style == "" {
		return FieldError(CodeInvalidInput, "style", "is required")
	}
	if len([]rune(p.Style)) > 80 {
		return FieldError(CodeInvalidInput, "style", "must not exceed 80 characters")
	}
	return nil
}

// Publish appends a new immutable version. A zero effectiveFrom means the
// version is effective immediately.
func (p *CellarProfile) Publish(
	expectations ProfileExpectations,
	effectiveFrom time.Time,
	note string,
	now time.Time,
) (ProfileVersion, error) {
	if err := expectations.Validate(); err != nil {
		return ProfileVersion{}, err
	}
	if len([]rune(note)) > 500 {
		return ProfileVersion{}, FieldError(CodeInvalidInput, "note",
			"must not exceed 500 characters")
	}
	stamp := now.UTC()
	effective := effectiveFrom.UTC()
	if effectiveFrom.IsZero() {
		effective = stamp
	}
	version := ProfileVersion{
		Version:       int64(len(p.Versions)) + 1,
		State:         ProfileVersionPublished,
		Expectations:  expectations,
		EffectiveFrom: effective,
		Note:          note,
		PublishedAt:   stamp,
	}
	p.Versions = append(p.Versions, version)
	p.UpdatedAt = stamp
	p.Version++
	return version, nil
}

func (p *CellarProfile) Withdraw(version int64, now time.Time) error {
	for index := range p.Versions {
		if p.Versions[index].Version != version {
			continue
		}
		if p.Versions[index].State == ProfileVersionWithdrawn {
			return NewError(CodeProfileState, "profile version is already withdrawn")
		}
		stamp := now.UTC()
		p.Versions[index].State = ProfileVersionWithdrawn
		p.Versions[index].WithdrawnAt = &stamp
		p.UpdatedAt = stamp
		p.Version++
		return nil
	}
	return NewError(CodeProfileVersionNotFound, "profile version was not found")
}

// ResolveBindable returns the highest published, non-withdrawn version whose
// effective-from time has passed at the given moment.
func (p CellarProfile) ResolveBindable(now time.Time) (ProfileVersion, bool) {
	moment := now.UTC()
	for index := len(p.Versions) - 1; index >= 0; index-- {
		version := p.Versions[index]
		if version.State == ProfileVersionPublished && !version.EffectiveFrom.After(moment) {
			return version, true
		}
	}
	return ProfileVersion{}, false
}

// HasUsableVersion reports whether the family holds at least one published,
// non-withdrawn version, regardless of its effective-from time.
func (p CellarProfile) HasUsableVersion() bool {
	for _, version := range p.Versions {
		if version.State == ProfileVersionPublished {
			return true
		}
	}
	return false
}

func (p CellarProfile) VersionByNumber(number int64) (ProfileVersion, bool) {
	for _, version := range p.Versions {
		if version.Version == number {
			return version, true
		}
	}
	return ProfileVersion{}, false
}

type ProfileDecisionEvent string

const (
	ProfileDecisionBoundAtStart         ProfileDecisionEvent = "bound_at_start"
	ProfileDecisionReusedAtConditioning ProfileDecisionEvent = "reused_at_conditioning"
	ProfileDecisionReusedAtCompletion   ProfileDecisionEvent = "reused_at_completion"
)

// ProfileBinding pins the exact profile version a run adopted, including a
// snapshot of its expectations, so later publications or withdrawals cannot
// silently rewrite the standard the run is judged by.
type ProfileBinding struct {
	ProfileID    string               `json:"profile_id"`
	ProfileName  string               `json:"profile_name"`
	Version      int64                `json:"version"`
	Expectations ProfileExpectations  `json:"expectations"`
	Decision     ProfileDecisionEvent `json:"decision"`
	Reason       string               `json:"reason"`
	BoundAt      time.Time            `json:"bound_at"`
}

// ProfileDecision is one audit entry explaining which profile version a run
// used at a lifecycle point and why.
type ProfileDecision struct {
	At        time.Time            `json:"at"`
	Event     ProfileDecisionEvent `json:"event"`
	ProfileID string               `json:"profile_id"`
	Version   int64                `json:"version"`
	Reason    string               `json:"reason"`
}

const (
	AssessmentWithinExpectations  = "within_expectations"
	AssessmentOutsideExpectations = "outside_expectations"
)

// CompletionAssessment records how a finished run measured against the
// expectations of its bound profile version.
type CompletionAssessment struct {
	ProfileID        string    `json:"profile_id"`
	ProfileVersion   int64     `json:"profile_version"`
	EvaluatedAt      time.Time `json:"evaluated_at"`
	DurationDays     float64   `json:"duration_days"`
	MinObservedTempC float64   `json:"min_observed_temp_c"`
	MaxObservedTempC float64   `json:"max_observed_temp_c"`
	Outcome          string    `json:"outcome"`
	Findings         []string  `json:"findings"`
}

func AssessCompletion(
	binding ProfileBinding,
	startedAt time.Time,
	endedAt time.Time,
	observations []GravityObservation,
) CompletionAssessment {
	expectations := binding.Expectations
	durationDays := endedAt.Sub(startedAt).Hours() / 24
	if durationDays < 0 {
		durationDays = 0
	}
	assessment := CompletionAssessment{
		ProfileID:      binding.ProfileID,
		ProfileVersion: binding.Version,
		EvaluatedAt:    endedAt.UTC(),
		DurationDays:   round2(durationDays),
		Outcome:        AssessmentWithinExpectations,
		Findings:       []string{},
	}
	if expectations.MinFermentationDays > 0 &&
		durationDays < float64(expectations.MinFermentationDays) {
		assessment.Findings = append(assessment.Findings, fmt.Sprintf(
			"fermentation lasted %.2f days, below the expected minimum of %d days",
			durationDays, expectations.MinFermentationDays))
	}
	if durationDays > float64(expectations.MaxFermentationDays) {
		assessment.Findings = append(assessment.Findings, fmt.Sprintf(
			"fermentation lasted %.2f days, above the expected maximum of %d days",
			durationDays, expectations.MaxFermentationDays))
	}
	if len(observations) > 0 {
		low := observations[0].TemperatureC
		high := observations[0].TemperatureC
		outside := 0
		for _, observation := range observations {
			if observation.TemperatureC < low {
				low = observation.TemperatureC
			}
			if observation.TemperatureC > high {
				high = observation.TemperatureC
			}
			if observation.TemperatureC < expectations.MinFermentationTempC ||
				observation.TemperatureC > expectations.MaxFermentationTempC {
				outside++
			}
		}
		assessment.MinObservedTempC = low
		assessment.MaxObservedTempC = high
		if outside > 0 {
			assessment.Findings = append(assessment.Findings, fmt.Sprintf(
				"%d of %d observations were outside the expected temperature band %.1f to %.1f Celsius (observed %.1f to %.1f)",
				outside, len(observations),
				expectations.MinFermentationTempC, expectations.MaxFermentationTempC,
				low, high))
		}
	}
	if len(assessment.Findings) > 0 {
		assessment.Outcome = AssessmentOutsideExpectations
	}
	return assessment
}
