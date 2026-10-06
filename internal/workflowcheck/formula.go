package workflowcheck

import (
	"fmt"

	"github.com/1260124186-cc/solo-0016-cellar-run-control/internal/domain"
)

func formulaApproval(api *client) error {
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

	config, err := ensureProcessConfig(api, "Approval Gate Config")
	if err != nil {
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

	// Approval must fail once steps exist but no configuration is selected.
	unboundResponse, err := api.call("POST", "/v1/formulas/"+formula.ID+"/approve",
		nil, ptrInt64(2))
	if err != nil {
		return err
	}
	if err := requireCode(unboundResponse, 409, string(domain.CodeProcessConfigUnbound),
		"approve without process config"); err != nil {
		return err
	}

	// Binding a missing configuration series must fail without partial writes.
	missingBind, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/process-config",
		map[string]any{"process_config_id": "pconf_doesnotexist"}, ptrInt64(2))
	if err != nil {
		return err
	}
	if err := requireCode(missingBind, 404, string(domain.CodeProcessConfigNotFound),
		"bind missing process config"); err != nil {
		return err
	}

	bindResponse, err := api.call("PUT", "/v1/formulas/"+formula.ID+"/process-config",
		map[string]any{"process_config_id": config.ID}, ptrInt64(2))
	if err != nil {
		return err
	}
	if err := requireStatus(bindResponse, 200, "bind process config"); err != nil {
		return err
	}
	if err := bindResponse.decode(&formula); err != nil {
		return err
	}
	if formula.Version != 3 {
		return fmt.Errorf("process config binding did not produce revision 3")
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
	if readBack.State != domain.FormulaApproved || len(readBack.Steps) != 3 {
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

func ensureApprovedFormula(api *client, name string) (formulaView, error) {
	config, err := ensureProcessConfig(api, "Config for "+name)
	if err != nil {
		return formulaView{}, err
	}
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
	if err := approveFormulaWithConfig(api, formula.ID, config.ID); err != nil {
		return formulaView{}, err
	}
	readResponse, err := api.call("GET", "/v1/formulas/"+formula.ID, nil, nil)
	if err != nil {
		return formulaView{}, err
	}
	if err := requireStatus(readResponse, 200, "read approved setup formula"); err != nil {
		return formulaView{}, err
	}
	if err := readResponse.decode(&formula); err != nil {
		return formulaView{}, err
	}
	return formula, nil
}

func ptrInt64(value int64) *int64 {
	return &value
}
