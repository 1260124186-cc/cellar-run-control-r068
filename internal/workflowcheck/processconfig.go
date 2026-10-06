package workflowcheck

import (
	"fmt"
	"sync"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

// processConfigVersioning exercises the guarantees behind versioned cellar
// process configuration:
//   - approval is impossible without a selected configuration;
//   - concurrent publishes receive contiguous revision numbers;
//   - runs bind the revision effective at their start, with every candidate
//     revision explained;
//   - crossing an effective time mid-life moves later starts to the new
//     revision while earlier runs keep their snapshot;
//   - withdrawing the bound revision does not disturb a live or completed run;
//   - completion is evaluated against the bound revision, not the catalogue.
func processConfigVersioning(api *client) error {
	// 1. Approval gate: two supervisors publishing concurrently.
	config, currentRev, pendingRev, err := createConfigWithConcurrentPublishes(api)
	if err != nil {
		return err
	}
	pendingEffectiveAt := config.Revisions[pendingRev-1].EffectiveAt

	// 2. Publish guards: duplicate effective time and stale version conflict.
	now := time.Now().UTC()
	duplicate, err := api.call("POST", "/v1/process-configs/"+config.ID+"/revisions",
		publishPayload(now.Add(-2*time.Hour)), ptrInt64(config.Version))
	if err != nil {
		return err
	}
	if err := requireCode(duplicate, 409, string(domain.CodeProcessConfigRevision),
		"publish duplicate effective time"); err != nil {
		return err
	}
	stale, err := api.call("POST", "/v1/process-configs/"+config.ID+"/revisions",
		publishPayload(now.Add(3*time.Hour)), ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireCode(stale, 412, string(domain.CodeRevisionConflict),
		"publish with stale version"); err != nil {
		return err
	}

	// 3. While the near-future revision is pending, a run started now binds
	// the currently effective revision and snapshots its thresholds.
	formula, err := approvedFormulaForConfig(api, "Versioning Formula", config.ID)
	if err != nil {
		return err
	}
	vesselA, err := createCheckVessel(api, "VSL-301", 300)
	if err != nil {
		return err
	}
	runA, err := createStartedRun(api, formula.ID, vesselA.ID, "RUN-CONFIG-A")
	if err != nil {
		return err
	}
	if runA.Run.ProcessBinding == nil || runA.Run.ProcessBinding.Revision != currentRev {
		return errInvalidWorkflow("run A did not bind the currently effective revision")
	}
	if err := requireCandidate(runA.Run.ProcessBinding.Candidates, pendingRev,
		domain.CandidateNotYetEffective); err != nil {
		return err
	}
	if runA.Run.ProcessBinding.Profile.MaxGravityRise != 0.010 ||
		runA.Run.ProcessBinding.Profile.MinCompletionObservations != 3 {
		return errInvalidWorkflow("run A did not snapshot the effective revision thresholds")
	}

	// 4. After the effective time passes, a new run binds the pending
	// revision; run A keeps its snapshot even though the catalogue moved.
	if err := waitUntilEffective(api, config.ID, pendingRev, pendingEffectiveAt); err != nil {
		return err
	}
	vesselB, err := createCheckVessel(api, "VSL-302", 300)
	if err != nil {
		return err
	}
	runB, err := createStartedRun(api, formula.ID, vesselB.ID, "RUN-CONFIG-B")
	if err != nil {
		return err
	}
	if runB.Run.ProcessBinding == nil || runB.Run.ProcessBinding.Revision != pendingRev {
		return errInvalidWorkflow("run B did not bind the revision that just became effective")
	}
	if err := requireCandidate(runB.Run.ProcessBinding.Candidates, currentRev,
		domain.CandidateSuperseded); err != nil {
		return err
	}

	// 5. Withdrawing the new revision blocks future starts but run B keeps
	// its snapshot; the read view flags the withdrawn bound revision.
	withdrawResponse, err := api.call("POST",
		fmt.Sprintf("/v1/process-configs/%s/revisions/%d/withdraw", config.ID, pendingRev),
		map[string]any{"reason": "superseded in error, hold new starts"},
		ptrInt64(config.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(withdrawResponse, 200, "withdraw the new revision"); err != nil {
		return err
	}
	var withdrawnConfig processConfigView
	if err := withdrawResponse.decode(&withdrawnConfig); err != nil {
		return err
	}

	readA, err := readRun(api, runA.Run.ID)
	if err != nil {
		return err
	}
	if readA.ProcessStatus.Revision != currentRev || readA.ProcessStatus.BoundRevisionWithdrawn {
		return errInvalidWorkflow("run A status should show its revision still available")
	}
	readB, err := readRun(api, runB.Run.ID)
	if err != nil {
		return err
	}
	if !readB.ProcessStatus.BoundRevisionWithdrawn {
		return errInvalidWorkflow("run B status should flag its bound revision as withdrawn")
	}

	// 6. Run B still completes against its withdrawn-but-snapshotted
	// revision thresholds.
	if err := finishRun(api, runB, 1.012); err != nil {
		return err
	}

	// 7. With the new revision withdrawn, a fresh start falls back to the
	// older still-effective revision; the withdrawn one is explained.
	vesselC, err := createCheckVessel(api, "VSL-303", 300)
	if err != nil {
		return err
	}
	runC, err := createStartedRun(api, formula.ID, vesselC.ID, "RUN-CONFIG-C")
	if err != nil {
		return err
	}
	if runC.Run.ProcessBinding.Revision != currentRev {
		return errInvalidWorkflow("run C should fall back to the older revision after withdrawal")
	}
	if err := requireCandidate(runC.Run.ProcessBinding.Candidates, pendingRev,
		domain.CandidateWithdrawn); err != nil {
		return err
	}

	// 8. Withdraw every effective revision: starting a fresh reserved run
	// must fail with a stable code and leave the run unbound.
	if _, err := withdraw(api, config.ID, currentRev, withdrawnConfig.Version); err != nil {
		return err
	}
	vesselD, err := createCheckVessel(api, "VSL-304", 300)
	if err != nil {
		return err
	}
	plannedResponse, err := api.call("POST", "/v1/runs",
		createRunPayload("RUN-CONFIG-D", formula.ID), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(plannedResponse, 201, "create blocked run"); err != nil {
		return err
	}
	var planned runView
	if err := plannedResponse.decode(&planned); err != nil {
		return err
	}
	reserveBlocked, err := api.call("POST", "/v1/runs/"+planned.ID+"/reserve",
		map[string]any{"vessel_id": vesselD.ID}, ptrInt64(planned.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(reserveBlocked, 200, "reserve blocked run"); err != nil {
		return err
	}
	var blockedPair pairView
	if err := reserveBlocked.decode(&blockedPair); err != nil {
		return err
	}
	startBlocked, err := api.call("POST", "/v1/runs/"+planned.ID+"/start", nil,
		ptrInt64(blockedPair.Run.Version))
	if err != nil {
		return err
	}
	if err := requireCode(startBlocked, 409, string(domain.CodeProcessConfigNotEffective),
		"start without an effective revision"); err != nil {
		return err
	}
	pendingRead, err := readRun(api, planned.ID)
	if err != nil {
		return err
	}
	if pendingRead.ProcessStatus.Bound || pendingRead.ProcessStatus.PendingConfigName == "" {
		return errInvalidWorkflow("blocked run should show a pending, unbound configuration")
	}

	// 9. Run A (bound to the older revision) finishes normally even though
	// that revision was withdrawn after it started; its result records it.
	if err := finishRun(api, runA, 1.012); err != nil {
		return err
	}
	completedA, err := readRun(api, runA.Run.ID)
	if err != nil {
		return err
	}
	if completedA.ProcessResult == nil ||
		!completedA.ProcessResult.FinalGravityWithinTolerance ||
		completedA.ProcessResult.DurationStatus != domain.DurationBeforeWindow ||
		!completedA.ProcessResult.BoundRevisionWithdrawn {
		return errInvalidWorkflow("completed run did not record bound-revision evaluation")
	}

	// 10. Retiring the series blocks new publishes; existing runs survive
	// and the read view reports the retirement.
	retireResponse, err := api.call("POST", "/v1/process-configs/"+config.ID+"/retire",
		nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(retireResponse, 200, "retire process config"); err != nil {
		return err
	}
	retiredRead, err := readRun(api, runA.Run.ID)
	if err != nil {
		return err
	}
	if !retiredRead.ProcessStatus.ConfigSeriesRetired ||
		retiredRead.ProcessStatus.Revision != currentRev {
		return errInvalidWorkflow("retired series was not reflected on the run view")
	}
	publishOnRetired, err := api.call("POST", "/v1/process-configs/"+config.ID+"/revisions",
		publishPayload(now.Add(24*time.Hour)), nil)
	if err != nil {
		return err
	}
	if err := requireCode(publishOnRetired, 409, string(domain.CodeProcessConfigState),
		"publish on retired config"); err != nil {
		return err
	}
	return nil
}

// createConfigWithConcurrentPublishes fires three simultaneous publishes with
// staggered effective times. It resolves revision numbers from the persisted
// result rather than assuming request order: currentRev is effective now and
// pendingRev is the next one to become effective (~70 seconds out).
func createConfigWithConcurrentPublishes(api *client) (config processConfigView, currentRev, pendingRev int64, err error) {
	created, callErr := api.call("POST", "/v1/process-configs",
		createProcessConfigPayload("Concurrent Season Profile"), nil)
	if callErr != nil {
		return processConfigView{}, 0, 0, callErr
	}
	if callErr = requireStatus(created, 201, "create concurrent config"); callErr != nil {
		return processConfigView{}, 0, 0, callErr
	}
	if callErr = created.decode(&config); callErr != nil {
		return processConfigView{}, 0, 0, callErr
	}

	now := time.Now().UTC()
	offsets := []time.Duration{-2 * time.Hour, 70 * time.Second, 2 * time.Hour}
	errs := make([]error, len(offsets))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for index, offset := range offsets {
		wg.Add(1)
		go func(i int, effectiveOffset time.Duration) {
			defer wg.Done()
			<-start
			response, publishErr := api.call("POST",
				"/v1/process-configs/"+config.ID+"/revisions",
				publishPayload(now.Add(effectiveOffset)), nil)
			if publishErr != nil {
				errs[i] = publishErr
				return
			}
			if response.status != 201 {
				errs[i] = fmt.Errorf("concurrent publish: expected 201, got %d: %s",
					response.status, string(response.body))
			}
		}(index, offset)
	}
	close(start)
	wg.Wait()
	for _, publishErr := range errs {
		if publishErr != nil {
			return processConfigView{}, 0, 0, publishErr
		}
	}

	fresh, callErr := getConfig(api, config.ID)
	if callErr != nil {
		return processConfigView{}, 0, 0, callErr
	}
	if len(fresh.Revisions) != 3 || fresh.Version != 4 {
		return processConfigView{}, 0, 0, errInvalidWorkflow(
			"concurrent publishes were not serialized into three revisions")
	}

	stamp := time.Now().UTC()
	currentRev = 0
	pendingRev = 0
	var pendingAt time.Time
	for _, revision := range fresh.Revisions {
		switch {
		case !revision.EffectiveAt.After(stamp):
			if currentRev != 0 {
				return processConfigView{}, 0, 0, errInvalidWorkflow(
					"more than one revision was effective immediately after publish")
			}
			currentRev = revision.Revision
		default:
			if pendingRev == 0 || revision.EffectiveAt.Before(pendingAt) {
				pendingRev = revision.Revision
				pendingAt = revision.EffectiveAt
			}
		}
	}
	if currentRev == 0 || pendingRev == 0 {
		return processConfigView{}, 0, 0, errInvalidWorkflow(
			"could not resolve current and pending revisions after concurrent publish")
	}
	if time.Until(pendingAt) > 110*time.Second {
		return processConfigView{}, 0, 0, errInvalidWorkflow(
			"the pending revision did not land in the expected wait window")
	}
	return fresh, currentRev, pendingRev, nil
}

func publishPayload(effectiveAt time.Time) map[string]any {
	return map[string]any{
		"profile":      standardProfile(),
		"effective_at": effectiveAt.Format(time.RFC3339),
		"published_by": "season-supervisor",
	}
}

func approvedFormulaForConfig(api *client, name, configID string) (formulaView, error) {
	created, err := api.call("POST", "/v1/formulas", createFormulaPayload(name), nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(created, 201, "create formula"); err != nil {
		return formulaView{}, err
	}
	var formula formulaView
	if err := created.decode(&formula); err != nil {
		return formulaView{}, err
	}
	if err := approveFormulaWithConfig(api, formula.ID, configID); err != nil {
		return formulaView{}, err
	}
	formula.State = domain.FormulaApproved
	return formula, nil
}

func waitUntilEffective(api *client, configID string, revision int64, effectiveAt time.Time) error {
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if time.Now().UTC().After(effectiveAt) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("revision %d did not become effective within the wait window", revision)
}

func getConfig(api *client, id string) (processConfigView, error) {
	response, err := api.call("GET", "/v1/process-configs/"+id, nil, nil)
	if err != nil {
		return processConfigView{}, err
	}
	if err := requireStatus(response, 200, "read process config"); err != nil {
		return processConfigView{}, err
	}
	var config processConfigView
	if err := response.decode(&config); err != nil {
		return processConfigView{}, err
	}
	return config, nil
}

func readRun(api *client, id string) (runView, error) {
	response, err := api.call("GET", "/v1/runs/"+id, nil, nil)
	if err != nil {
		return runView{}, err
	}
	if err := requireStatus(response, 200, "read run"); err != nil {
		return runView{}, err
	}
	var run runView
	if err := response.decode(&run); err != nil {
		return runView{}, err
	}
	return run, nil
}

func withdraw(api *client, configID string, revision int64, version int64) (processConfigView, error) {
	response, err := api.call("POST",
		fmt.Sprintf("/v1/process-configs/%s/revisions/%d/withdraw", configID, revision),
		map[string]any{"reason": "withdrawn by check"}, ptrInt64(version))
	if err != nil {
		return processConfigView{}, err
	}
	if err := requireStatus(response, 200, "withdraw revision"); err != nil {
		return processConfigView{}, err
	}
	var config processConfigView
	if err := response.decode(&config); err != nil {
		return processConfigView{}, err
	}
	return config, nil
}

func requireCandidate(candidates []domain.RevisionCandidate, revision int64, status string) error {
	for _, candidate := range candidates {
		if candidate.Revision == revision {
			if candidate.Status != status {
				return fmt.Errorf("candidate revision %d: expected status %q, got %q",
					revision, status, candidate.Status)
			}
			return nil
		}
	}
	return fmt.Errorf("resolution did not explain candidate revision %d", revision)
}

// finishRun appends three observations, conditions, and completes a run.
func finishRun(api *client, started pairView, finalGravity float64) error {
	base := time.Now().UTC().Add(10 * time.Second)
	points := []float64{1.050, 1.030, finalGravity}
	version := started.Run.Version
	for index, gravity := range points {
		response, err := api.call("POST",
			"/v1/runs/"+started.Run.ID+"/observations",
			map[string]any{
				"sequence":      index + 1,
				"gravity":       gravity,
				"temperature_c": 20.0,
				"observed_at":   verifiedAt(base, index+1),
			}, ptrInt64(version))
		if err != nil {
			return err
		}
		if err := requireStatus(response, 200, "append finishing observation"); err != nil {
			return err
		}
		var run runView
		if err := response.decode(&run); err != nil {
			return err
		}
		version = run.Version
	}
	conditioning, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/conditioning",
		nil, ptrInt64(version))
	if err != nil {
		return err
	}
	if err := requireStatus(conditioning, 200, "condition finishing run"); err != nil {
		return err
	}
	var conditioned runView
	if err := conditioning.decode(&conditioned); err != nil {
		return err
	}
	complete, err := api.call("POST", "/v1/runs/"+started.Run.ID+"/complete",
		nil, ptrInt64(conditioned.Version))
	if err != nil {
		return err
	}
	return requireStatus(complete, 200, "complete finishing run")
}
