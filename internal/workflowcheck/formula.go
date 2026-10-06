package workflowcheck

import (
	"fmt"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func formulaApproval(api *client) error {
	profile, err := ensurePublishedProfile(api, "Current Pale Profile")
	if err != nil {
		return err
	}

	createdResponse, err := api.call("POST", "/v1/formulas",
		createFormulaPayload("Current Pale"), nil)
	if err != nil {
		return err
	}
	if err := requireStatus(createdResponse, 201, "create formula"); err != nil {
		return err
	}
	var formula formulaView
	if err := createdResponse.decode(&formula); err != nil {
		return err
	}
	if formula.State != domain.FormulaDraft || formula.Version != 1 {
		return fmt.Errorf("created formula has unexpected state or version")
	}

	premature, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve", nil, ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireCode(premature, 400, string(domain.CodeInvalidInput),
		"approve without steps"); err != nil {
		return err
	}

	updatedResponse, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/steps",
		formulaStepPayload(), ptrInt64(1))
	if err != nil {
		return err
	}
	if err := requireStatus(updatedResponse, 200, "set formula steps"); err != nil {
		return err
	}
	if err := updatedResponse.decode(&formula); err != nil {
		return err
	}
	if formula.Version != 2 || len(formula.Steps) != 3 {
		return fmt.Errorf("formula steps were not accepted as one revision")
	}

	missingProfile, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve",
		nil, ptrInt64(2))
	if err != nil {
		return err
	}
	if err := requireCode(missingProfile, 400, string(domain.CodeInvalidInput),
		"approve without a selected profile"); err != nil {
		return err
	}

	selectedResponse, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/profile",
		selectProfilePayload(profile.ID), ptrInt64(2))
	if err != nil {
		return err
	}
	if err := requireStatus(selectedResponse, 200, "select formula profile"); err != nil {
		return err
	}
	if err := selectedResponse.decode(&formula); err != nil {
		return err
	}
	if formula.Version != 3 || formula.ProfileID != profile.ID {
		return fmt.Errorf("formula profile selection was not accepted as one revision")
	}

	duplicate, err := api.call("POST", "/v1/formulas",
		createFormulaPayload("  current   pale  "), nil)
	if err != nil {
		return err
	}
	if err := requireCode(duplicate, 409, string(domain.CodeFormulaNameConflict),
		"create duplicate formula"); err != nil {
		return err
	}

	approvedResponse, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve", nil, ptrInt64(3))
	if err != nil {
		return err
	}
	if err := requireStatus(approvedResponse, 200, "approve formula"); err != nil {
		return err
	}
	if err := approvedResponse.decode(&formula); err != nil {
		return err
	}
	if formula.State != domain.FormulaApproved || formula.Version != 4 {
		return fmt.Errorf("approved formula has unexpected state or version")
	}

	readResponse, err := api.call("GET", "/v1/formulas/"+formula.ID, nil, nil)
	if err != nil {
		return err
	}
	if err := requireStatus(readResponse, 200, "read formula"); err != nil {
		return err
	}
	var readBack formulaView
	if err := readResponse.decode(&readBack); err != nil {
		return err
	}
	if readBack.State != domain.FormulaApproved || len(readBack.Steps) != 3 ||
		readBack.ProfileID != profile.ID {
		return fmt.Errorf("approved formula was not readable")
	}

	repeated, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve", nil, ptrInt64(4))
	if err != nil {
		return err
	}
	if err := requireCode(repeated, 409, string(domain.CodeFormulaState),
		"approve formula twice"); err != nil {
		return err
	}
	return nil
}

func ensurePublishedProfile(api *client, name string) (profileView, error) {
	createdResponse, err := api.call("POST", "/v1/profiles", createProfilePayload(name), nil)
	if err != nil {
		return profileView{}, err
	}
	if err := requireStatus(createdResponse, 201, "create setup profile"); err != nil {
		return profileView{}, err
	}
	var profile profileView
	if err := createdResponse.decode(&profile); err != nil {
		return profileView{}, err
	}
	publishResponse, err := api.call("POST", "/v1/profiles/"+profile.ID+"/versions",
		publishProfilePayload("setup standard"), ptrInt64(1))
	if err != nil {
		return profileView{}, err
	}
	if err := requireStatus(publishResponse, 201, "publish setup profile version"); err != nil {
		return profileView{}, err
	}
	if err := publishResponse.decode(&profile); err != nil {
		return profileView{}, err
	}
	if len(profile.Versions) != 1 || profile.Versions[0].Version != 1 ||
		profile.Versions[0].State != string(domain.ProfileVersionPublished) {
		return profileView{}, fmt.Errorf("published profile version was not recorded")
	}
	return profile, nil
}

func ensureApprovedFormula(api *client, name string) (formulaView, error) {
	profile, err := ensurePublishedProfile(api, name+" Profile")
	if err != nil {
		return formulaView{}, err
	}
	return ensureApprovedFormulaWithProfile(api, name, profile.ID)
}

func ensureApprovedFormulaWithProfile(
	api *client,
	name string,
	profileID string,
) (formulaView, error) {
	createdResponse, err := api.call("POST", "/v1/formulas", createFormulaPayload(name), nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(createdResponse, 201, "create setup formula"); err != nil {
		return formulaView{}, err
	}
	var formula formulaView
	if err := createdResponse.decode(&formula); err != nil {
		return formulaView{}, err
	}
	stepsResponse, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/steps",
		formulaStepPayload(), ptrInt64(1))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(stepsResponse, 200, "set setup steps"); err != nil {
		return formulaView{}, err
	}
	profileResponse, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/profile",
		selectProfilePayload(profileID), ptrInt64(2))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(profileResponse, 200, "select setup profile"); err != nil {
		return formulaView{}, err
	}
	approveResponse, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve",
		nil, ptrInt64(3))
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(approveResponse, 200, "approve setup formula"); err != nil {
		return formulaView{}, err
	}
	if err := approveResponse.decode(&formula); err != nil {
		return formulaView{}, err
	}
	return formula, nil
}

func ptrInt64(value int64) *int64 {
	return &value
}
