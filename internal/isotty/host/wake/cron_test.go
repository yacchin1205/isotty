package wake

import (
	"strings"
	"testing"
	"time"
)

func TestCronNext(t *testing.T) {
	loc := time.FixedZone("JST", 9*3600)
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	for _, c := range []struct{ spec, from, want string }{
		{"0 6 * * *", "2026-11-26 07:00", "2026-11-27 06:00"},
		{"0 6 * * *", "2026-11-26 05:59", "2026-11-26 06:00"},
		{"0 6 * * *", "2026-11-26 06:00", "2026-11-27 06:00"},
		{"*/15 * * * *", "2026-11-26 07:01", "2026-11-26 07:15"},
		{"0 9 * * 1-5", "2026-11-27 10:00", "2026-11-30 09:00"}, // Friday to Monday
		{"30 8 1 * *", "2026-11-26 07:00", "2026-12-01 08:30"},
		{"0 0 29 2 *", "2026-11-26 07:00", "2028-02-29 00:00"},
		{"0 12 * * 0", "2026-11-26 07:00", "2026-11-29 12:00"},
		{"0 12 * * 7", "2026-11-26 07:00", "2026-11-29 12:00"},
		{"0 6,18 * * *", "2026-11-26 07:00", "2026-11-26 18:00"},
	} {
		cron, err := ParseCron(c.spec)
		if err != nil {
			t.Fatalf("%s: %v", c.spec, err)
		}
		if got := cron.Next(at(c.from)); !got.Equal(at(c.want)) {
			t.Errorf("%s after %s = %s, want %s", c.spec, c.from, got.Format("2006-01-02 15:04"), c.want)
		}
	}
	for _, bad := range []string{"0 6 * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "*/0 * * * *", "a * * * *", "5-1 * * * *"} {
		if _, err := ParseCron(bad); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

// A recurring wake moves on past the times it missed; a one-time one goes.
func TestDoneMovesARecurringWakeOn(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 11, 26, 7, 0, 0, 0, time.Local)
	daily, err := Add(home, "abc", Request{Agent: "claude", Session: "s", At: now.AddDate(0, 0, -3), Cron: "0 6 * * *", Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	once, err := Add(home, "abc", Request{Agent: "claude", Session: "s", At: now, Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Done(home, "abc", daily, now); err != nil {
		t.Fatal(err)
	}
	if err := Done(home, "abc", once, now); err != nil {
		t.Fatal(err)
	}
	got, err := Load(home, "abc")
	if err != nil || len(got) != 1 || got[0].ID != daily.ID || !got[0].At.Equal(time.Date(2026, 11, 27, 6, 0, 0, 0, time.Local)) {
		t.Fatalf("after done: %+v %v", got, err)
	}
	if _, err := Add(home, "abc", Request{Agent: "claude", Session: "s", At: now, Cron: "61 * * * *", Prompt: "p"}); err == nil {
		t.Fatal("a bad cron was kept")
	}
}

// A one-time wake that never can be delivered is given up after its last
// try; one that waited for a busy conversation is not counted.
func TestFailedGivesUpAfterTheLastTry(t *testing.T) {
	home := t.TempDir()
	now := time.Now().Truncate(time.Second)
	w, err := Add(home, "abc", Request{Agent: "claude", Session: "s", At: now, Prompt: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if err := Postpone(home, "abc", w.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= MaxFailures; i++ {
		dropped, err := Failed(home, "abc", w.ID, now.Add(time.Duration(i)*time.Minute))
		if err != nil || dropped != (i == MaxFailures) {
			t.Fatalf("try %d: dropped %v, err %v", i, dropped, err)
		}
	}
	if got, _ := Load(home, "abc"); len(got) != 0 {
		t.Fatalf("kept after the last try: %+v", got)
	}
	if _, err := Add(home, "abc", Request{Agent: "claude", Session: "s", At: now, Prompt: strings.Repeat("x", MaxPrompt+1)}); err == nil {
		t.Fatal("an overlong prompt was kept")
	}
}
