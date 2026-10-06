package service

import (
	"fmt"
	"sort"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateProfile(input CreateProfileInput) (domain.CellarProfile, error) {
	now := s.clock.Now()
	profile := domain.CellarProfile{
		Name:      input.Name,
		Season:    input.Season,
		Style:     input.Style,
		Versions:  []domain.ProfileVersion{},
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	profile.Normalize()
	if err := profile.Validate(); err != nil {
		return domain.CellarProfile{}, err
	}
	id, err := generateEntityID("profile")
	if err != nil {
		return domain.CellarProfile{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	profile.ID = id
	nameKey := domain.NameKey(profile.Name)

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, exists := snapshot.ProfileNameIndex[nameKey]; exists {
			return domain.NewError(domain.CodeProfileNameConflict,
				"a process profile with the same normalized name already exists")
		}
		snapshot.Profiles[profile.ID] = profile
		snapshot.ProfileNameIndex[nameKey] = profile.ID
		return nil
	})
	if err != nil {
		return domain.CellarProfile{}, err
	}
	return profile.Clone(), nil
}

func (s *Service) PublishProfileVersion(
	id string,
	input PublishProfileVersionInput,
) (domain.CellarProfile, error) {
	var updated domain.CellarProfile
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		profile, ok := snapshot.Profiles[id]
		if !ok {
			return domain.NewError(domain.CodeProfileNotFound, "process profile was not found")
		}
		if err := s.checkExpected(profile.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if _, err := profile.Publish(
			input.Expectations, input.EffectiveFrom, input.Note, s.clock.Now(),
		); err != nil {
			return err
		}
		snapshot.Profiles[id] = profile.Clone()
		updated = profile.Clone()
		return nil
	})
	if err != nil {
		return domain.CellarProfile{}, err
	}
	return updated, nil
}

func (s *Service) WithdrawProfileVersion(
	id string,
	version int64,
	input VersionInput,
) (domain.CellarProfile, error) {
	var updated domain.CellarProfile
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		profile, ok := snapshot.Profiles[id]
		if !ok {
			return domain.NewError(domain.CodeProfileNotFound, "process profile was not found")
		}
		if err := s.checkExpected(profile.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if err := profile.Withdraw(version, s.clock.Now()); err != nil {
			return err
		}
		snapshot.Profiles[id] = profile.Clone()
		updated = profile.Clone()
		return nil
	})
	if err != nil {
		return domain.CellarProfile{}, err
	}
	return updated, nil
}

func (s *Service) GetProfile(id string) (domain.CellarProfile, error) {
	var found domain.CellarProfile
	err := s.store.View(func(snapshot domain.Snapshot) error {
		profile, ok := snapshot.Profiles[id]
		if !ok {
			return domain.NewError(domain.CodeProfileNotFound, "process profile was not found")
		}
		found = profile.Clone()
		return nil
	})
	if err != nil {
		return domain.CellarProfile{}, err
	}
	return found, nil
}

func (s *Service) ListProfiles() ([]domain.CellarProfile, error) {
	var profiles []domain.CellarProfile
	err := s.store.View(func(snapshot domain.Snapshot) error {
		profiles = make([]domain.CellarProfile, 0, len(snapshot.Profiles))
		for _, profile := range snapshot.Profiles {
			profiles = append(profiles, profile.Clone())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(profiles, func(i, j int) bool {
		if profiles[i].Name == profiles[j].Name {
			return profiles[i].ID < profiles[j].ID
		}
		return profiles[i].Name < profiles[j].Name
	})
	return profiles, nil
}

// bindReason explains why a run adopted a specific profile version,
// including why newer versions were skipped.
func bindReason(profile domain.CellarProfile, resolved domain.ProfileVersion, now time.Time) string {
	moment := now.UTC()
	reason := fmt.Sprintf(
		"bound at run start: version %d is the highest published version effective at %s",
		resolved.Version, moment.Format(time.RFC3339))
	for _, candidate := range profile.Versions {
		if candidate.Version <= resolved.Version {
			continue
		}
		switch {
		case candidate.State == domain.ProfileVersionWithdrawn:
			reason += fmt.Sprintf("; version %d was withdrawn before the run started",
				candidate.Version)
		case candidate.EffectiveFrom.After(moment):
			reason += fmt.Sprintf("; version %d is published but not effective until %s",
				candidate.Version, candidate.EffectiveFrom.UTC().Format(time.RFC3339))
		}
	}
	return reason
}

// profileContextReason explains why a run keeps its pinned version at a
// later lifecycle point, given the family's current state.
func profileContextReason(
	profile domain.CellarProfile,
	bindingVersion int64,
	now time.Time,
) string {
	reason := fmt.Sprintf("continued with pinned version %d", bindingVersion)
	if pinned, ok := profile.VersionByNumber(bindingVersion); ok &&
		pinned.State == domain.ProfileVersionWithdrawn {
		when := "an earlier time"
		if pinned.WithdrawnAt != nil {
			when = pinned.WithdrawnAt.UTC().Format(time.RFC3339)
		}
		reason += fmt.Sprintf(
			"; version %d was withdrawn at %s and the run retains the expectations bound at start",
			bindingVersion, when)
	}
	if current, ok := profile.ResolveBindable(now); ok {
		if current.Version > bindingVersion {
			reason += fmt.Sprintf(
				"; version %d is now the highest effective published version and the run keeps version %d bound at start",
				current.Version, bindingVersion)
		}
	} else {
		reason += "; no published version is currently bindable and the run keeps its pinned expectations"
	}
	return reason
}
