package sum

import "testing"

func TestTo(t *testing.T) {
	for n, want := range map[int]int{0: 0, 1: 1, 4: 10, 100: 5050} {
		if got := To(n); got != want {
			t.Errorf("To(%d) = %d, want %d", n, got, want)
		}
	}
}
