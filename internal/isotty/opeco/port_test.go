package opeco

import "testing"

func TestChooseQRPort(t *testing.T) {
	for _, tc := range []struct {
		taken []int
		want  int
	}{
		{nil, 61002},
		{[]int{61002}, 61003},
		{[]int{61003}, 61002},
		{[]int{61003, 61002, 61004}, 61005},
		{[]int{61002, 61002, 61004}, 61003},
	} {
		if got := ChooseQRPort(tc.taken); got != tc.want {
			t.Fatalf("ChooseQRPort(%v) = %d, want %d", tc.taken, got, tc.want)
		}
	}
}
