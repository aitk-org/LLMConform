package main

import "testing"

func TestBuildRunPlanLevelsHaveDistinctScope(t *testing.T) {
	t.Parallel()
	tests := []struct {
		level          string
		scenarios      int
		modelCalls     int
		maxOutputToken int
	}{
		{level: LevelQuick, scenarios: 6, modelCalls: 3, maxOutputToken: 96},
		{level: LevelStandard, scenarios: 24, modelCalls: 9, maxOutputToken: 384},
		{level: LevelFull, scenarios: 30, modelCalls: 15, maxOutputToken: 576},
	}
	for _, test := range tests {
		test := test
		t.Run(test.level, func(t *testing.T) {
			t.Parallel()
			plan, err := BuildRunPlan(RunConfig{
				BaseURL: "https://example.com",
				Model:   "model",
				Profile: ProfileGateway,
				Level:   test.level,
				Routes:  allRouteIDs(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if plan.ScenarioCount != test.scenarios || plan.ModelCalls != test.modelCalls || plan.MaxOutputTokens != test.maxOutputToken {
				t.Fatalf("plan counts = scenarios %d, calls %d, tokens %d; want %d, %d, %d",
					plan.ScenarioCount, plan.ModelCalls, plan.MaxOutputTokens,
					test.scenarios, test.modelCalls, test.maxOutputToken,
				)
			}
		})
	}
}

func TestRunConfigDefaultsRoutesFromProfile(t *testing.T) {
	t.Parallel()
	cfg := RunConfig{BaseURL: "https://example.com", Model: "model", Profile: ProfileClaude, Level: LevelQuick}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Routes) != 1 || cfg.Routes[0] != RouteMessages {
		t.Fatalf("routes = %v, want [%s]", cfg.Routes, RouteMessages)
	}
}
