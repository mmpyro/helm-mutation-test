package runner

import (
	"context"
	"testing"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

func TestCheckEquivalenceOnlyTouchesSurvivors(t *testing.T) {
	// The pass may only ever turn Survived into Equivalent. Anything else would
	// let it rewrite a kill, an invalid or a no-coverage verdict.
	mutants := []model.Mutant{
		{ID: "k", Status: model.StatusKilled, File: "templates/deployment.yaml"},
		{ID: "n", Status: model.StatusNoCoverage, File: "templates/deployment.yaml"},
		{ID: "i", Status: model.StatusInvalid, File: "templates/deployment.yaml"},
	}
	before := make([]model.Status, len(mutants))
	for i, m := range mutants {
		before[i] = m.Status
	}

	CheckEquivalence(context.Background(), mutants, EquivalenceInput{
		ChartDir: fixtureChart,
		Parallel: 2,
	})

	for i, m := range mutants {
		if m.Status != before[i] {
			t.Errorf("%s: status changed from %s to %s", m.ID, before[i], m.Status)
		}
	}
}
