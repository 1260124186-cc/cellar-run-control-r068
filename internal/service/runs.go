package service

import (
	"sort"
	"strconv"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateRun(input CreateRunInput) (domain.FermentationRun, error) {
	now := s.clock.Now()
	code := domain.NormalizeRunCode(input.Code)
	if code == "" {
		generated, err := generatedRunCode()
		if err != nil {
			return domain.FermentationRun{}, domain.NewError(domain.CodeStorage, err.Error())
		}
		code = generated
	}
	run := domain.FermentationRun{
		Code:           code,
		FormulaID:      input.FormulaID,
		PlannedVolumeL: input.PlannedVolumeL,
		Notes:          input.Notes,
		State:          domain.RunPlanned,
		Observations:   []domain.GravityObservation{},
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}
	if err := run.Validate(); err != nil {
		return domain.FermentationRun{}, err
	}
	id, err := generateEntityID("run")
	if err != nil {
		return domain.FermentationRun{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	run.ID = id

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		formula, ok := snapshot.Formulas[run.FormulaID]
		if !ok {
			return domain.NewError(domain.CodeFormulaNotFound, "formula was not found")
		}
		if formula.State != domain.FormulaApproved {
			return domain.NewError(domain.CodeFormulaState,
				"run requires an approved formula")
		}
		if formula.ProcessConfigID == nil || *formula.ProcessConfigID == "" {
			return domain.NewError(domain.CodeProcessConfigUnbound,
				"approved formula has no bound process configuration")
		}
		run.ProcessConfigID = *formula.ProcessConfigID
		if _, exists := snapshot.RunIndex[run.Code]; exists {
			return domain.NewError(domain.CodeRunConflict,
				"a run with the same code already exists")
		}
		snapshot.Runs[run.ID] = run.Clone()
		snapshot.RunIndex[run.Code] = run.ID
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, err
	}
	return run.Clone(), nil
}

func (s *Service) ReserveRun(
	id string,
	input ReserveRunInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		vessel, ok := snapshot.Vessels[input.VesselID]
		if !ok {
			return domain.NewError(domain.CodeVesselNotFound, "vessel was not found")
		}
		if run.PlannedVolumeL > vessel.CapacityL {
			return domain.FieldError(domain.CodeInvalidInput,
				"planned_volume_liters", "exceeds vessel capacity")
		}
		if err := vessel.Reserve(run.ID, s.clock.Now()); err != nil {
			return err
		}
		if err := run.ReserveVessel(vessel.ID, s.clock.Now()); err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		snapshot.Vessels[vessel.ID] = vessel
		updatedRun = run.Clone()
		updatedVessel = vessel
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func (s *Service) StartRun(
	id string,
	input VersionInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if run.VesselID == nil {
			return domain.NewError(domain.CodeRunState, "run has no vessel")
		}
		config, ok := snapshot.ProcessConfigs[run.ProcessConfigID]
		if !ok {
			return domain.NewError(domain.CodeProcessConfigNotFound,
				"bound process configuration was not found")
		}
		if config.State != domain.ProcessConfigActive {
			return domain.NewError(domain.CodeProcessConfigState,
				"bound process configuration series has been retired")
		}
		now := s.clock.Now()
		revision, candidates, err := domain.ResolveEffective(config, now)
		if err != nil {
			return err
		}
		binding := domain.RunProcessBinding{
			ProcessConfigID:   config.ID,
			ProcessConfigName: config.Name,
			Revision:          revision.Revision,
			EffectiveAt:       revision.EffectiveAt,
			PublishedAt:       revision.PublishedAt,
			ResolutionReason:  domain.ResolutionLatestEffective,
			Profile:           revision.Profile,
			Candidates:        candidates,
		}
		vessel, ok := snapshot.Vessels[*run.VesselID]
		if !ok {
			return domain.NewError(domain.CodeVesselNotFound, "vessel was not found")
		}
		if err := vessel.StartRun(run.ID, now); err != nil {
			return err
		}
		if err := run.StartFermentation(binding, now); err != nil {
			return err
		}
		snapshot.Runs[id] = run.Clone()
		snapshot.Vessels[vessel.ID] = vessel
		updatedRun = run.Clone()
		updatedVessel = vessel
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

func (s *Service) AbortRun(
	id string,
	input VersionInput,
) (domain.FermentationRun, domain.Vessel, error) {
	var updatedRun domain.FermentationRun
	var updatedVessel domain.Vessel
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		if err := s.checkExpected(run.Version, input.ExpectedVersion); err != nil {
			return err
		}
		hadStarted := run.HadStarted()
		if err := run.Abort(s.clock.Now()); err != nil {
			return err
		}
		if run.VesselID != nil {
			vessel, ok := snapshot.Vessels[*run.VesselID]
			if !ok {
				return domain.NewError(domain.CodeVesselNotFound, "vessel was not found")
			}
			if hadStarted {
				if err := vessel.StartCleaning(run.ID, s.clock.Now()); err != nil {
					return err
				}
			} else if err := vessel.ReleaseReservation(run.ID, s.clock.Now()); err != nil {
				return err
			}
			snapshot.Vessels[vessel.ID] = vessel
			updatedVessel = vessel
		}
		snapshot.Runs[id] = run.Clone()
		updatedRun = run.Clone()
		return nil
	})
	if err != nil {
		return domain.FermentationRun{}, domain.Vessel{}, err
	}
	return updatedRun, updatedVessel, nil
}

// RunProcessStatus explains which configuration revision a run is judged by
// and whether that revision is still available in the catalogue. It is
// derived at read time; the run's own binding snapshot is never rewritten.
type RunProcessStatus struct {
	Bound                  bool   `json:"bound"`
	ProcessConfigID        string `json:"process_config_id,omitempty"`
	ProcessConfigName      string `json:"process_config_name,omitempty"`
	Revision               int64  `json:"revision,omitempty"`
	EffectiveAt            string `json:"effective_at,omitempty"`
	ResolutionReason       string `json:"resolution_reason,omitempty"`
	BoundRevisionWithdrawn bool   `json:"bound_revision_withdrawn"`
	ConfigSeriesRetired    bool   `json:"config_series_retired"`
	PendingConfigID        string `json:"pending_config_id,omitempty"`
	PendingConfigName      string `json:"pending_config_name,omitempty"`
	PendingEffective       bool   `json:"pending_effective_revision"`
	Summary                string `json:"summary"`
}

// RunView pairs the stored run aggregate with its derived configuration
// status. The embedded run is returned unchanged so clients keep one shape.
type RunView struct {
	domain.FermentationRun
	ProcessStatus RunProcessStatus `json:"process_status"`
}

func (s *Service) GetRun(id string) (RunView, error) {
	var view RunView
	err := s.store.View(func(snapshot domain.Snapshot) error {
		run, ok := snapshot.Runs[id]
		if !ok {
			return domain.NewError(domain.CodeRunNotFound, "run was not found")
		}
		view = s.buildRunView(snapshot, run)
		return nil
	})
	if err != nil {
		return RunView{}, err
	}
	return view, nil
}

func (s *Service) ListRuns() ([]RunView, error) {
	var views []RunView
	err := s.store.View(func(snapshot domain.Snapshot) error {
		views = make([]RunView, 0, len(snapshot.Runs))
		for _, run := range snapshot.Runs {
			views = append(views, s.buildRunView(snapshot, run))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Code == views[j].Code {
			return views[i].ID < views[j].ID
		}
		return views[i].Code < views[j].Code
	})
	return views, nil
}

func (s *Service) buildRunView(snapshot domain.Snapshot, run domain.FermentationRun) RunView {
	now := s.clock.Now().UTC()
	status := RunProcessStatus{}
	if run.ProcessBinding != nil {
		binding := run.ProcessBinding
		status.Bound = true
		status.ProcessConfigID = binding.ProcessConfigID
		status.ProcessConfigName = binding.ProcessConfigName
		status.Revision = binding.Revision
		status.EffectiveAt = binding.EffectiveAt.UTC().Format(time.RFC3339)
		status.ResolutionReason = binding.ResolutionReason
		status.Summary = "governed by revision " +
			strconv.FormatInt(binding.Revision, 10) + " of " + binding.ProcessConfigName +
			", bound at start; thresholds are snapshotted and immutable"
		if config, ok := snapshot.ProcessConfigs[binding.ProcessConfigID]; ok {
			if config.State == domain.ProcessConfigRetired {
				status.ConfigSeriesRetired = true
			}
			if int(binding.Revision) <= len(config.Revisions) &&
				config.Revisions[binding.Revision-1].Withdrawn(now) {
				status.BoundRevisionWithdrawn = true
				status.Summary += "; the bound revision has since been withdrawn, but this run keeps its snapshot"
			}
		} else {
			status.Summary += "; the configuration series was removed from the catalogue"
		}
	} else if run.ProcessConfigID != "" {
		status.PendingConfigID = run.ProcessConfigID
		if config, ok := snapshot.ProcessConfigs[run.ProcessConfigID]; ok {
			status.PendingConfigName = config.Name
			if _, _, err := domain.ResolveEffective(config, now); err != nil {
				status.Summary = "not started; no revision of " + config.Name +
					" is currently effective"
			} else {
				status.PendingEffective = true
				if next := nextFutureRevision(config, now); next != nil {
					status.Summary = "not started; will resolve against the latest revision of " +
						config.Name + " effective at start; revision " +
						strconv.FormatInt(next.Revision, 10) + " becomes effective " +
						next.EffectiveAt.UTC().Format(time.RFC3339)
				} else {
					status.Summary = "not started; will resolve against the latest revision of " +
						config.Name + " effective at start"
				}
			}
		} else {
			status.Summary = "not started; bound configuration series is missing"
		}
	} else {
		status.Summary = "run predates process configuration binding"
	}
	return RunView{FermentationRun: run.Clone(), ProcessStatus: status}
}

func nextFutureRevision(config domain.ProcessConfig, now time.Time) *domain.ProcessConfigRevision {
	var next *domain.ProcessConfigRevision
	for index := range config.Revisions {
		revision := config.Revisions[index]
		if revision.Withdrawn(now) || !revision.EffectiveAt.After(now) {
			continue
		}
		if next == nil || revision.EffectiveAt.Before(next.EffectiveAt) {
			candidate := revision
			next = &candidate
		}
	}
	return next
}
