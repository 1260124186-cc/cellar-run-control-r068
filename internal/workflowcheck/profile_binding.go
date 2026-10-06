package workflowcheck

import (
	"fmt"
	"strings"
	"time"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func profileBinding(api *client) error {
	profile, err := ensureProfile(api, "Seasonal Standard")
	if err != nil {
		return err
	}
	published, err := publishProfile(api, profile.ID, "standard A", ptrInt64(1))
	if err != nil {
		return err
	}
	if len(published.Versions) != 1 || published.Versions[0].Version != 1 {
		return errInvalidWorkflow("first profile version was not published")
	}

	formula, err := ensureApprovedFormulaWithProfile(api, "Profile Binding Formula", profile.ID)
	if err != nil {
		return err
	}
	if formula.ProfileID != profile.ID {
		return errInvalidWorkflow("approved formula did not retain its selected profile")
	}

	// A stale supervisor publish is rejected instead of silently renumbering.
	stale, err := api.call("POST", "/v1/profiles/"+profile.ID+"/versions",
		publishProfilePayload("stale standard"), ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireCode(stale, 412, string(domain.CodeRevisionConflict),
		"publish with a stale family version"); err != nil {
		return err
	}

	vesselOne, err := createCheckVessel(api, "VSL-301", 300)
	if err != nil {
		return err
	}
	first, err := createStartedRun(api, formula.ID, vesselOne.ID, "RUN-PROFILE-A")
	if err != nil {
		return err
	}
	if first.Run.ProfileBinding == nil || first.Run.ProfileBinding.Version != 1 {
		return errInvalidWorkflow("first run did not bind profile version 1 at start")
	}

	// A second version published mid-run does not rewrite the running batch.
	second, err := publishProfile(api, profile.ID, "standard B", ptrInt64(2))
	if err != nil {
		return err
	}
	if len(second.Versions) != 2 {
		return errInvalidWorkflow("second profile version was not appended")
	}

	vesselTwo, err := createCheckVessel(api, "VSL-302", 300)
	if err != nil {
		return err
	}
	later, err := createStartedRun(api, formula.ID, vesselTwo.ID, "RUN-PROFILE-B")
	if err != nil {
		return err
	}
	if later.Run.ProfileBinding == nil || later.Run.ProfileBinding.Version != 2 {
		return errInvalidWorkflow("second run did not bind the newer profile version")
	}

	if err := finishRun(api, first.Run); err != nil {
		return err
	}
	finishedResponse, err := api.call("GET", "/v1/runs/"+first.Run.ID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(finishedResponse, 200, "read finished run"); err != nil {
		return err
	}
	var finished runView
	if err := finishedResponse.decode(&finished); err != nil {
		return err
	}
	if finished.Assessment == nil || finished.Assessment.ProfileVersion != 1 {
		return errInvalidWorkflow("finished run was not assessed against its bound version")
	}
	if !hasCompletionDecision(finished.ProfileDecisions, "version 2") {
		return errInvalidWorkflow("run decisions did not explain keeping version 1 over version 2")
	}

	// Withdrawing the newer version makes older runs resolvable again.
	withdrawn, err := api.call("POST", "/v1/profiles/"+profile.ID+"/versions/2/withdraw",
		nil, ptrInt64(3))
	if err != nil {
		return err
	}
	if err := requireStatus(withdrawn, 200, "withdraw profile version 2"); err != nil {
		return err
	}
	vesselThree, err := createCheckVessel(api, "VSL-303", 300)
	if err != nil {
		return err
	}
	third, err := createStartedRun(api, formula.ID, vesselThree.ID, "RUN-PROFILE-C")
	if err != nil {
		return err
	}
	if third.Run.ProfileBinding == nil || third.Run.ProfileBinding.Version != 1 {
		return errInvalidWorkflow("run did not fall back to the remaining published version")
	}

	// Withdrawing the last published version blocks new starts entirely.
	if _, err := api.call("POST", "/v1/profiles/"+profile.ID+"/versions/1/withdraw",
		nil, ptrInt64(4)); err != nil {
		return err
	}
	vesselFour, err := createCheckVessel(api, "VSL-304", 300)
	if err != nil {
		return err
	}
	blocked, err := startRunExpecting(api, formula.ID, vesselFour.ID, "RUN-PROFILE-D")
	if err != nil {
		return err
	}
	if err := requireCode(blocked, 409, string(domain.CodeProfileNotBindable),
		"start run without a bindable profile version"); err != nil {
		return err
	}

	// A formula cannot be approved against a family with no usable version.
	empty, err := ensureProfile(api, "Empty Standard")
	if err != nil {
		return err
	}
	draftResponse, err := api.call("POST", "/v1/formulas",
		createFormulaPayload("Unbindable Formula"), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(draftResponse, 201, "create unbindable formula"); err != nil {
		return err
	}
	var draft formulaView
	if err := draftResponse.decode(&draft); err != nil {
		return err
	}
	missing, err := api.call("PUT", "/v1/formulas/"+draft.ID+"/profile",
		selectProfilePayload("profile_missing"), ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireCode(missing, 404, string(domain.CodeProfileNotFound),
		"select an unknown profile"); err != nil {
		return err
	}
	selected, err := api.call("PUT", "/v1/formulas/"+draft.ID+"/profile",
		selectProfilePayload(empty.ID), ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireStatus(selected, 200, "select empty profile"); err != nil {
		return err
	}
	stepsResponse, err := api.call("PUT", "/v1/formulas/"+draft.ID+"/steps",
		formulaStepPayload(), ptrInt64(2))
	if err != nil {
		return err
	}
	if err := requireStatus(stepsResponse, 200, "set unbindable formula steps"); err != nil {
		return err
	}
	unbindable, err := api.call("POST", "/v1/formulas/"+draft.ID+"/approve", nil, ptrInt64(3))
	if err != nil {
		return err
	}
	if err := requireCode(unbindable, 409, string(domain.CodeProfileNotBindable),
		"approve against a profile without versions"); err != nil {
		return err
	}
	return nil
}

func ensureProfile(api *client, name string) (profileView, error) {
	response, err := api.call("POST", "/v1/profiles", createProfilePayload(name), nil)
	if err != nil {
		return profileView{}, err
	}
	if err := requireStatus(response, 201, "create profile"); err != nil {
		return profileView{}, err
	}
	var profile profileView
	if err := response.decode(&profile); err != nil {
		return profileView{}, err
	}
	return profile, nil
}

func publishProfile(
	api *client,
	profileID string,
	note string,
	expected *int64,
) (profileView, error) {
	response, err := api.call("POST", "/v1/profiles/"+profileID+"/versions",
		publishProfilePayload(note), expected)
	if err != nil {
		return profileView{}, err
	}
	if err := requireStatus(response, 201, "publish profile version"); err != nil {
		return profileView{}, err
	}
	var profile profileView
	if err := response.decode(&profile); err != nil {
		return profileView{}, err
	}
	return profile, nil
}

func startRunExpecting(
	api *client,
	formulaID string,
	vesselID string,
	code string,
) (response, error) {
	runResponse, err := api.call("POST", "/v1/runs", createRunPayload(code, formulaID), nil)
	if err != nil {
		return response{}, err
	}
	if err := requireStatus(runResponse, 201, "create blocked run"); err != nil {
		return response{}, err
	}
	var run runView
	if err := runResponse.decode(&run); err != nil {
		return response{}, err
	}
	reserveResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/reserve",
		map[string]any{"vessel_id": vesselID}, ptrInt64(run.Version))
	if err != nil {
		return response{}, err
	}
	if err := requireStatus(reserveResponse, 200, "reserve blocked run vessel"); err != nil {
		return response{}, err
	}
	var reserved pairView
	if err := reserveResponse.decode(&reserved); err != nil {
		return response{}, err
	}
	startResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/start",
		nil, ptrInt64(reserved.Run.Version))
	if err != nil {
		return response{}, err
	}
	return startResponse, nil
}

func hasCompletionDecision(decisions []profileDecisionView, fragment string) bool {
	for _, decision := range decisions {
		if decision.Event == string(domain.ProfileDecisionReusedAtCompletion) &&
			strings.Contains(decision.Reason, fragment) {
			return true
		}
	}
	return false
}

func finishRun(api *client, run runView) error {
	base := time.Now().UTC()
	points := []struct {
		gravity     float64
		temperature float64
	}{
		{gravity: 1.048, temperature: 20.0},
		{gravity: 1.026, temperature: 20.5},
		{gravity: 1.013, temperature: 19.8},
	}
	version := run.Version
	for index, point := range points {
		sequence := index + 1
		response, err := api.call("POST", "/v1/runs/"+run.ID+"/observations",
			map[string]any{
				"sequence":      sequence,
				"gravity":       point.gravity,
				"temperature_c": point.temperature,
				"observed_at":   verifiedAt(base, sequence),
				"note":          fmt.Sprintf("profile observation %d", sequence),
			}, ptrInt64(version))
		if err != nil {
			return err
		}
		if err := requireStatus(response, 200, "append profile run observation"); err != nil {
			return err
		}
		var updated runView
		if err := response.decode(&updated); err != nil {
			return err
		}
		version = updated.Version
	}
	conditioningResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/conditioning",
		nil, ptrInt64(version))
	if err != nil {
		return err
	}
	if err := requireStatus(conditioningResponse, 200, "condition profile run"); err != nil {
		return err
	}
	var conditioning runView
	if err := conditioningResponse.decode(&conditioning); err != nil {
		return err
	}
	completeResponse, err := api.call("POST", "/v1/runs/"+run.ID+"/complete",
		nil, ptrInt64(conditioning.Version))
	if err != nil {
		return err
	}
	if err := requireStatus(completeResponse, 200, "complete profile run"); err != nil {
		return err
	}
	return nil
}
