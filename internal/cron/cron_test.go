package cron

import "testing"

// TestValidate checks that well-formed cron expressions are accepted and that a
// range of malformed ones (wrong field count, out-of-range values, zero steps,
// backwards ranges, unsupported macros) are rejected.
func TestValidate(t *testing.T) {
	good := []string{"*/15 * * * *", "0 6 * * 1-5", "0 0 1,15 jan-mar *", "@daily", "30 4 * * mon", "0 */3 * * *"}
	bad := []string{"", "* * * *", "60 * * * *", "* 24 * * *", "*/0 * * * *", "5-1 * * * *", "@reboot", "a b c d e", "* * 0 * *"}
	for _, g := range good {
		if err := Validate(g); err != nil {
			t.Errorf("%q should be valid: %v", g, err)
		}
	}
	for _, b := range bad {
		if err := Validate(b); err == nil {
			t.Errorf("%q should be invalid", b)
		}
	}
}

// TestDescribe checks that preset expressions get a friendly label, ignoring
// irregular whitespace.
func TestDescribe(t *testing.T) {
	if got := Describe("*/30  * * * *"); got != "every 30 minutes" {
		t.Errorf("got %q", got)
	}
}
