package sum

// To returns 1 + 2 + ... + n.
func To(n int) int {
	total := 0
	for i := 1; i < n; i++ {
		total += i
	}
	return total
}
