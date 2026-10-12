package report

import (
	"fmt"

	"github.com/mmpyro/helm-mutation-test/internal/model"
)

// incrementalNotices is the wording every format uses for a --since run, so the
// five reports cannot drift into saying different things about what was left
// out. Empty for a full run without --since.
func incrementalNotices(run *model.Run) []string {
	inc := run.Incremental
	if inc == nil {
		return nil
	}
	var out []string
	if inc.FullRunReason != "" {
		out = append(out, fmt.Sprintf("--since %s: full run, %s", inc.Base, inc.FullRunReason))
	} else {
		out = append(out, fmt.Sprintf(
			"changed-files mode (--since %s): %d files inspected, %d untouched files skipped, "+
				"%d mutants not evaluated; score covers the diff only",
			inc.Base, inc.FilesInspected, inc.FilesSkipped, inc.MutantsSkipped))
	}
	if run.NothingToScore() {
		msg := "nothing in the diff to score"
		if run.Threshold > 0 {
			msg += "; threshold not applied"
		}
		out = append(out, msg)
	}
	return out
}
