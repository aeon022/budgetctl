package budget

import (
	"fmt"

	"github.com/aeon022/missionctl-core/activity"
)

// LogAdded and LogImported write to the suite activity log. Privacy: counts
// only — never amounts, accounts, payees or descriptions.
func LogAdded() { activity.Log("budgetctl", "added", "a transaction") }

func LogImported(n int) {
	if n > 0 {
		activity.Log("budgetctl", "imported", fmt.Sprintf("%d transactions", n))
	}
}
