package service

import (
	"sort"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func (s *Service) CreateProcessConfig(input CreateProcessConfigInput) (domain.ProcessConfig, error) {
	now := s.clock.Now()
	config := domain.ProcessConfig{
		Name:      input.Name,
		Season:    input.Season,
		Style:     input.Style,
		Notes:     input.Notes,
		State:     domain.ProcessConfigActive,
		Revisions: []domain.ProcessConfigRevision{},
		Version:   1,
		CreatedAt: now,
		UpdatedAt: now,
	}
	config.Normalize()
	if err := config.Validate(); err != nil {
		return domain.ProcessConfig{}, err
	}
	id, err := generateEntityID("pconf")
	if err != nil {
		return domain.ProcessConfig{}, domain.NewError(domain.CodeStorage, err.Error())
	}
	config.ID = id
	nameKey := domain.NameKey(config.Name)

	err = s.store.Execute(func(snapshot *domain.Snapshot) error {
		if _, exists := snapshot.ProcessConfigIndex[nameKey]; exists {
			return domain.NewError(domain.CodeProcessConfigNameConflict,
				"a process configuration with the same normalized name already exists")
		}
		snapshot.ProcessConfigs[config.ID] = config.Clone()
		snapshot.ProcessConfigIndex[nameKey] = config.ID
		return nil
	})
	if err != nil {
		return domain.ProcessConfig{}, err
	}
	return config.Clone(), nil
}

func (s *Service) PublishRevision(
	id string,
	input PublishRevisionInput,
) (domain.ProcessConfig, domain.ProcessConfigRevision, error) {
	var updated domain.ProcessConfig
	var published domain.ProcessConfigRevision
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		config, ok := snapshot.ProcessConfigs[id]
		if !ok {
			return domain.NewError(domain.CodeProcessConfigNotFound,
				"process configuration was not found")
		}
		if err := s.checkExpected(config.Version, input.ExpectedVersion); err != nil {
			return err
		}
		revision, err := config.Publish(input.Profile, input.EffectiveAt,
			input.PublishedBy, s.clock.Now())
		if err != nil {
			return err
		}
		snapshot.ProcessConfigs[id] = config.Clone()
		updated = config.Clone()
		published = revision.Clone()
		return nil
	})
	if err != nil {
		return domain.ProcessConfig{}, domain.ProcessConfigRevision{}, err
	}
	return updated, published, nil
}

func (s *Service) WithdrawRevision(
	id string,
	revisionNumber int64,
	input WithdrawRevisionInput,
) (domain.ProcessConfig, error) {
	var updated domain.ProcessConfig
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		config, ok := snapshot.ProcessConfigs[id]
		if !ok {
			return domain.NewError(domain.CodeProcessConfigNotFound,
				"process configuration was not found")
		}
		if err := s.checkExpected(config.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if _, err := config.WithdrawRevision(revisionNumber, input.Reason, s.clock.Now()); err != nil {
			return err
		}
		snapshot.ProcessConfigs[id] = config.Clone()
		updated = config.Clone()
		return nil
	})
	if err != nil {
		return domain.ProcessConfig{}, err
	}
	return updated, nil
}

func (s *Service) RetireProcessConfig(
	id string,
	input VersionInput,
) (domain.ProcessConfig, error) {
	var updated domain.ProcessConfig
	err := s.store.Execute(func(snapshot *domain.Snapshot) error {
		config, ok := snapshot.ProcessConfigs[id]
		if !ok {
			return domain.NewError(domain.CodeProcessConfigNotFound,
				"process configuration was not found")
		}
		if err := s.checkExpected(config.Version, input.ExpectedVersion); err != nil {
			return err
		}
		if err := config.Retire(s.clock.Now()); err != nil {
			return err
		}
		snapshot.ProcessConfigs[id] = config.Clone()
		updated = config.Clone()
		return nil
	})
	if err != nil {
		return domain.ProcessConfig{}, err
	}
	return updated, nil
}

func (s *Service) GetProcessConfig(id string) (domain.ProcessConfig, error) {
	var found domain.ProcessConfig
	err := s.store.View(func(snapshot domain.Snapshot) error {
		config, ok := snapshot.ProcessConfigs[id]
		if !ok {
			return domain.NewError(domain.CodeProcessConfigNotFound,
				"process configuration was not found")
		}
		found = config.Clone()
		return nil
	})
	if err != nil {
		return domain.ProcessConfig{}, err
	}
	return found, nil
}

func (s *Service) ListProcessConfigs() ([]domain.ProcessConfig, error) {
	var configs []domain.ProcessConfig
	err := s.store.View(func(snapshot domain.Snapshot) error {
		configs = make([]domain.ProcessConfig, 0, len(snapshot.ProcessConfigs))
		for _, config := range snapshot.ProcessConfigs {
			configs = append(configs, config.Clone())
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(configs, func(i, j int) bool {
		if configs[i].Name == configs[j].Name {
			return configs[i].ID < configs[j].ID
		}
		return configs[i].Name < configs[j].Name
	})
	return configs, nil
}
