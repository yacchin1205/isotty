package opeco

import "sort"

// The host browser must reach opeco's QR page at the address opeco serves it
// on in the VM, and opeco learns that port when the agent starts. The port is
// therefore fixed per project for the life of its VM, and chosen on the host so
// that no two projects attached from one host share it.
const qrPortBase = 61002

// ChooseQRPort returns the lowest port from qrPortBase that no other project
// on this host holds.
func ChooseQRPort(taken []int) int {
	sorted := append([]int(nil), taken...)
	sort.Ints(sorted)
	port := qrPortBase
	for _, t := range sorted {
		if t == port {
			port++
		}
	}
	return port
}
