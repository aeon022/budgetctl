package budget

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aeon022/missionctl-core/activity"
)

func isolateActivity(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("MISSIONCTL_DATA_DIR", t.TempDir())
	t.Setenv("MISSIONCTL_ACTIVITY", "")
}

func todaysEvents(t *testing.T) []activity.Event {
	t.Helper()
	from, to := activity.Day(time.Now())
	evs, err := activity.Read(from, to)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

const activityCSV = `Date,Payee,Account number,Transaction type,Payment reference,Amount (EUR),Amount (Foreign Currency),Type Foreign Currency,Exchange Rate
2026-01-15,REWE Markt,,MasterCard Payment,Groceries,-42.37,,,
2026-01-16,Employer GmbH,,Income,Salary January,2500.00,,,
`

func TestImportLogsOneCountOnlyEvent(t *testing.T) {
	isolateActivity(t)
	path := writeTemp(t, "n26.csv", activityCSV)
	if _, err := ImportFile(context.Background(), testStore(t), path, "Joint Account", false); err != nil {
		t.Fatal(err)
	}
	evs := todaysEvents(t)
	if len(evs) != 1 || evs[0].Tool != "budgetctl" || evs[0].Action != "imported" || evs[0].Title != "2 transactions" {
		t.Fatalf("events = %+v, want exactly one 'imported / 2 transactions'", evs)
	}
	// Check the logged fields, not the raw line: a timestamp like ":42.37…"
	// would match a secret by chance (clock-dependent flake).
	logged := evs[0].Tool + " " + evs[0].Action + " " + evs[0].Title
	for _, secret := range []string{"REWE", "Employer", "42.37", "2500", "Groceries", "Salary", "Joint Account"} {
		if strings.Contains(logged, secret) {
			t.Errorf("activity log leaks %q: %q", secret, logged)
		}
	}
}

func TestImportOfNothingLogsNothing(t *testing.T) {
	isolateActivity(t)
	LogImported(0)
	if evs := todaysEvents(t); len(evs) != 0 {
		t.Errorf("zero imported must not log: %+v", evs)
	}
}

func TestImportStillWorksWithActivityOff(t *testing.T) {
	isolateActivity(t)
	t.Setenv("MISSIONCTL_ACTIVITY", "off")
	res, err := ImportFile(context.Background(), testStore(t), writeTemp(t, "n26.csv", activityCSV), "", false)
	if err != nil || res.Imported != 2 {
		t.Fatalf("import must succeed with logging off: %+v %v", res, err)
	}
	if evs := todaysEvents(t); len(evs) != 0 {
		t.Errorf("logging off must log nothing: %+v", evs)
	}
}
