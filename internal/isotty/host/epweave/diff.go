package epweave

// addedLines returns the lines of next that are not matched by the longest
// common subsequence with prev, in order. Pads are small, so the quadratic
// table is fine.
func addedLines(prev, next []string) []string {
	n, m := len(prev), len(next)
	table := make([][]int, n+1)
	for i := range table {
		table[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if prev[i] == next[j] {
				table[i][j] = table[i+1][j+1] + 1
			} else {
				table[i][j] = max(table[i+1][j], table[i][j+1])
			}
		}
	}
	added := []string{}
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case prev[i] == next[j]:
			i++
			j++
		case table[i+1][j] >= table[i][j+1]:
			i++
		default:
			added = append(added, next[j])
			j++
		}
	}
	return append(added, next[j:]...)
}
