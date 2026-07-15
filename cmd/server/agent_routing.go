package main

import (
	"fmt"

	"github.com/iac-studio/iac-studio/internal/agentrouting"
	"github.com/iac-studio/iac-studio/internal/agentruns"
)

type agentRoutingServices struct {
	runs   *agentruns.Store
	router *agentrouting.Router
}

func newAgentRoutingServices(evaluator agentrouting.ToolEvaluator) (*agentRoutingServices, error) {
	runs := agentruns.NewStore()
	policies := agentrouting.NewPolicyStore()
	authorizer, err := agentrouting.NewStoreAuthorizer(policies, evaluator)
	if err != nil {
		return nil, fmt.Errorf("create tool route authorizer: %w", err)
	}
	recorder, err := agentrouting.NewRunRecorder(runs)
	if err != nil {
		return nil, fmt.Errorf("create tool route recorder: %w", err)
	}
	router, err := agentrouting.NewRouter(authorizer, recorder)
	if err != nil {
		return nil, fmt.Errorf("create tool route router: %w", err)
	}
	return &agentRoutingServices{
		runs:   runs,
		router: router,
	}, nil
}
