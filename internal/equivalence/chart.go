package equivalence

import (
	"fmt"

	"gopkg.in/yaml.v3"
	"helm.sh/helm/v3/pkg/chart"
)

// WithMutatedFile returns a shallow clone of chrt with one chart-relative file
// replaced by data. The receiver is never modified: a single loaded chart is the
// base for every mutant, and a mutation leaking into it would contaminate the
// rest of the run.
//
// An unknown path is an error rather than a no-op. Rendering the unmutated chart
// would compare it against itself and declare the mutant equivalent — exactly
// the false verdict this feature must not produce.
func WithMutatedFile(chrt *chart.Chart, relPath string, data []byte) (*chart.Chart, error) {
	for _, valuesName := range []string{"values.yaml", "values.yml"} {
		if relPath == valuesName {
			var values map[string]any
			if err := yaml.Unmarshal(data, &values); err != nil {
				return nil, fmt.Errorf("parsing mutated %s: %w", relPath, err)
			}
			clone := *chrt
			clone.Values = values
			return &clone, nil
		}
	}

	for i, f := range chrt.Templates {
		if f.Name != relPath {
			continue
		}
		templates := make([]*chart.File, len(chrt.Templates))
		copy(templates, chrt.Templates)
		templates[i] = &chart.File{Name: f.Name, Data: data}
		clone := *chrt
		clone.Templates = templates
		return &clone, nil
	}
	return nil, fmt.Errorf("chart has no file %q", relPath)
}
