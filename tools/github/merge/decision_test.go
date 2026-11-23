package merge

import "testing"

func TestPlanAllowsOnlyMaintainersAndAdministrators(t *testing.T) {
	for _, test := range []struct {
		role string
		want bool
	}{
		{role: "admin", want: true},
		{role: "maintain", want: true},
		{role: "write", want: false},
		{role: "triage", want: false},
		{role: "", want: false},
	} {
		t.Run(test.role, func(t *testing.T) {
			current := &state{}
			current.observed.role = test.role
			current.plan()
			if current.decision.allowed != test.want {
				t.Fatalf("role %q allowed = %v, want %v", test.role, current.decision.allowed, test.want)
			}
		})
	}
}
